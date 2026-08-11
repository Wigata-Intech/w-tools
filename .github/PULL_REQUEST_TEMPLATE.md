## What it does

<!-- One or two sentences: what does this change do, and why? Link the issue for features. -->

## Evidence

<!-- Show it working: test output, `make check` output, log lines, benchmark diff, screenshot — whatever proves the claim. "Trust me" is not evidence. -->

```text

```

## AI usage

<!-- Per AI_POLICY.md. Delete this section only if no AI was involved at all. -->

- **Model(s):** <!-- e.g. Claude Fable 5, GPT-x, none -->
- **Harness/tool:** <!-- e.g. Claude Code, Cursor, Copilot, none -->
- **How it was used:** <!-- pick what applies: autocompletion only / discussion & design / code generation I then reviewed / agent-driven development -->

## Checklist

- [ ] `make check` passes locally (fmt, vet, lint, build, test, vuln)
- [ ] Tests follow the house standards (blackbox `_test` package, table-driven, cases mirror code order) — see [CONTRIBUTING.md](../CONTRIBUTING.md)
- [ ] Every commit is signed off (`git commit -s`) — the DCO
- [ ] No new dependencies in any `go.mod`
- [ ] Exported identifiers have doc comments
