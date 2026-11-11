# Changelog

All notable changes to `httpx` are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is semver, tagged `httpx/vX.Y.Z`.

## [Unreleased]

### Added

- `middleware.IdempotencyConfig.ValidKey` — decides whether a key is accepted; a rejected key is answered 400 before the body is read or the store is touched. `DefaultIdempotencyMaxKey` (255)

### Changed

- `middleware.Idempotency` refuses keys longer than `DefaultIdempotencyMaxKey` bytes or with a byte outside visible ASCII (0x21–0x7E) with 400 when `ValidKey` is nil. Before, any key up to the server's header limit was stored, so a client could fill the store's records with keys of many kilobytes each
- `middleware.Idempotency` stores and replays only the headers the handler set: a header outer middleware set before it (a request ID, security headers) is no longer replayed, so a duplicate keeps its own `X-Request-ID`

## [0.2.4] - 2026-11-11

### Added

- `Problem.Header` — response headers `Respond` sets before writing (`WWW-Authenticate` on a 401, `Retry-After` on a 503), so a registered or self-describing problem carries them; `Content-Type` and `Content-Length` stay `Respond`'s own, and `json.Marshal` omits the field
- `ErrorMap.Unmapped` — receives each error `RespondRequest` answers with the bare 500, so its cause can be logged while the response still never carries it; `Respond` never calls it

## [0.2.3] - 2026-11-11

### Changed

- Go floor raised from 1.26.8 to 1.27.4: govulncheck finds GO-2026-6859 (an HTTP/1 server crash from a `MaxBytesReader` header race in `net/http`) in every Go 1.27 release before 1.27.4

### Added

- `middleware.IdempotencyConfig.Scope` — namespaces each key by the request's tenant (`"<Prefix><Scope(r)>:<key>"`), so two tenants sending the same key never share a record. A covered request whose scope is empty is refused with 500 before the handler runs

## [0.2.2] - 2026-11-11

### Added

- `middleware.RecoverConfig.OmitPath` — leaves the request path out of the panic record, for routes whose path carries a secret (a payment-link token). The record still names the route
- `middleware.Recover` logs the matched route pattern (`pattern`) when the router has set it on the request Recover holds, as `Logger` does

## [0.2.1] - 2026-09-28

### Added

- `Problem.Extensions` — RFC 9457 §3.2 extension members, written by `Respond` at the top level of the object after the standard members and sorted by key; an extension named like a standard member, compared case-insensitively, is ignored. Output without extensions is byte-identical to before. An extension value that cannot be marshaled makes `Respond` write the problem without its extensions
- `ErrorMap.RespondRequest` and the `ErrorMap.Enrich` hook — one place to add request-scoped members (request ID, trace ID) to every problem the map writes: Problemer, registry match, and the bare 500. `Enrich` gets a private, non-nil `Extensions` map, so registered problems are never mutated; `Respond` is unchanged and never calls it
- `Config.ErrorWriter` — opt-in: requests no route matches answer through it, 404 for an unknown path and 405 for a wrong method with ServeMux's `Allow` header kept, still wrapped by `Use` middleware. Matched routes, including a handler-written 404, are untouched; nil keeps ServeMux's plain-text responses. Costs one extra ServeMux lookup per request when set
- `FuzzProblemRespond`, wired into `make fuzz`
- `middleware.RequestIDConfig.Valid` — screens non-empty inbound request IDs; a rejected ID is treated as absent (a fresh 32-hex ID is minted, the inbound value is never echoed or stored, and the request header `next` sees carries the minted ID instead — the caller's request is not modified). Nil keeps the existing rule (at most 128 bytes). `middleware.StrictRequestID` is the ready-made strict rule: 1–64 characters of `[A-Za-z0-9._-]`, fuzzed by `FuzzStrictRequestID`
- `middleware.SecureHeaders` — sets `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'` before calling next, so routing 404/405s, gate short-circuits and 500s from an inner `Recover` carry them. HSTS is opt-in (`HSTSMaxAge`, with `HSTSIncludeSubDomains` and `HSTSPreload`); each header is overridable, and `OmitHeader` suppresses one. Defaults exported as `DefaultContentSecurityPolicy` and `DefaultReferrerPolicy`. Canonical order extended: `… Trace → SecureHeaders → Logger …`
- `middleware.BodyLimit` — a declared `Content-Length` over `Max` is answered 413 (`request body too large`, via `ErrorWriter`, RFC 9457 by default) before next runs, so as outer middleware it wins over routing 404/405; every other body is wrapped in `http.MaxBytesReader`, capping chunked and undeclared bodies too. `Max <= 0` means `httpx.DefaultMaxBind`. Canonical order extended: `… RateLimit → BodyLimit → Idempotency`

### Changed

- `Problem` carries a map field (`Extensions`), so `Problem` values are no longer comparable with `==` or usable as map keys. Encoding is unchanged: `Problem` has no `MarshalJSON`, and a struct embedding it encodes its own fields as before
- `middleware.RequestID`: an inbound value that fails validation — including one over 128 bytes under a nil `Valid` — is also replaced in the request header `next` sees; the response and the context are unchanged

### Fixed

- `Problem.Respond` removes a `Content-Length` header set by an earlier writer, which previously contradicted the problem body

## [0.2.0] - 2026-09-28

### Changed

- Minimum Go version raised to 1.26.8 (`go.mod` directive); Go 1.25 and earlier are out of upstream support

## [0.1.2] - 2026-08-18

### Added

- `middleware.Idempotency` — at-most-once handler execution per `Idempotency-Key` within a TTL: stored-response replay marked `Idempotency-Replayed: true` or configurable reject, payload-mismatch 422, in-flight duplicate 409, key `Prefix` namespacing, capped request-body fingerprint read (`MaxRequestBodyBytes`, 413 past it). Storage is a plain key-value `Store` interface whose four methods map 1:1 to Redis commands (`SetNX`/`Get`/`Set`/`Delete` = `SET NX PX`/`GET`/`SET XX KEEPTTL`/`DEL`) — the bounded `MemoryStore` ships in-package, a Redis implementation makes it distributed with no middleware change. Only observed completions are stored — 5xx, panics, and oversized bodies release the claim so retries re-execute; store errors fail closed, and nothing live is ever evicted
- `middleware.RealIPFrom` — reads the client IP `RealIP` concluded from the request context, for layers holding only a `ctx` (log enrichment being the driving consumer). `RealIP` now stores its conclusion in the context on every parseable request, including with no trusted proxies configured

## [0.1.1] - 2026-08-14

### Changed

- README: benchmark and fuzz sections carry device specs, exact commands, and raw output logs above the summary tables

- README: pkg.go.dev reference badge under the pitch, the `go get` install command opening the TL;DR, and the promises header no longer claims v0 is unreleased
- middleware/ and client/ deep-dive READMEs on the sub-package template, summarized from the httpx README's "Using" subsections

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
