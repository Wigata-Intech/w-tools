package logger

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
)

// maxDepth bounds traversal of nested values, so pointer cycles terminate.
// Anything deeper logs as Unloggable.
const maxDepth = 16

// RedactConfig declares which keys carry sensitive values. Keys are bare —
// no dotted paths — and match case-insensitively at any depth: a rule for
// "password" covers the key wherever it appears.
type RedactConfig struct {
	// Redacted keys have their value replaced entirely with Replacement.
	Redacted []string

	// Replacement is written for Redacted keys. Default DefaultReplacement.
	Replacement string

	// Masked keys keep only the configured leading/trailing characters.
	// A key in both Redacted and Masked is redacted.
	Masked map[string]Mask
}

// Mask keeps ShowFirst leading and ShowLast trailing characters, replacing
// the middle with MaskChar. Values with no middle to hide are masked
// entirely — a mask never reveals more of a short value than a long one.
type Mask struct {
	ShowFirst int
	ShowLast  int
	MaskChar  rune // default '*'
}

type rule struct {
	mask   Mask
	redact bool
}

type redactHandler struct {
	inner       slog.Handler
	rules       map[string]rule
	replacement string
	plans       *sync.Map // reflect.Type -> []fieldPlan, shared across derived handlers
}

// wrapRedact layers redaction over h; with no rules configured it
// returns h unchanged.
func wrapRedact(h slog.Handler, r RedactConfig) slog.Handler {
	if len(r.Redacted) == 0 && len(r.Masked) == 0 {
		return h
	}
	rules := make(map[string]rule, len(r.Redacted)+len(r.Masked))
	for k, m := range r.Masked {
		rules[strings.ToLower(k)] = rule{mask: m}
	}
	for _, k := range r.Redacted {
		rules[strings.ToLower(k)] = rule{redact: true}
	}
	repl := r.Replacement
	if repl == "" {
		repl = DefaultReplacement
	}
	return &redactHandler{inner: h, rules: rules, replacement: repl, plans: new(sync.Map)}
}

func (h *redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i], _ = h.process(a, 0)
	}
	return &redactHandler{inner: h.inner.WithAttrs(out), rules: h.rules, replacement: h.replacement, plans: h.plans}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{inner: h.inner.WithGroup(name), rules: h.rules, replacement: h.replacement, plans: h.plans}
}

func (h *redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	changed := false
	attrs := make([]slog.Attr, 0, rec.NumAttrs())
	rec.Attrs(func(a slog.Attr) bool {
		p, c := h.process(a, 0)
		changed = changed || c
		attrs = append(attrs, p)
		return true
	})
	if !changed {
		return h.inner.Handle(ctx, rec)
	}
	out := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	out.AddAttrs(attrs...)
	return h.inner.Handle(ctx, out)
}

// process applies the rules to one attr. The returned bool reports whether
// anything was rewritten; untouched attrs are returned as-is so records with
// no sensitive keys pass through unmodified.
func (h *redactHandler) process(a slog.Attr, depth int) (slog.Attr, bool) {
	if depth >= maxDepth {
		return slog.String(a.Key, Unloggable), true
	}
	if r, ok := h.rules[strings.ToLower(a.Key)]; ok {
		if r.redact {
			return slog.String(a.Key, h.replacement), true
		}
		rv, ok := resolve(a.Value)
		if !ok {
			return slog.String(a.Key, Unloggable), true
		}
		return slog.String(a.Key, maskString(valueString(rv), r.mask)), true
	}
	rv, ok := resolve(a.Value)
	if !ok {
		return slog.String(a.Key, Unloggable), true
	}
	switch rv.Kind() {
	case slog.KindGroup:
		members := rv.Group()
		out := make([]slog.Attr, len(members))
		changed := false
		for i, m := range members {
			p, c := h.process(m, depth+1)
			out[i] = p
			changed = changed || c
		}
		if !changed {
			return a, false
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}, true
	case slog.KindAny:
		sanitized, changed := h.sanitize(reflect.ValueOf(rv.Any()), depth)
		if !changed {
			return a, false
		}
		return slog.Any(a.Key, sanitized), true
	default:
		return a, false
	}
}

