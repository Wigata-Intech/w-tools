// Package logger wraps log/slog with JSON-first output, service metadata on
// every line, and key-based redaction that runs before serialization.
//
// Configure keys once and they are redacted or masked wherever they appear —
// top level, nested in groups, or inside structs passed as plain values.
// When a value cannot be processed it logs as "[UNLOGGABLE]" rather than
// shipping raw: when in doubt, this package hides.
package logger
