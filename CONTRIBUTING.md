# Contributing to w-tools

Thank you for considering it. This project is open to contributions on purpose — and it protects its shape on purpose too. Reading this first will save you a rejected PR.

## Before you write code

- **Bug fixes:** open a PR directly. A failing test that demonstrates the bug is the best possible opening move.
- **Features or API changes:** open an issue first and wait for a maintainer's go-ahead. The API surface here is deliberately small; a feature that's useful but grows the surface will likely be declined, and we'd rather tell you before you've built it.
- **New packages:** maintainer-initiated only. Propose the idea in an issue; if accepted it starts life under `x/`.

## House rules

These are non-negotiable and enforced in review:

1. **Standard library first, zero dependencies.** No `go.mod` in this repo lists a third-party dependency. If you believe one is justified, make that case in an issue — adding one is a maintainer decision, never a side effect of a PR.
2. **CGO-free.** Everything cross-compiles with `CGO_ENABLED=0`.
3. **Simplicity first.** Minimum code that solves the problem. No speculative abstraction, no configurability nobody asked for, no interfaces with one implementation.
4. **Surgical changes.** Touch only what your change requires. Don't reformat, rename, or "improve" adjacent code — a PR that mixes its purpose with drive-by cleanup will be asked to split.

## Test standards

Tests are law here, not decoration:

- **Blackbox packages:** tests live in `package <name>_test` — they exercise only what consumers can reach.
- **One source file, one test file:** `mask.go` → `mask_test.go`. One function under test, one test function.
- **Table-driven**, using the house case struct:

```go
tests := []struct {
    name     string
    mockFunc func(t *testing.T) // nil unless the case needs one
    input    inputType
    expected expectedType
}{ /* ... */ }
```

- **Cases mirror the code:** ordered top-to-bottom as the code reads, positive cases before negative. New logic in the middle of a function means new cases in the middle of the table.
- **Race coverage:** anything shared gets concurrent cases; CI runs `-race` always.
- **Errors are asserted, not appeased.** Assert the specific error. A test loosened to make a run pass will fail review harder than the bug it hides.

## Before you push

One command runs the whole gate — CI runs the identical list:

```bash
make check
```

Which executes, in this order (cheap static checks first, network last, so failures cost you seconds not minutes):

| Step | Command | Catches |
| ---- | ------- | ------- |
| 1 | `gofmt -l .` | Formatting — must print nothing |
| 2 | `go vet ./...` | Suspicious constructs, printf/slog arg mistakes |
| 3 | `golangci-lint run` | Lint suite including **gosec** (enabled in `.golangci.yml` — no separate gosec run needed) |
| 4 | `GOWORK=off CGO_ENABLED=0 go build ./...` | Each module compiles standalone, CGO-free |
| 5 | `go test -race ./...` | Behavior, and data races |
| 6 | `govulncheck ./...` | Known vulnerabilities reachable from the code (stdlib included) |

Tools you'll need once: [`golangci-lint`](https://golangci-lint.run/welcome/install/) and `go install golang.org/x/vuln/cmd/govulncheck@latest`. These are dev tools — they never appear in any `go.mod`.

## Documentation

Godoc is the documentation — what you write in comments is what [pkg.go.dev](https://pkg.go.dev) renders:

- Every exported identifier gets a doc comment, starting with its name: `// Wrap layers redaction over an existing slog.Handler.`
- Package documentation lives in a `doc.go` with a `// Package logger ...` comment telling the story, not repeating the README.
- Runnable `Example` functions (`func ExampleNew()` in a `_test.go` file) render as executable examples on pkg.go.dev *and* compile in CI — examples that can't rot.
- Deprecations use the magic comment: `// Deprecated: use Wrap instead.` — tooling and pkg.go.dev both honor it.

### Package README template

Every module's `README.md` follows the same human-first shape — a reader should learn what it is, what pain it removes, and how to use it, in that order:

```markdown
# <package>
> One-line pitch — the whole value in one sentence.

**Status:** where the package is on the v0 → v1 road, and what semver means right now.

## TL;DR                      3–5 bullets: what you get, no prose
## What problem this solves   human prose — the pain first, then how the package removes it
## How it solves it           minimal working code, plus a pointer to examples/
## Why it matters             what this buys the reader: standards, guarantees, no lock-in
## What it costs              the honest price — benchmark table when measured, structural notes before that
## The promises               the guarantees, as of the current version
```

No per-package license section — the repository [LICENSE](LICENSE) covers every module, and pkg.go.dev detects and displays it on its own.

## Commits and the DCO

Every commit must be signed off:

```bash
git commit -s -m "logger: mask by rune, not byte"
```

The `Signed-off-by` line is your [Developer Certificate of Origin](https://developercertificate.org/) — your statement that you have the right to submit this code under Apache-2.0. Unsigned commits can't be merged. Subject lines: `<package>: <imperative summary>`.

## AI-assisted contributions

Welcome, with accountability — see [AI_POLICY.md](AI_POLICY.md). The one-line version: you own every line you submit, whoever or whatever typed it first.

## Conduct

Everyone interacting here is covered by the [Code of Conduct](CODE_OF_CONDUCT.md).

## Releases

Maintainers tag releases (`logger/v0.3.0` — per-module tags). Contributors never need to touch versioning.
