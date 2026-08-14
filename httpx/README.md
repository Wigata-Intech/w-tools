# httpx

> Everything `net/http` makes you wire by hand — timeouts, groups, middleware, JSON errors — without ever hiding `net/http` from you.

[![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/w-tools/httpx.svg)](https://pkg.go.dev/github.com/Wigata-Intech/w-tools/httpx)

**Status: v0, released.** Production-track at Wigata InTech. Semver v0 applies: the API can still move between minor versions until `v1.0.0`, which lands only after surviving production use.

## TL;DR

```bash
go get github.com/Wigata-Intech/w-tools/httpx
```

- A server that's production-safe by default: every timeout on, graceful shutdown in one call
- Route groups with shared prefixes and middleware over the stdlib `ServeMux` — every method routable, including RFC 10008 `QUERY`
- JSON in and out: size-capped `Bind`, and errors as RFC 9457 `application/problem+json` by default
- A standard middleware set: `RealIP`, `RequestID`, `Trace` (W3C traceparent), `Recover`, `Logger` — with request/response body logging that plugs into your logger's redaction — plus the gates: `CORS` and `RateLimit` (pluggable `Limiter`)
- BFF-ready HTML rendering (`Renderer` — templ satisfies it natively, `html/template` via the built-in adapter) and `ErrorMap` for one-line domain-error responses
- An outbound `client`: pooling tuned for services (not the stdlib's 2 idle conns/host), a timeout you can't turn off, a circuit-breaker hook, trace propagation, and opt-in logging where redaction follows your logger
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

### Using the middleware

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

Your own middleware plugs into the same slots — the chain type is the ecosystem's `func(http.Handler) http.Handler`, so anything written for that convention drops in unchanged. Per-middleware behavior and gotchas: [middleware/README.md](middleware/).

### Using the client

The outbound half: pooling tuned for services, a timeout you can't turn off, a breaker seam, trace propagation, redaction-inheriting logging.

```go
c := client.New(client.Config{Log: log.Slog(), Breaker: breaker})
resp, err := c.Get(ctx, "https://api.upstream.example/orders")
```

Build one client per upstream at boot and reuse it — the pool is the point. Details: [client/README.md](client/).

### Recipe: pprof on an internal debug server

`net/http/pprof` mounts on httpx as-is — no adapter, no import side effects. Run it as a **second, internal-only server** in the same process: your public server keeps its strict timeouts and middleware chain, while the debug listener stays unreachable from outside and tolerant of long profile streams (a `WriteTimeout` shorter than `?seconds=30` would cut a CPU profile mid-capture):

```go
debug := httpx.New(httpx.Config{
    Addr:         "127.0.0.1:6060",  // never behind the public proxy
    WriteTimeout: 2 * time.Minute,   // must outlast ?seconds=N profile streams
})
debug.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index)) // also serves heap, goroutine, allocs, mutex, block
debug.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
debug.HandleFunc("/debug/pprof/profile", pprof.Profile)
debug.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
debug.HandleFunc("/debug/pprof/trace", pprof.Trace)
go func() { _ = debug.Run(ctx) }() // same ctx: drains with the main server
```

Never expose pprof publicly — heap dumps can contain secrets held in memory, and CPU profiling is a free denial-of-service lever.

## Why it matters

Because there's no lock-in in either direction: anything written for `net/http` drops into httpx unchanged, and anything written for httpx runs under bare `net/http` — ejecting costs you a router file, not a rewrite. Because the defaults are the safe ones: the dangerous zero values (`no timeout`, unbounded bodies) are not expressible. And because it speaks current standards from day one — RFC 10008 `QUERY` routed, bound, and validated per the spec's server rules (a QUERY without a `Content-Type` is rejected, as the RFC requires); RFC 9457 for every error body.

## What it costs

Measured with `go test -bench=. -benchmem` (Apple Silicon, output discarded). Read it as a price list:

| Situation | ns/op | allocs/op | Meaning for you |
| --------- | ----- | --------- | --------------- |
| Raw `ServeMux` routing | ~109 | 2 | The stdlib baseline |
| The same route through nested groups | ~114 | 2 | **Grouping is free** — parity within noise, identical allocations, because groups are registration-time sugar |
| Request floor (build + bare handler) | ~139 | 3 | What the middleware numbers subtract |
| `Logger` middleware, capture off | ~1,112 | 8 | ~1µs per request — almost all of it the JSON access line itself |
| `Logger` with request-body capture | ~2,545 | 40 | The opt-in costs ~1.4µs more: capture, parse, structured attr |
| Full canonical chain (RealIP → RequestID → Trace → Logger → Recover) | ~2,567 | 32 | Your whole production identity stack: under 2.5µs of overhead per request |

The practical takeaway: the expensive thing in the stack is writing a log line, not the middleware machinery around it — and even the everything-on chain costs less than 0.3% of a 1ms handler.

Under concurrency the chain holds flat (~2.6–3.0µs/op from 1 to 8 parallel callers — throughput scales with cores) and `RateLimit`'s single mutex stays sub-microsecond at 8 concurrent clients (~364 ns/op). Parallel variants of these benchmarks ship in the suite; run them with `-cpu 1,4,8`.

## The promises

As of v0:

- **We never wrap or rename what `net/http` defines.** Handlers, `ResponseWriter`, request types, mux patterns — the stdlib shapes are the API, always.
- **Safe by default.** Every timeout on from the zero config; body reads capped; "no timeout" is not a thing you can configure.
- **Fail loud at boot, not silent in production.** Misregistration panics at startup exactly like `ServeMux`; nothing degrades silently.
- **Zero dependencies.** The `go.mod` stays empty — that's a feature, and it's permanent.

Runnable programs live in [`examples/`](examples/): a REST service with `ErrorMap` and QUERY search, a BFF page, and the [redaction proof](examples/redaction/main.go) — the Logger middleware feeding a captured request body through [w-tools/logger](../logger/)'s rules, password `[REDACTED]` in the access line with nobody writing a careful log call. The examples run from a clone of the repo — the committed `go.work` resolves the sibling modules locally.

templ users need no adapter at all — a generated component *is* a `Renderer`:

```go
_ = httpx.Render(w, r, http.StatusOK, pages.Dashboard(user)) // templ.Component satisfies Renderer structurally
```

(No templ program ships in `examples/` — it would put a third-party dependency in the repo, and rule one is zero of those.)

Coming next: `x/circuitbreaker`, the experimental breaker that plugs into the client's `Breaker` hook — the full plan is in [ROADMAP.md](../ROADMAP.md).
