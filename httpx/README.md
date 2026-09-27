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
- A standard middleware set: `RealIP`, `RequestID` (optional inbound-ID validation), `Trace` (W3C traceparent), `SecureHeaders` (JSON-API defaults, opt-in HSTS), `Recover`, `Logger` — with request/response body logging that plugs into your logger's redaction — plus the gates: `CORS`, `RateLimit` (pluggable `Limiter`), `BodyLimit` (413 on oversized bodies, chunked ones capped too), and `Idempotency` (at-most-once execution per `Idempotency-Key`, pluggable `Store`)
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

A `Problem` carries extension members at the top level of the object (RFC 9457 §3.2), after the standard members and sorted by key; an extension named like a standard member is ignored. `ErrorMap.RespondRequest` adds request-scoped members to every problem it writes — Problemer, registry match, and the bare 500 — through one `Enrich` hook:

```go
errs := httpx.NewErrorMap()
errs.Map(ErrOrderNotFound, httpx.Problem{Status: http.StatusNotFound})
errs.Enrich = func(r *http.Request, p *httpx.Problem) {
    p.Extensions["request_id"] = middleware.RequestIDFrom(r.Context()) // Extensions is always a private, non-nil map here
}

errs.RespondRequest(w, r, err) // {"type":"about:blank","title":"Not Found","status":404,"request_id":"..."}
```

Unmatched routes answer through the same writer when you opt in — `Config.ErrorWriter` turns ServeMux's plain-text `404 page not found` and `405 Method Not Allowed` into your error format, keeping the `Allow` header, and `Use` middleware still wraps them. Responses from matched routes, including a 404 your own handler writes, are never touched; left nil, ServeMux answers exactly as before:

```go
s := httpx.New(httpx.Config{
    Addr: ":8080",
    ErrorWriter: func(w http.ResponseWriter, _ *http.Request, status int, detail string) {
        httpx.Error(w, status, detail)
    },
})
```

### Using the middleware

Middleware wires in canonical order — outermost first, so the logger sees the real client IP, the IDs, and the panic-turned-500 with its latency:

```go
s.Use(
    middleware.RealIP(middleware.RealIPConfig{TrustedProxies: proxies}),
    middleware.RequestID(middleware.RequestIDConfig{}),         // reuses inbound X-Request-ID, mints otherwise
    middleware.Trace(),                                         // W3C traceparent in, ids in ctx — no OTel dependency
    middleware.SecureHeaders(middleware.SecureHeadersConfig{}), // nosniff, no-referrer, locked-down CSP on every response
    middleware.Logger(middleware.LoggerConfig{Log: log.Slog()}),
    middleware.Recover(middleware.RecoverConfig{Log: log.Slog()}),
    middleware.BodyLimit(middleware.BodyLimitConfig{}), // 413 past 1 MiB, before routing
)
```

SecureHeaders goes outside Logger and Recover so recovered 500s carry its headers too; the gates — CORS, RateLimit, BodyLimit, Idempotency, in that order — go innermost, after Recover. Your own middleware plugs into the same slots — the chain type is the ecosystem's `func(http.Handler) http.Handler`, so anything written for that convention drops in unchanged. Per-middleware behavior and gotchas: [middleware/README.md](middleware/).

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

Read it as a price list. Measured on a MacBook Pro — Apple M2 Pro (10 cores), 16 GB RAM, macOS 27.0, go1.26.8.

```bash
cd httpx && go test -run='^$' -bench=. -benchmem ./...
```

<details>
<summary>Raw output</summary>

