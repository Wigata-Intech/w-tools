# Changelog

All notable changes to `httpx` are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is semver, tagged `httpx/vX.Y.Z`.

## [Unreleased]

### Added

- README recipe: `net/http/pprof` on a second, internal-only httpx server — mount pattern, the WriteTimeout-vs-profile-stream gotcha, and the never-expose-publicly warning



## [0.1.0] - 2026-08-12

### Added

- `middleware` package: `RealIP` (CIDR-trusted proxy resolution, spoof-safe by default, `PrivateNetworks()` convenience set), `RequestID` (reuse-or-mint, customizable header), `Trace` (W3C traceparent wire format, no OTel dependency, flags preserved via `TraceFlagsFrom`), `Recover` (panic → logged 500, `http.ErrAbortHandler` honored), `Logger` (one access line per request; buffer-pooled, opt-in JSON body capture as structured attrs — non-JSON bodies never log raw). Canonical order documented: `RealIP → RequestID → Trace → Logger → Recover`
- Fuzzers for the two attacker-facing parsers: `FuzzRealIP` and `FuzzTraceparent`, wired into `make fuzz`
- Gate middleware: `CORS` (Fetch-spec preflights, wildcard+credentials rejected at construction, `QUERY` in the default method list) and `RateLimit` (per-key token bucket with bounded memory and idle eviction, pluggable `Limiter` interface for other algorithms or distributed backends, `Retry-After` on 429). Canonical order extended: `… Recover → CORS → RateLimit`
- BFF rendering: the structural `Renderer` interface (templ-compatible, engine-free), `Render` streaming with the request context, and the `Template` adapter for html/template
- `ErrorMap`: register a service's domain-error → `Problem` taxonomy once; handlers respond with one line. Checks the error's own `Problemer` first, then the registry via `errors.Is`; unmapped errors respond as a bare 500 that never leaks `err.Error()`
- `examples/` module (workspace-only, never tagged): a REST service with `ErrorMap` and QUERY search, a BFF page through `Template`, and the redaction proof — httpx's Logger middleware feeding a captured request body through w-tools/logger's rules, `[REDACTED]` asserted in CI on every run
- `client` package: outbound `http.Client` wrapper — production transport tuning (pooling at 100 idle conns/host vs stdlib's 2, TLS session resumption, mandatory timeout with no "no timeout" setting), an overridable `TLS *tls.Config` for internal CAs and mTLS, ctx-first verbs incl. `Query` (RFC 10008), the `Breaker` circuit-breaker hook (`ErrCircuitOpen` before the network when open), W3C traceparent propagation from the request context with a fresh span id (a caller-set header is never overwritten), and opt-in outbound logging: query strings logged as parsed maps and JSON bodies as structured attrs so the supplied logger's redaction applies to both; response capture never gates the caller
- `DefaultMaxBody` moved to the httpx root — it governs body capture in both the middleware Logger and the client, so it lives where both can reach it without cross-subpackage imports

- `Server` over `http.Server`: production timeout defaults, `Run(ctx)` with graceful shutdown, `ServeHTTP` for httptest/mounting, `HTTPServer()` escape hatch
- Route `Group`s over `ServeMux`: nested prefixes, per-group middleware chains, typed helpers for every method including `QUERY` (RFC 10008), `Handle`/`HandleFunc` mirroring `ServeMux` signatures
- `JSON` respond helper and RFC 9457 `Problem`/`Error` responses, with `ErrorWriter` for services that carry their own error format
- `Bind`: size-capped JSON body decoding (default 1 MiB, `MaxBody` override), strict content-type and trailing-data checks; QUERY requests without a Content-Type are rejected per RFC 10008's server requirement
