# Changelog

All notable changes to `logger`. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are per-module tags (`logger/vX.Y.Z`), semver, v0 until proven in production.

## [Unreleased]

### Added

- Initial implementation: `log/slog` wrapper with JSON-first output, service base fields (`env`, `version`, `app`, `protocol`), and levels `Debug`/`Info`/`Warn`/`Error`/`Panic` (rendered `PANIC`; logs before panicking)
- Key-based redaction (`[REDACTED]`, configurable wording) and rune-safe masking (`ShowFirst`/`ShowLast`/`MaskChar`), matching bare keys case-insensitively at any depth — including struct fields, maps, slices, groups, and resolved LogValuers
- Fail-closed behavior: unprocessable values log as `[UNLOGGABLE]`; panicking or endless LogValuers never leak stack traces into output
- `Wrap(handler, RedactConfig)` to layer redaction over any existing `slog.Handler`
- `context.Context` as the first parameter of every log method, reserved for future enrichment
- Typed `Protocol` with common constants; `Level slog.Level` with `ParseLevel` for string wiring
