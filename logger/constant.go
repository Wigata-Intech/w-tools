package logger

import "log/slog"

// LevelPanic ranks above slog.LevelError. Logger.Panic logs at this level,
// rendered as "PANIC", before panicking.
const LevelPanic = slog.Level(12)

// DefaultReplacement is written for redacted values when
// RedactConfig.Replacement is empty.
const DefaultReplacement = "[REDACTED]"

// Unloggable replaces any value the redaction layer could not process —
// fail closed: when redaction cannot run, the data does not ship.
const Unloggable = "[UNLOGGABLE]"

// Protocol names the entry point a service logs under; see Config.Protocol.
// Any value works — Protocol("webhook") — these cover the usual ones.
type Protocol string

// Common Config.Protocol values.
const (
	ProtocolHTTP     Protocol = "http"
	ProtocolGRPC     Protocol = "grpc"
	ProtocolGraphQL  Protocol = "graphql"
	ProtocolCron     Protocol = "cron"
	ProtocolConsumer Protocol = "consumer"
)
