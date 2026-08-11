# httpx

> Everything `net/http` makes you wire by hand — timeouts, groups, middleware, JSON errors — without ever hiding `net/http` from you.

**Status: v0, in development.** The API below is real and tested, but unreleased and still allowed to move. First tag lands when the middleware set is in.

## TL;DR

- A server that's production-safe by default: every timeout on, graceful shutdown in one call
- Route groups with shared prefixes and middleware over the stdlib `ServeMux` — every method routable, including RFC 10008 `QUERY`
- JSON in and out: size-capped `Bind`, and errors as RFC 9457 `application/problem+json` by default
- A standard middleware set: `RealIP`, `RequestID`, `Trace` (W3C traceparent), `Recover`, `Logger` — with request/response body logging that plugs into your logger's redaction
- Handlers stay plain `http.HandlerFunc` — nothing to learn, nothing to eject from
- Zero dependencies, permanently

## What problem this solves

Since Go 1.22, `ServeMux` routes by method and pattern natively — you don't need a framework for routing anymore. But the stdlib still leaves real work to every service: `http.Server` ships with **no timeouts** (slowloris-open by default) and no shutdown wiring, there are no route groups sharing a prefix and middleware chain, and JSON boilerplate — capped decoding, a consistent error shape — gets reinvented per repo.

httpx fills exactly that list, and nothing more. It is deliberately not a framework: no custom handler signature, no context reinvention, no routing engine of its own.

## How it solves it

```go
s := httpx.New(httpx.Config{Addr: ":8080"}) // production timeouts on by default

api := s.Group("/api/v1")
api.Get("/orders/{id}", getOrder)     // r.PathValue("id"), stdlib-native
api.Post("/orders", createOrder)
api.Query("/orders/search", search)   // HTTP QUERY, RFC 10008 — filters in the body

ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
_ = s.Run(ctx) // serves until SIGTERM, then drains gracefully
```

Inside a handler:

```go
func createOrder(w http.ResponseWriter, r *http.Request) {
    var in OrderInput
    if err := httpx.Bind(r, &in); err != nil { // JSON, capped at 1 MiB by default
        httpx.Error(w, http.StatusBadRequest, "invalid order payload")
        return
    }
    httpx.JSON(w, http.StatusCreated, in)
}
```

Errors default to [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457): `{"type":"about:blank","title":"Bad Request","status":400,"detail":"..."}` — and services with their own error format swap it via `ErrorWriter`.

Middleware wires in canonical order — outermost first, so the logger sees the real client IP, the IDs, and the panic-turned-500 with its latency:

```go
s.Use(
    middleware.RealIP(middleware.RealIPConfig{TrustedProxies: proxies}),
    middleware.RequestID(middleware.RequestIDConfig{}), // reuses inbound X-Request-ID, mints otherwise
    middleware.Trace(),                                 // W3C traceparent in, ids in ctx — no OTel dependency
    middleware.Logger(middleware.LoggerConfig{Log: log.Slog()}),
    middleware.Recover(middleware.RecoverConfig{Log: log.Slog()}),
)
```

Your own middleware plugs into the same slots — the chain type is the ecosystem's `func(http.Handler) http.Handler`, so anything written for that convention drops in unchanged.

## Why it matters

Because there's no lock-in in either direction: anything written for `net/http` drops into httpx unchanged, and anything written for httpx runs under bare `net/http` — ejecting costs you a router file, not a rewrite. Because the defaults are the safe ones: the dangerous zero values (`no timeout`, unbounded bodies) are not expressible. And because it speaks current standards from day one — RFC 10008 `QUERY` routed, bound, and validated per the spec's server rules (a QUERY without a `Content-Type` is rejected, as the RFC requires); RFC 9457 for every error body.

## What it costs

Not measured yet — the benchmark suite lands with the middleware phase, and numbers get published here the same way logger's were. What's true by construction today: groups are registration-time sugar, so at request time there is only the one `ServeMux` — grouping adds **zero** per-request routing cost over the stdlib.

## The promises

As of today, v0 unreleased:

- **We never wrap or rename what `net/http` defines.** Handlers, `ResponseWriter`, request types, mux patterns — the stdlib shapes are the API, always.
- **Safe by default.** Every timeout on from the zero config; body reads capped; "no timeout" is not a thing you can configure.
- **Fail loud at boot, not silent in production.** Misregistration panics at startup exactly like `ServeMux`; nothing degrades silently.
- **Zero dependencies.** The `go.mod` stays empty — that's a feature, and it's permanent.

Coming next, in order: the gate middleware (`CORS`, `RateLimit`), BFF HTML rendering, and an outbound client with tuned pooling and a circuit-breaker hook — the full plan is in [ROADMAP.md](../ROADMAP.md).
