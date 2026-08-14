# client

> The outbound half of httpx: a production-tuned HTTP client with a timeout you can't turn off, a circuit-breaker seam, and logging that inherits your redaction.

**Status:** ships with the `httpx` module. Module overview: the [httpx README](../README.md).

## TL;DR

```bash
go get github.com/Wigata-Intech/w-tools/httpx
```

- Pooling tuned for services: 100 idle connections per host (stdlib default: 2), TLS 1.2+ with session cache, `Config.TLS` for internal CAs/mTLS
- A mandatory timeout — a hung upstream can't hang your goroutine
- `Config.Breaker` seam: anything with `Allow() error` / `Record(err error)` plugs in; `x/circuitbreaker` satisfies it natively; open circuit → `ErrCircuitOpen` in nanoseconds
- Outbound `traceparent` with fresh span ids; a caller-set header is never overwritten
- Opt-in request/response logging where redaction follows your logger: query strings logged as parsed maps (`?api_key=` redacts), response capture `Content-Length`-gated so streaming/SSE is never buffered
- Ctx-first verbs including RFC 10008 `QUERY`

## How to use with httpx

Server and client share one logger, so redaction and trace ids flow through both directions of a request:

```go
log := logger.New(logger.Config{App: "my-service", Redact: logger.RedactConfig{Redacted: []string{"api_key"}}})
upstream := client.New(client.Config{Log: log.Slog(), Breaker: breaker})

// inside a handler: the outbound call carries the inbound trace
resp, err := upstream.Get(r.Context(), "https://api.upstream.example/orders")
```

## How to use standalone

Nothing here needs the httpx server — any Go program gets the same client:

```go
c := client.New(client.Config{}) // production transport + default timeout, zero config required
resp, err := c.Get(ctx, url)
```

Build **one client per upstream at boot** and reuse it everywhere — the pool is the point; per-request clients rebuild pools and defeat every number above.
