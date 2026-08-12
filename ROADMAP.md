# Roadmap

Where each package is and where it's going. ✅ delivered · 🚧 agreed and in progress · 💡 candidate, not committed. History of what actually shipped lives in each module's `CHANGELOG.md`.

## httpx

Design approved 2026-08-11; landing in phases, each a reviewed PR.

| Status | Item |
| ------ | ---- |
| ✅ | Server with production timeouts + graceful shutdown; route groups over `ServeMux`; every method incl. RFC 10008 `QUERY` — merged via #5 |
| ✅ | JSON respond/bind with RFC 9457 `application/problem+json` errors, swappable via `ErrorWriter` — merged via #5 |
| ✅ | `ErrorMap` — register domain-error → `Problem` mappings once at startup; handlers respond with one line |
| ✅ | Middleware: `RealIP`, `RequestID`, `Trace` (W3C traceparent), `Recover`, `Logger` (buffer-pooled, opt-in body capture) — fuzzers on the two wire-input parsers |
| ✅ | Gate middleware: `CORS` (Fetch-spec preflights, boot-time misconfig panic) and `RateLimit` (bounded-memory token bucket, pluggable `Limiter`) |
| ✅ | BFF rendering: structural `Renderer` interface — templ native, `html/template` adapter — plus the examples module (REST, BFF, and the CI-asserted redaction proof). **On probation**: templ ships its own `templ.Handler`, so `Render`/`Renderer` must earn their keep in the first real WiPays BFF page or be deleted before `v1.0.0` — v0 semantics make removal cheap now, impossible later |
| ✅ | Outbound client: tuned pooling, mandatory timeout, circuit-breaker hook, traceparent propagation, and opt-in redaction-inheriting request/response logging |
| 🚧 | `x/circuitbreaker`: three-state breaker implementing the client's `Breaker` hook — experimental |
| 💡 | Idempotency-Key middleware (server) and idempotency-aware client retries — bring-your-own store |
| 💡 | Distributed rate limiting: a store-backed `Limiter` (Redis), likely under `x/` |
| 💡 | `x/websocket` — RFC 6455 implementation, its own design when its turn comes |

## logger

| Status | Item |
| ------ | ---- |
| ✅ | slog wrapper: JSON-first, base fields (`env`, `version`, `app`, `protocol`), levels incl. `PANIC` |
| ✅ | Key-based redaction and masking at any depth — structs, maps, groups, LogValuers — fail-closed |
| ✅ | `Wrap` for adopting only the redaction layer over an existing handler |
| ✅ | `ctx` parameter on every log method (reserved for enrichment) |
| ✅ | 100% test coverage, race-clean, all-linters-on, govulncheck-clean |
| ✅ | Internal benchmarks — no-rules path measured at parity with raw slog, zero allocations |
| ✅ | Fuzzing: mask invariants and full-pipeline redaction, 10M+ executions clean |
| 🚧 | First production adoption in a Wigata InTech service — gates `v1.0.0` |
| 🚧 | Automatic enrichment from `ctx` — request id, trace id, real IP on every line, configurable (the sibling-dependency law means logger takes extractor funcs, never middleware's ctx keys) |
| 💡 | Comparative benchmarks vs zap, zerolog, logrus — README material, after the API freezes |
| 💡 | Async/buffered slog handler — take log emission off the request path in one place instead of per-middleware goroutines |
| 💡 | Call-site / stack trace support (`AddSource`, stack attr on `Error`/`Panic`) — shaped by real WiPays usage |

## Future packages

| Status | Item |
| ------ | ---- |
| 💡 | Config helpers, `x/sshx`, `x/workerpool` (reusable bounded-worker primitive) — each starts with its own RFC or design doc |
