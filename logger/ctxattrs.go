package logger

import (
	"context"
	"log/slog"
)

// wrapContext layers enrichment over h; a nil fn returns h unchanged.
func wrapContext(h slog.Handler, fn func(ctx context.Context) []slog.Attr) slog.Handler {
	if fn == nil {
		return h
	}
	return &ctxHandler{inner: h, fn: fn}
}

type ctxHandler struct {
	inner slog.Handler
	fn    func(ctx context.Context) []slog.Attr
}

func (h *ctxHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *ctxHandler) Handle(ctx context.Context, rec slog.Record) error {
	attrs := h.fn(ctx)

	var kept []slog.Attr
	for _, a := range attrs {
		dup := false
		rec.Attrs(func(ra slog.Attr) bool {
			dup = ra.Key == a.Key
			return !dup
		})
		if !dup {
			kept = append(kept, a)
		}
	}

	if len(kept) > 0 {
		rec = rec.Clone()
		rec.AddAttrs(kept...)
	}

	return h.inner.Handle(ctx, rec)
}

func (h *ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ctxHandler{inner: h.inner.WithAttrs(attrs), fn: h.fn}
}

func (h *ctxHandler) WithGroup(name string) slog.Handler {
	return &ctxHandler{inner: h.inner.WithGroup(name), fn: h.fn}
}
