# Changelog

All notable changes to `logger`. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are per-module tags (`logger/vX.Y.Z`), semver, v0 until proven in production.

## [Unreleased]

## [0.1.1] - 2026-08-12

### Added

- Parallel benchmark variants (`BenchmarkPassThroughParallel`, `BenchmarkRedactStructParallel`) — concurrent-logger scaling published in the README

### Changed

- README restructured to the house 5W+1H template (TL;DR → problem → how → why → cost → promises); per-package license section dropped — the repository LICENSE covers every module
- Test tables drop the `mockFunc` field where no case used it, per the sharpened house standard

No functional change; no API change.

## [0.1.0] - 2026-08-11

### Added

- Initial implementation: `log/slog` wrapper with JSON-first output, service base fields (`env`, `version`, `app`, `protocol`), and levels `Debug`/`Info`/`Warn`/`Error`/`Panic` (rendered `PANIC`; logs before panicking)
- Key-based redaction (`[REDACTED]`, configurable wording) and rune-safe masking (`ShowFirst`/`ShowLast`/`MaskChar`), matching bare keys case-insensitively at any depth — including struct fields, maps, slices, groups, and resolved LogValuers
- Fail-closed behavior: unprocessable values log as `[UNLOGGABLE]`; panicking or endless LogValuers never leak stack traces into output
- `Wrap(handler, RedactConfig)` to layer redaction over any existing `slog.Handler`
- `context.Context` as the first parameter of every log method, reserved for future enrichment
- Typed `Protocol` with common constants; `Level slog.Level` with `ParseLevel` for string wiring
- Benchmark suite (`make bench`): raw-slog baseline, no-rules pass-through, copy-on-write walk, top-level and struct redaction — all with allocation reporting
- Fuzz tests (`make fuzz`): mask invariants (never panic, rune count preserved, never over-reveal) and full-pipeline redaction (valid JSON, redacted values never survive)

### Changed

- The hot path carries no `ReplaceAttr`: an internal split handler with concrete `*slog.JSONHandler` fields routes only PANIC records through the level-renaming handler. Result: no-rules logging is allocation-free at parity with raw `slog` (previously +45% and 3 allocs/op — a non-nil `ReplaceAttr` disables slog's fast paths for every record)

### Fixed

- Masking bounds overflow: `ShowFirst`/`ShowLast` values near `math.MaxInt` could wrap negative, slip past the full-mask guard, and panic — now overflow-safe and fail-closed
