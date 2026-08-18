package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config configures New. Every field has a safe default: an empty Config
// yields an info-level JSON logger on os.Stdout with no base fields and no
// redaction.
type Config struct {
	Env     string // e.g. "production"; omitted from output when empty
	Version string // application version; omitted when empty
	App     string // application name; omitted when empty

	// Protocol tags every line with the service entry point. Use the
	// Protocol constants — ProtocolHTTP, ProtocolGRPC, ProtocolGraphQL,
	// ProtocolCron, ProtocolConsumer — or any custom value. Omitted when empty.
	Protocol Protocol

	// Level is the minimum level logged. The zero value is slog.LevelInfo,
	// so leaving it unset gives an info-level logger. For level names from
	// env vars or config files ("debug", "warn"), use ParseLevel.
	Level slog.Level

	Redact RedactConfig
	Writer io.Writer // default os.Stdout

	// ContextAttrs, when set, is called once per emitted record with the
	// record's context; the returned attrs are appended to the record
	// before redaction. Wire it to context accessors from the middleware
	// layer (request ID, trace ID, client IP) — this package never reads
	// context keys itself. Nil disables enrichment.
	ContextAttrs func(ctx context.Context) []slog.Attr
}

// Logger wraps a *slog.Logger. Create one with New.
type Logger struct {
	s     *slog.Logger
	level *slog.LevelVar
}

// New builds a Logger from cfg: JSON handler, redaction layer, and the
// non-empty base fields (env, version, app, protocol) attached to every line.
func New(cfg Config) *Logger {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	level := new(slog.LevelVar)
	level.Set(cfg.Level)
	split := &splitHandler{
		fast:   slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}),
		rename: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: renameLevel}),
	}
	s := slog.New(Wrap(split, WrapConfig{Redact: cfg.Redact, ContextAttrs: cfg.ContextAttrs}))
	var base []any
	for _, f := range [...]struct{ key, val string }{
		{"env", cfg.Env}, {"version", cfg.Version}, {"app", cfg.App}, {"protocol", string(cfg.Protocol)},
	} {
		if f.val != "" {
			base = append(base, slog.String(f.key, f.val))
		}
	}
	if len(base) > 0 {
		s = s.With(base...)
	}
	return &Logger{s: s, level: level}
}

// WrapConfig configures Wrap: the layers applied over an existing
// slog.Handler. Zero-value fields add nothing.
type WrapConfig struct {
	Redact RedactConfig

	// ContextAttrs behaves exactly as Config.ContextAttrs.
	ContextAttrs func(ctx context.Context) []slog.Attr
}

// Wrap layers redaction and context enrichment over an existing
// slog.Handler — the adoption path for services already holding a
// *slog.Logger. New is built on it:
//
//	log := slog.New(logger.Wrap(existingHandler, logger.WrapConfig{
//	    Redact:       redactCfg,
//	    ContextAttrs: fromCtx,
//	}))
//
// Enrichment runs above redaction, so extracted attrs are redacted
// like call-site attrs. An extracted attr whose key the record already
// carries is dropped — the call site wins, and a line never repeats a
// key (attrs bound with With live in the handler and are not
// consulted). Appended attrs render as if passed at the call site: a
// logger derived with WithGroup places them inside that group. A
// zero-value config returns h unchanged.
func Wrap(h slog.Handler, cfg WrapConfig) slog.Handler {
	return wrapContext(wrapRedact(h, cfg.Redact), cfg.ContextAttrs)
}

// splitHandler exists because of a slog quirk: the only way to print "PANIC"
// instead of "ERROR+4" is a ReplaceAttr, and any ReplaceAttr switches slog
// off its zero-allocation path for every record. So normal records go to a
// handler without one, and only PANIC records pay for the rename.
// The fields stay concrete (*slog.JSONHandler, not slog.Handler) so these
// calls devirtualize; interface fields measured ~20% slower per record.
type splitHandler struct {
	fast   *slog.JSONHandler
	rename *slog.JSONHandler
}

func (h *splitHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.fast.Enabled(ctx, level)
}

func (h *splitHandler) Handle(ctx context.Context, rec slog.Record) error {
	if rec.Level >= LevelPanic {
		return h.rename.Handle(ctx, rec)
	}
	return h.fast.Handle(ctx, rec)
}

// The bare type assertions below are safe: JSONHandler.WithAttrs and
// WithGroup return *JSONHandler and have since slog shipped. A comma-ok
// fallback would be dead code no test can reach; if a future toolchain ever
// changes the concrete type, every test panics on the first log line.

//nolint:forcetypeassert
func (h *splitHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &splitHandler{
		fast:   h.fast.WithAttrs(attrs).(*slog.JSONHandler),
		rename: h.rename.WithAttrs(attrs).(*slog.JSONHandler),
	}
}

//nolint:forcetypeassert
func (h *splitHandler) WithGroup(name string) slog.Handler {
	return &splitHandler{
		fast:   h.fast.WithGroup(name).(*slog.JSONHandler),
		rename: h.rename.WithGroup(name).(*slog.JSONHandler),
	}
}

// ParseLevel converts a level name — "debug", "info", "warn", "error" or
// "panic", case-insensitive — to its slog.Level, for wiring Config.Level
// from env vars or config files. Unknown names fall back to slog.LevelInfo.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "panic":
		return LevelPanic
	default:
		return slog.LevelInfo
	}
}

// Debug logs at slog.LevelDebug.
func (l *Logger) Debug(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, slog.LevelDebug, msg, args...)
}

// Info logs at slog.LevelInfo.
func (l *Logger) Info(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, slog.LevelInfo, msg, args...)
}

// Warn logs at slog.LevelWarn.
func (l *Logger) Warn(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, slog.LevelWarn, msg, args...)
}

// Error logs at slog.LevelError.
func (l *Logger) Error(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, slog.LevelError, msg, args...)
}

// Panic logs at LevelPanic, then panics with msg. The record is written
// before the panic unwinds.
func (l *Logger) Panic(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, LevelPanic, msg, args...)
	panic(msg)
}

// With returns a child Logger with args bound to every subsequent record.
// Bound args pass through redaction like any other attribute.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{s: l.s.With(args...), level: l.level}
}

// Slog returns the underlying *slog.Logger for code that wants slog directly.
func (l *Logger) Slog() *slog.Logger {
	return l.s
}

// renameLevel renders LevelPanic as "PANIC" instead of slog's "ERROR+4".
func renameLevel(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.LevelKey {
		if lvl, ok := a.Value.Any().(slog.Level); ok && lvl >= LevelPanic {
			return slog.String(slog.LevelKey, "PANIC")
		}
	}
	return a
}