```text
goos: darwin
goarch: arm64
pkg: github.com/Wigata-Intech/w-tools/httpx
cpu: Apple M2 Pro
BenchmarkServeMuxBaseline-10    	11720256	       103.5 ns/op	      18 B/op	       2 allocs/op
BenchmarkGroupRoute-10          	11480323	       104.6 ns/op	      18 B/op	       2 allocs/op
PASS
ok  	github.com/Wigata-Intech/w-tools/httpx	4.004s
PASS
ok  	github.com/Wigata-Intech/w-tools/httpx/client	0.348s
goos: darwin
goarch: arm64
pkg: github.com/Wigata-Intech/w-tools/httpx/middleware
cpu: Apple M2 Pro
BenchmarkBareHandler-10               	 7553701	       146.4 ns/op	     512 B/op	       3 allocs/op
BenchmarkLogger-10                    	 1000000	      1076 ns/op	     673 B/op	       8 allocs/op
BenchmarkLoggerCapture-10             	  484046	      2445 ns/op	    2292 B/op	      40 allocs/op
BenchmarkCanonicalChain-10            	  444656	      2696 ns/op	    2291 B/op	      35 allocs/op
BenchmarkCanonicalChainParallel-10    	  392004	      3030 ns/op	    2297 B/op	      35 allocs/op
BenchmarkRateLimitParallel-10         	 3034630	       420.2 ns/op	     512 B/op	       3 allocs/op
BenchmarkIdempotencyFirst-10          	  333657	      3176 ns/op	    7306 B/op	      38 allocs/op
BenchmarkIdempotencyReplay-10         	  344046	      3650 ns/op	    7391 B/op	      35 allocs/op
PASS
ok  	github.com/Wigata-Intech/w-tools/httpx/middleware	11.100s
```

</details>

| Situation | ns/op | allocs/op | Meaning for you |
| --------- | ----- | --------- | --------------- |
| Raw `ServeMux` routing | ~104 | 2 | The stdlib baseline |
| The same route through nested groups | ~105 | 2 | **Grouping is free** — parity within noise, identical allocations, because groups are registration-time sugar |
| Request floor (build + bare handler) | ~146 | 3 | What the middleware numbers subtract |
| `Logger` middleware, capture off | ~1,076 | 8 | ~1µs per request — almost all of it the JSON access line itself |
| `Logger` with request-body capture | ~2,445 | 40 | The opt-in costs ~1.4µs more: capture, parse, structured attr |
| Full canonical chain (RealIP → RequestID → Trace → Logger → Recover) | ~2,696 | 35 | Your whole production identity stack: ~3µs of overhead per request |

The practical takeaway: the expensive thing in the stack is writing a log line, not the middleware machinery around it — and even the everything-on chain costs less than 0.3% of a 1ms handler.

Opting into `Config.ErrorWriter` costs one extra ServeMux lookup per request: the server asks the mux whether a pattern matches before serving, so routing runs twice. Left nil, the server serves the mux directly and pays nothing.

Under concurrency the chain stays in one band (~2.5–3.2µs/op from 1 to 8 parallel callers — throughput scales with cores) and `RateLimit`'s single mutex stays sub-microsecond at 8 concurrent clients (~440 ns/op). Parallel variants of these benchmarks ship in the suite; run them with `-cpu 1,4,8`.

`Idempotency` adds ~2.7µs for the winning request (claim, capture, store) and serves a duplicate's replay in ~2.6µs without touching the handler — both invisible next to any real handler. Measured on the same machine, go1.26.8:

```text
$ cd middleware && go test -run='^$' -bench=Idempotency -benchmem .
BenchmarkIdempotencyFirst-10     	  450759	      2659 ns/op	    7304 B/op	      38 allocs/op
BenchmarkIdempotencyReplay-10    	  449562	      2628 ns/op	    7391 B/op	      35 allocs/op
```

The wire-input parsers (RealIP's forwarding headers, the W3C traceparent), the strict request-ID validator, and the problem encoder are fuzzed:

<details>
<summary>Fuzzing — commands and raw output</summary>

```text
$ go test -run='^$' -fuzz=FuzzRealIP -fuzztime=10s .
$ go test -run='^$' -fuzz=FuzzTraceparent -fuzztime=10s .
$ go test -run='^$' -fuzz=FuzzStrictRequestID -fuzztime=10s .
$ cd .. && go test -run='^$' -fuzz=FuzzProblemRespond -fuzztime=10s .
```

</details>

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
