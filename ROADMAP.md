# Roadmap

Where each package is and where it's going. ✅ delivered · 🚧 agreed and in progress · 💡 candidate, not committed. History of what actually shipped lives in each module's `CHANGELOG.md`. Module sections are alphabetical; Future packages always sits last.

## cli

Shipped as `cli/v0.1.0`, 2026-08-14, `migrationx` included.

| Status | Item |
| ------ | ---- |
| ✅ | Stdlib-only command/flag/env framework for service entrypoints: command tree, precedence chain, struct binding, generated help — shipped as `cli/v0.1.0` |
| ✅ | `cli/migrationx` — SQL migrations on `database/sql` + `embed`: timestamp-versioned files with checksums and a migration lock; drivers stay consumer-side; shipped in `cli/v0.1.0` — engine, scanner, locks, migrate command |
| 💡 | Live config reload — *proposed, not approved*: would supersede the design's deliberate omissions; shape if accepted is SIGHUP-triggered re-resolution with an `OnReload` callback, never file watching |

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
| ✅ | `x/circuitbreaker`: three-state breaker implementing the client's `Breaker` hook — experimental, own module, CI-proven fail-fast via the examples' breaker demo |
| 💡 | Idempotency-Key middleware (server) and idempotency-aware client retries — bring-your-own store |
| 💡 | `Debug` convenience constructor — the internal pprof server the README recipe builds by hand, if the recipe proves too repetitive across services |
| 💡 | Distributed rate limiting: a store-backed `Limiter` (Redis), likely under `x/` |
| 💡 | Distributed circuit breaking — *uncertain on purpose*: the seam already exists (any Redis-backed implementation of `client.Breaker` plugs in today), and per-instance breaking is usually the correct semantic; if cluster coordination is ever proven necessary, the shape is local breakers sharing observations asynchronously and deciding locally — never a network hop inside `Allow` |
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

## x/sshx

Design approved 2026-08-14; shipped as `x/sshx/v0.1.0` 2026-08-15 (persistent SSH connection management; first consumer is `kay`). Carries the repo's one dependency exception: `golang.org/x/crypto`, allowlisted for `x/` modules by maintainer decision.

| Status | Item |
| ------ | ---- |
| ✅ | Core client: ctx-first `Dial` with staged typed errors, `Output`/`CombinedOutput` (output survives failure), deadline-guarded `Ping`, keepalive |
| ✅ | Fail-closed host-key policies: strict `KnownHosts`, `TOFU` with caller confirmation (single prompt under concurrent first contact), explicit `InsecureAcceptAny` |
| ✅ | `Pool`/`Managed`: self-healing per-host connections, jittered exponential backoff, pool-wide dial cap, non-blocking `ErrNotReady`, `OnStateChange` |
| ✅ | Stream-based sessions: `Shell` with optional PTY and live `Resize` — the library never touches the local terminal |
| ✅ | `keys` subpackage: parse / load with passphrase callback / generate (Ed25519 default, RSA ≥ 2048); fuzzers on the parse and comment round-trip invariants |
| ✅ | `IsAuthFailure` — the one audited auth classifier, so consumers never substring-match dial errors (fed back from the first adoption); shipped in `v0.1.1` |
| 💡 | ssh-agent auth (`SSH_AUTH_SOCK`) — additive once a real consumer asks |
| 💡 | Jump/bastion hosts — needs its own design pass |

## Future packages

Each starts with its own RFC or design doc; 💡 means candidate, not commitment. Rows alphabetical.

| Status | Item |
| ------ | ---- |
| 💡 | `dbx` — `database/sql` ergonomics (tx helpers, timeouts, scanning); never imports a driver, tested against sqlite/mysql via the examples module |
| 💡 | Go 1.27 compatibility pass — verify the gate under 1.27 (json/v2 becomes `encoding/json`'s engine); floor bumps stay demand-driven, since a floor excludes every consumer below it |
| 💡 | `hasher` (argon2) — the curated `golang.org/x` allowlist for `x/` modules now exists (maintainer-approved per pair; `x/sshx` → `x/crypto` was the first); an `x/hasher` needs its own approval and design |
| 💡 | `tomlx` / `yamlx` — config-format decoders plugging into `cli`'s decoder seam (TOML feasible; YAML only ever as a strict subset under `x/`) |
| 💡 | `x/token` — JWT on stdlib crypto only; security-sensitive, so `x/` and heavy hardening if attempted |
| 💡 | `x/workerpool` — reusable bounded-worker primitive |