// sanitize walks a reflected value looking for rule matches, returning a
// plain map/slice/scalar tree. The bool reports whether any rule applied;
// callers keep the original value when nothing matched, preserving
// json.Marshal behavior (MarshalJSON, omitempty) for untouched values.
func (h *redactHandler) sanitize(v reflect.Value, depth int) (any, bool) {
	if depth >= maxDepth {
		return Unloggable, true
	}
	switch v.Kind() {
	case reflect.Invalid:
		return nil, false
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil, false
		}
		return h.sanitize(v.Elem(), depth+1)
	case reflect.Struct:
		m := make(map[string]any)
		changed := false
		for _, f := range h.plan(v.Type()) {
			fv := v.FieldByIndex(f.index)
			if r, ok := h.rules[f.lowerName]; ok {
				changed = true
				if r.redact {
					m[f.name] = h.replacement
				} else {
					m[f.name] = maskString(fmt.Sprint(fv.Interface()), r.mask)
				}
				continue
			}
			sv, c := h.sanitize(fv, depth+1)
			m[f.name] = sv
			changed = changed || c
		}
		return m, changed
	case reflect.Map:
		m := make(map[string]any, v.Len())
		changed := false
		iter := v.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			if r, ok := h.rules[strings.ToLower(key)]; ok {
				changed = true
				if r.redact {
					m[key] = h.replacement
				} else {
					m[key] = maskString(fmt.Sprint(iter.Value().Interface()), r.mask)
				}
				continue
			}
			sv, c := h.sanitize(iter.Value(), depth+1)
			m[key] = sv
			changed = changed || c
		}
		return m, changed
	case reflect.Slice, reflect.Array:
		s := make([]any, v.Len())
		changed := false
		for i := range v.Len() {
			sv, c := h.sanitize(v.Index(i), depth+1)
			s[i] = sv
			changed = changed || c
		}
		return s, changed
	default:
		return v.Interface(), false
	}
}

// maxLogValues bounds LogValuer resolution chains, mirroring slog's own guard.
const maxLogValues = 10

// resolve resolves LogValuer chains with our own recover. slog's
// Value.Resolve catches LogValuer panics itself and substitutes a string
// carrying a stack trace — resolving here keeps that trace out of the output
// and lets the caller fail closed instead.
func resolve(v slog.Value) (slog.Value, bool) {
	for range maxLogValues {
		if v.Kind() != slog.KindLogValuer {
			return v, true
		}
		next, ok := safeLogValue(v.LogValuer())
		if !ok {
			return slog.Value{}, false
		}
		v = next
	}
	return slog.Value{}, false
}

//nolint:nonamedreturns // the recover pattern needs named returns to set ok.
func safeLogValue(lv slog.LogValuer) (out slog.Value, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return lv.LogValue(), true
}

func valueString(v slog.Value) string {
	if v.Kind() == slog.KindString {
		return v.String()
	}
	return fmt.Sprint(v.Any())
}

// fieldPlan is one exported struct field: its output name (json tag wins),
// the precomputed lowercase for rule lookup, and its index path.
type fieldPlan struct {
	name      string
	lowerName string
	index     []int
}

// plan memoizes struct field plans per type, so reflection cost is paid once
// per type rather than per record.
func (h *redactHandler) plan(t reflect.Type) []fieldPlan {
	if p, ok := h.plans.Load(t); ok {
		if fields, ok := p.([]fieldPlan); ok {
			return fields
		}
	}
	fields := make([]fieldPlan, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("json"); ok {
			base, _, _ := strings.Cut(tag, ",")
			if base == "-" {
				continue
			}
			if base != "" {
				name = base
			}
		}
		fields = append(fields, fieldPlan{name: name, lowerName: strings.ToLower(name), index: f.Index})
	}
	h.plans.Store(t, fields)

	return fields
}
