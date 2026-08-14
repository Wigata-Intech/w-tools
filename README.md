# w-tools

> Small Go tools, forged in production, free of dependencies — and shared.

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

`w-tools` is [Wigata InTech](https://wigataintech.com)'s open-source Go toolbox: focused packages we run in our own production services first, and publish because they're useful beyond us. One discipline holds everything together — **standard library first, zero third-party dependencies, CGO-free**. The single carve-out: an `x/` package implementing a protocol the standard library doesn't cover may use an allowlisted, Go-team-maintained `golang.org/x` module (today: `x/crypto` for SSH) — never anything beyond that. If a package here can't justify itself without pulling in half the ecosystem, it doesn't ship.

## TL;DR

- **Multi-module monorepo** — every package has its own `go.mod`, its own tags, its own dependency graph (empty for every root package, and staying that way)
- **Stable at the root, experimental under `x/`** — the `golang.org/x` convention: root packages hold their APIs; `x/` packages may break, stall, or be **dropped from the repo entirely**, without a deprecation cycle
- **Production-proven or it doesn't ship** — nothing lands here that Wigata InTech doesn't run itself
- **v0 until earned** — a package reaches `v1.0.0` only after surviving real production use

## Packages

| Package | What it does | Status | Docs |
| ------- | ------------ | ------ | ---- |
| [`cli`](cli/) | Command framework: command tree, flag > env > config > default precedence, struct binding, generated help; SQL migrations via `cli/migrationx`: checksummed, locked, transactional | v0.1.0 | [![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/w-tools/cli.svg)](https://pkg.go.dev/github.com/Wigata-Intech/w-tools/cli) |
| [`httpx`](httpx/) | `net/http` wrapper: server with production timeouts, route groups, all methods incl. RFC 10008 `QUERY`, RFC 9457 errors, middleware, outbound client | v0.1.1 | [![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/w-tools/httpx.svg)](https://pkg.go.dev/github.com/Wigata-Intech/w-tools/httpx) |
| [`logger`](logger/) | `log/slog` wrapper: JSON-first, service metadata on every line, compliance-grade key redaction and masking | v0.1.2 | [![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/w-tools/logger.svg)](https://pkg.go.dev/github.com/Wigata-Intech/w-tools/logger) |
| [`x/circuitbreaker`](x/circuitbreaker/) | Three-state circuit breaker: sliding-window trip, half-open probes, native `net/http` and `httpx/client` integration | **experimental** v0.1.1 | [![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/w-tools/x/circuitbreaker.svg)](https://pkg.go.dev/github.com/Wigata-Intech/w-tools/x/circuitbreaker) |
| [`x/sshx`](x/sshx/) | Persistent SSH connection management: self-healing per-host connections, jittered backoff, capped dial concurrency, fail-closed host keys, stream-based sessions, key handling | **experimental** v0.1.0 | [![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/w-tools/x/sshx.svg)](https://pkg.go.dev/github.com/Wigata-Intech/w-tools/x/sshx) |

Treat anything under `x/` as a sharp tool with no handle: an `x/` package is an experiment, not a promise — it can fail its experiment and be deleted outright, so never build anything load-bearing on one. Graduation to the root, under a new import path, is the only way an `x/` package earns stability.

What's shipped and what's coming: [ROADMAP.md](ROADMAP.md), plus a `CHANGELOG.md` per module.

## Install

Each package is its own module — pull only what you need:

```bash
go get github.com/Wigata-Intech/w-tools/cli@latest
go get github.com/Wigata-Intech/w-tools/logger@latest
go get github.com/Wigata-Intech/w-tools/httpx@latest
go get github.com/Wigata-Intech/w-tools/x/circuitbreaker@latest   # experimental — see the x/ contract above
```

Importing any of them brings in that module and nothing else beyond its own stated `go.mod` — no transitive surprises; for every root package that `go.mod` is empty.

## Versioning

Tags are per-module — `<module>/vX.Y.Z`, never a repo-wide version. Semver applies per package:

- **v0.x** — the API may still move; we run it in production, but hold your upgrade pins
- **v1+** — the API is a promise; breaking it costs us a `/v2` module path, so we won't
- **`x/` packages** — v0 forever until they graduate to the root under a new import path; an experiment that doesn't pass gets deleted, not maintained

## Building and testing

The committed `go.work` wires the workspace — clone and build, no `replace` directives. The whole verification gate is one command:

```bash
make check   # gofmt -> vet -> golangci-lint -> build (standalone, CGO off) -> test -race -> govulncheck
```

CI runs the identical gate per module. See [CONTRIBUTING.md](CONTRIBUTING.md) for the breakdown.

## Contributing

Contributions are welcome — this is open source on purpose, not by accident. Start with:

- [CONTRIBUTING.md](CONTRIBUTING.md) — house rules, test standards, and the DCO sign-off
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — Contributor Covenant
- [AI_POLICY.md](AI_POLICY.md) — AI-assisted contributions are fine; unaccountable ones are not
- [SECURITY.md](SECURITY.md) — how to report a vulnerability privately

The short version: standard library only, tests are law, sign your commits with `-s`, and every line you submit is yours to explain.

## The name

*Wigata* is an Indonesian word carrying two inheritances: in the KBBI it means common, ordinary — humility; in its Sanskrit root, *to step forward*. That's the intended spirit of this toolbox: ordinary tools, no grand framework ambitions, each one a small step forward. The `w` carries the name.

## License

[Apache-2.0](LICENSE). Use it, modify it, sell with it — keep the attribution. The Wigata InTech name and marks are not licensed; the code is.
