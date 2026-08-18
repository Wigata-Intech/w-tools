// Package logger wraps log/slog with JSON-first output, service metadata on
// every line, key-based redaction that runs before serialization, and
// optional per-record context enrichment via Config.ContextAttrs.
//
// Configure keys once and they are redacted or masked wherever they appear —
// top level, nested in groups, or inside structs passed as plain values.
// When a value cannot be processed it logs as "[UNLOGGABLE]" rather than
// shipping raw: when in doubt, this package hides.
package logger
