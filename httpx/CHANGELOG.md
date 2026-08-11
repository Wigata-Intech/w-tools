# Changelog

All notable changes to `httpx` are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is semver, tagged `httpx/vX.Y.Z`.

## [Unreleased]

### Added

- `Server` over `http.Server`: production timeout defaults, `Run(ctx)` with graceful shutdown, `ServeHTTP` for httptest/mounting, `HTTPServer()` escape hatch
- Route `Group`s over `ServeMux`: nested prefixes, per-group middleware chains, typed helpers for every method including `QUERY` (RFC 10008), `Handle`/`HandleFunc` mirroring `ServeMux` signatures
- `JSON` respond helper and RFC 9457 `Problem`/`Error` responses, with `ErrorWriter` for services that carry their own error format
- `Bind`: size-capped JSON body decoding (default 1 MiB, `MaxBody` override), strict content-type and trailing-data checks; QUERY requests without a Content-Type are rejected per RFC 10008's server requirement
