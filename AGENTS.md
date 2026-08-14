# AGENTS.md

Instructions for AI coding agents working in this repository. Human policy on AI contributions is in [AI_POLICY.md](AI_POLICY.md); this file is the machine-facing counterpart.

## What this repo is

`w-tools` — Wigata InTech's open-source Go monorepo. Multi-module: each package has its own `go.mod` and is tagged independently, format `<module>/vX.Y.Z` (e.g. `logger/v0.1.0`, the first release). Stable packages live at the root; experimental ones under `x/` may break or be deleted outright. The committed `go.work` wires local development.

## Layout

```text
w-tools/
├── go.work          # committed on purpose — do not gitignore it
├── cli/             # command framework: tree, flag>env>config precedence, help; migrations under cli/migrationx
├── logger/          # slog wrapper with key-based redaction (see logger/README.md)
├── httpx/           # net/http wrapper: server, groups, JSON + RFC 9457 (see httpx/README.md)
│   ├── middleware/  # RealIP, RequestID, Trace, Recover, Logger, CORS, RateLimit — same module
│   ├── client/      # outbound: pooled, timed, breaker hook, traced, logged — same module
│   └── examples/    # own module, workspace-only — the one place siblings assemble
└── x/
    ├── circuitbreaker/  # experimental: three-state breaker (own module, deletable)
    └── sshx/            # experimental: persistent SSH connection management (own module; the one allowlisted dependency)
        └── keys/        # same module: key parse/load/generate
```

## Hard rules

1. **Zero third-party dependencies.** Never add a `require` to any `go.mod`. Single exception: a module under `x/` implementing a protocol the standard library doesn't cover may require an **allowlisted `golang.org/x` module** — one maintainer approval per (module, dependency) pair, granted before the `require` is written. Current allowlist: `x/sshx` → `golang.org/x/crypto` (its own `// indirect` graph entries, recorded by `go mod tidy`, ride along). If a task seems to need anything else, stop and report why instead of adding it.
2. **Modules never require each other.** Root modules stay independent — no cross-module imports inside the family. The `examples/` module is the one place siblings assemble, and it is never tagged.
3. **CGO-free.** Nothing that breaks `CGO_ENABLED=0` cross-compilation.
4. **The module's `go.mod` directive is the API floor** — don't call stdlib APIs newer than it. Floors carry a patch version (e.g. `go 1.23.12`) so `govulncheck` analyzes against a patched stdlib; raising a floor is a maintainer decision.
5. **Simplicity first.** Minimum code that solves the problem. No speculative abstraction, no single-implementation interfaces, no unrequested configurability.
6. **Surgical changes.** Touch only what the task requires. Match existing style. Never reformat or "improve" adjacent code.
7. **Never run git commands that mutate state** (add, commit, push, tag, rebase). Reading state (`git status`, `git diff`, `git log`) is fine. The human maintainer performs all version control.

## Documentation standards

- **Godoc:** every exported identifier gets a comment starting with its name. Package docs live in `doc.go`. Deprecations use `// Deprecated: ...`.
- **Comments say what, not why:** a comment states a constraint, contract, or invariant the code cannot express — never design rationale or a narration of the change that added it. Rationale belongs in the PR/report, not the source.
- **READMEs follow the template in [CONTRIBUTING.md](CONTRIBUTING.md) from the first commit** — the 5W+1H sections (`TL;DR → problem → how → why → cost → promises`), no per-package license section, unreleased status stated in the Status line. Placeholder or "starter" READMEs are never acceptable.
- **README hierarchy:** the root README references modules; a module's README carries the high- and mid-level story, with a `### Using <subpackage>` subsection under "How it solves it" for each substantial subpackage (short, usage-focused, linking down). Substantial subpackages carry their own README on the sub-template: `# <name>` → `## TL;DR` (go get first) → `## How to use with <module>` → `## How to use standalone`.
- **Examples are generic.** Code, comments, and docs use neutral names (`my-service`, `app`) — never internal product names.
- **Packages are described on their own terms.** Never frame a package as a copy of another project — no "X-style", "X-like", or "the X subset" in pitches, godoc, or roadmap rows. Named comparisons live only in explicit alternatives discussions ("why not X") and measured tables.
- **Runnable demonstrations live in the `examples/` module.** In-code `Example` functions are added only when their output is assertable — the `testableexamples` linter rejects output-less ones.
- **Never cite a version, tag, count, or filename without verifying it exists** — check against `git tag` and the tree, not memory.

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
- 100% statement coverage is the bar; a deliberate gap needs a stated reason.

## Verification gate

Run the full gate before reporting any task complete, and report its real output:

```bash
make check   # fmt -> vet -> golangci-lint (incl. gosec) -> build (GOWORK=off, CGO off) -> test -race -> examples proofs -> govulncheck
```

Never claim a check you didn't run. If a tool is missing locally, say so — don't skip silently.
