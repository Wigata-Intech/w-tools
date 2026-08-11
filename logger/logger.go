package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config configures New. Every field has a safe default: an empty Config
// yields an info-level JSON logger on os.Stdout with no base fields and no
// redaction.
type Config struct {
	Env     string // e.g. "production"; omitted from output when empty
	Version string // application version; omitted when empty
	App     string // application name; omitted when empty

	// Protocol tags every line with the service entry point. Use the
	// Protocol constants — ProtocolHTTP, ProtocolGRPC, ProtocolGraphQL,
	// ProtocolCron, ProtocolConsumer — or any custom value. Omitted when empty.
	Protocol Protocol

	// Level is the minimum level logged. The zero value is slog.LevelInfo,
	// so leaving it unset gives an info-level logger. For level names from
	// env vars or config files ("debug", "warn"), use ParseLevel.
	Level slog.Level

	Redact RedactConfig
	Writer io.Writer // default os.Stdout
}

// Logger wraps a *slog.Logger. Create one with New.
type Logger struct {
	s     *slog.Logger
	level *slog.LevelVar
}

// New builds a Logger from cfg: JSON handler, redaction layer, and the
// non-empty base fields (env, version, app, protocol) attached to every line.
func New(cfg Config) *Logger {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	level := new(slog.LevelVar)
	level.Set(cfg.Level)
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: renameLevel,
	})
	l := slog.New(Wrap(h, cfg.Redact))
	var base []any
	for _, f := range [...]struct{ key, val string }{
		{"env", cfg.Env}, {"version", cfg.Version}, {"app", cfg.App}, {"protocol", string(cfg.Protocol)},
	} {
		if f.val != "" {
			base = append(base, slog.String(f.key, f.val))
		}
	}
	if len(base) > 0 {
		l = l.With(base...)
	}
	return &Logger{s: l, level: level}
}

// ParseLevel converts a level name — "debug", "info", "warn", "error" or
// "panic", case-insensitive — to its slog.Level, for wiring Config.Level
// from env vars or config files. Unknown names fall back to slog.LevelInfo.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "panic":
		return LevelPanic
	default:
		return slog.LevelInfo
	}
}

// Debug logs at slog.LevelDebug.
func (l *Logger) Debug(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, slog.LevelDebug, msg, args...)
}

// Info logs at slog.LevelInfo.
func (l *Logger) Info(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, slog.LevelInfo, msg, args...)
}

// Warn logs at slog.LevelWarn.
func (l *Logger) Warn(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, slog.LevelWarn, msg, args...)
}

// Error logs at slog.LevelError.
func (l *Logger) Error(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, slog.LevelError, msg, args...)
}

// Panic logs at LevelPanic, then panics with msg. The record is written
// before the panic unwinds.
func (l *Logger) Panic(ctx context.Context, msg string, args ...any) {
	l.s.Log(ctx, LevelPanic, msg, args...)
	panic(msg)
}

// With returns a child Logger with args bound to every subsequent record.
// Bound args pass through redaction like any other attribute.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{s: l.s.With(args...), level: l.level}
}

// Slog returns the underlying *slog.Logger for code that wants slog directly.
func (l *Logger) Slog() *slog.Logger {
	return l.s
}

// renameLevel renders LevelPanic as "PANIC" instead of slog's "ERROR+4".
func renameLevel(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.LevelKey {
		if lvl, ok := a.Value.Any().(slog.Level); ok && lvl >= LevelPanic {
			return slog.String(slog.LevelKey, "PANIC")
		}
	}
	return a
}
