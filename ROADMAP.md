# Roadmap

Where each package is and where it's going. ✅ delivered · 🚧 agreed and in progress · 💡 candidate, not committed. History of what actually shipped lives in each module's `CHANGELOG.md`.

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
| 💡 | Comparative benchmarks vs zap, zerolog, logrus — README material, after the API freezes |
| 💡 | Automatic enrichment from `ctx` — trace id and friends, key naming to be decided |
| 💡 | Call-site / stack trace support (`AddSource`, stack attr on `Error`/`Panic`) — shaped by real WiPays usage |

## Future packages

| Status | Item |
| ------ | ---- |
| 💡 | `httpx`, config helpers, `x/sshx` — each starts with its own RFC or design doc |
