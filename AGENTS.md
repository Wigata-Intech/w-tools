# AGENTS.md

Instructions for AI coding agents working in this repository. Human policy on AI contributions is in [AI_POLICY.md](AI_POLICY.md); this file is the machine-facing counterpart.

## What this repo is

`w-tools` — Wigata InTech's open-source Go monorepo. Multi-module: each package has its own `go.mod` and is tagged independently, format `<module>/vX.Y.Z` (e.g. `logger/v0.1.0`, the first release). Stable packages live at the root; experimental ones under `x/`. The committed `go.work` wires local development.

## Hard rules

1. **Zero third-party dependencies.** Never add a `require` to any `go.mod`. If a task seems to need one, stop and report why instead of adding it.
2. **CGO-free.** Nothing that breaks `CGO_ENABLED=0` cross-compilation.
3. **The module's `go.mod` directive is the API floor** — don't call stdlib APIs newer than it. Floors carry a patch version (e.g. `go 1.23.12`) so `govulncheck` analyzes against a patched stdlib; raising a floor is a maintainer decision.
4. **Simplicity first.** Minimum code that solves the problem. No speculative abstraction, no single-implementation interfaces, no unrequested configurability.
5. **Surgical changes.** Touch only what the task requires. Match existing style. Never reformat or "improve" adjacent code.
6. **Never run git commands that mutate state** (add, commit, push, tag, rebase). Reading state (`git status`, `git diff`, `git log`) is fine. The human maintainer performs all version control.
7. **Every exported identifier gets a godoc comment** starting with its name. Package docs live in `doc.go`. Runnable demonstrations live in a package's `examples/` module; in-code `Example` functions are added only when their output is assertable (the `testableexamples` linter rejects output-less ones). Deprecations use `// Deprecated: ...`.

## Test standards (enforced in review)

- Tests in `package <name>_test` (blackbox). One source file → one test file. One function under test → one test function.
- Table-driven with this exact case struct shape:

```go
tests := []struct {
    name     string
    mockFunc func(t *testing.T) // include this field only when at least one case uses it; omit it otherwise
    input    inputType
    expected expectedType
}{ /* ... */ }
```

- Cases ordered to mirror the code top-to-bottom, positive before negative. Insert new cases where the new logic sits, don't append.
- A case whose shape differs from the table's (needs goroutines, recover, mid-case clock moves, bespoke writers) becomes a named `t.Run` subtest after the table — never a table case dragging one-off fields every other case must carry dead.
- Concurrent cases for anything shared. Assert specific errors — never weaken an assertion to make a test pass.

## Verification gate

Run the full gate before reporting any task complete, and report its real output:

```bash
make check   # fmt -> vet -> golangci-lint (incl. gosec) -> build (GOWORK=off, CGO off) -> test -race -> govulncheck
```

Never claim a check you didn't run. If a tool is missing locally, say so — don't skip silently.

## Layout

```text
w-tools/
├── go.work          # committed on purpose — do not gitignore it
├── logger/          # slog wrapper with key-based redaction (see logger/README.md)
├── httpx/           # net/http wrapper: server, groups, JSON + RFC 9457 (see httpx/README.md)
│   ├── middleware/  # RealIP, RequestID, Trace, Recover, Logger, CORS, RateLimit — same module
│   ├── client/      # outbound: pooled, timed, breaker hook, traced, logged — same module
│   └── examples/    # own module, workspace-only — the one place siblings assemble
└── x/
    └── circuitbreaker/  # experimental: three-state breaker (own module, deletable)
```
