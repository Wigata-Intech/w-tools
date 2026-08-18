// Context enrichment: request identity flows from ctx onto every line.
// The extractor func is the seam — the logger never reads context keys
// itself; the application wires its middleware's accessors in.
package main

import (
	"context"
	"log/slog"

	"github.com/Wigata-Intech/w-tools/logger"
)

// requestIDKey stands in for a middleware's unexported context key; in
// a real service the extractor calls the middleware's accessor
// (e.g. httpx middleware.RequestIDFrom) instead of reading ctx itself.
type requestIDKey struct{}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func main() {
	log := logger.New(logger.Config{
		Env: "production",
		App: "orders",
		ContextAttrs: func(ctx context.Context) []slog.Attr {
			var a []slog.Attr
			if id := requestIDFrom(ctx); id != "" {
				a = append(a, slog.String("request_id", id))
			}
			return a
		},
	})

	ctx := context.WithValue(context.Background(), requestIDKey{}, "req-8f3a9c")

	// Layers deep in the service hold only ctx — the id still lands.
	log.Info(ctx, "payment created", "order_id", "ord_123")
	// ..."order_id":"ord_123","request_id":"req-8f3a9c"

	// A call-site attr with the same key wins; the line never repeats it.
	log.Info(ctx, "replayed from cache", "request_id", "req-original")
	// ..."request_id":"req-original"

	// No value in ctx, nothing added — a nil ContextAttrs disables enrichment.
	log.Info(context.Background(), "background job finished", "job", "reconcile")
	// ..."job":"reconcile" (no request_id)
}
