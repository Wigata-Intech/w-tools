# middleware

> Identity, safety, and gates for every request — httpx's production middleware set in one canonical order.

**Status:** ships with the `httpx` module. Module overview: the [httpx README](../README.md).

## TL;DR

```bash
go get github.com/Wigata-Intech/w-tools/httpx
```

- **RealIP** — trusts client-IP headers only from your `TrustedProxies` CIDRs (`PrivateNetworks()` covers internal hops); an unparseable header entry abandons the whole header — spoof-resistant. The concluded IP is also stored in the request context: `RealIPFrom(ctx)` reads it anywhere below (log enrichment, audit trails)
- **RequestID** / **Trace** — reuse-or-mint `X-Request-ID`; W3C `traceparent` in/out with fresh span ids, no OpenTelemetry dependency; ids via `RequestIDFrom`/`TraceIDFrom`/`SpanIDFrom`
- **Logger** — one JSON access line per request; opt-in body capture (capped, JSON logged structurally, non-JSON as size only) that inherits your logger's redaction
- **Recover** — panics become RFC 9457 500s with stack logging
- **CORS** — Fetch-spec preflights, `Vary: Origin` always (cache-poison-proof), wildcard+credentials panics at boot
- **RateLimit** — per-key token bucket with bounded memory and `Retry-After`; bring your own store via the `Limiter` interface
- **Idempotency** — at-most-once handler execution per `Idempotency-Key` (POST by default): atomic claim, stored-response replay or configurable reject, payload-mismatch 422, in-flight duplicate 409; 5xx and panics release the claim so retries re-execute. Bring your own store via the `Store` interface — four key-value methods, each mapping 1:1 to a Redis command (`SetNX` = `SET NX PX`, `Get` = `GET`, `Set` = `SET XX KEEPTTL`, `Delete` = `DEL`); the bounded `MemoryStore` ships in-package

## How to use with httpx

```go
s := httpx.New(httpx.Config{Addr: ":8080"})
s.Use(
    middleware.RealIP(middleware.RealIPConfig{TrustedProxies: middleware.PrivateNetworks()}),
    middleware.RequestID(middleware.RequestIDConfig{}),
    middleware.Trace(),
    middleware.Logger(middleware.LoggerConfig{Log: log.Slog(), LogRequestBody: true}),
    middleware.Recover(middleware.RecoverConfig{Log: log.Slog()}),
)
```

The order is the contract: RealIP outermost so everything downstream sees the real client; Recover inside Logger so a panic is logged as the 500 it became, with its latency; gates (CORS, RateLimit) innermost so their short-circuits are logged and run under Recover. In this order CORS preflights are deliberately unmetered — place RateLimit before CORS to meter them too.

## How to use standalone

Every middleware is a plain `func(http.Handler) http.Handler` — they wrap anything `net/http` serves, no httpx server required:

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /orders/{id}", getOrder)

var h http.Handler = mux
h = middleware.Recover(middleware.RecoverConfig{})(h)
h = middleware.Logger(middleware.LoggerConfig{})(h)
h = middleware.RealIP(middleware.RealIPConfig{TrustedProxies: proxies})(h)

_ = http.ListenAndServe(":8080", h) // wrap outermost last
```
