package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/logger"
)

// fields unmarshals the first JSON line written to buf.
func fields(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line, _, _ := strings.Cut(buf.String(), "\n")
	m := make(map[string]any)
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("unmarshal log line %q: %v", line, err)
	}
	return m
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    logger.Config
		expected map[string]any // keys that must be present with these values; "-key" asserts absence
	}{
		{
			name:  "base fields stamped on every line",
			input: logger.Config{Env: "production", Version: "1.4.2", App: "wipays", Protocol: logger.ProtocolHTTP},
			expected: map[string]any{
				"env": "production", "version": "1.4.2", "app": "wipays", "protocol": "http",
			},
		},
		{
			name:     "custom protocol value accepted",
			input:    logger.Config{Protocol: logger.Protocol("webhook")},
			expected: map[string]any{"protocol": "webhook"},
		},
		{
			name:     "empty base fields omitted not logged empty",
			input:    logger.Config{App: "wipays"},
			expected: map[string]any{"app": "wipays", "-env": nil, "-version": nil, "-protocol": nil},
		},
		{
			name:     "redaction wired through config",
			input:    logger.Config{Redact: logger.RedactConfig{Redacted: []string{"password"}}},
			expected: map[string]any{"password": "[REDACTED]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.input.Writer = &buf
			logger.New(tt.input).Info(context.Background(), "m", "password", "hunter2")
			got := fields(t, &buf)
			for k, v := range tt.expected {
				if absent, ok := strings.CutPrefix(k, "-"); ok {
					if _, exists := got[absent]; exists {
						t.Errorf("field %q expected absent, got %v", absent, got[absent])
					}
					continue
				}
				if got[k] != v {
					t.Errorf("field %q = %v, expected %v", k, got[k], v)
				}
			}
		})
	}
}

func TestNewDefaultWriter(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    logger.Config
		expected bool // logger constructed
	}{
		{name: "nil writer defaults to stdout", input: logger.Config{}, expected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := logger.New(tt.input) != nil; got != tt.expected {
				t.Errorf("constructed = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    string
		expected slog.Level
	}{
		{name: "debug", input: "debug", expected: slog.LevelDebug},
		{name: "case-insensitive", input: "DeBuG", expected: slog.LevelDebug},
		{name: "info", input: "info", expected: slog.LevelInfo},
		{name: "warn", input: "warn", expected: slog.LevelWarn},
		{name: "error", input: "error", expected: slog.LevelError},
		{name: "panic", input: "panic", expected: logger.LevelPanic},
		{name: "empty falls back to info", input: "", expected: slog.LevelInfo},
		{name: "unknown falls back to info", input: "loud", expected: slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := logger.ParseLevel(tt.input); got != tt.expected {
				t.Errorf("ParseLevel(%q) = %v, expected %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestLoggerLevels(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    struct {
			level slog.Level
			logFn func(*logger.Logger)
		}
		expected string // "" means no output at all
	}{
		{
			name: "debug logs at debug level",
			input: struct {
				level slog.Level
				logFn func(*logger.Logger)
			}{slog.LevelDebug, func(l *logger.Logger) { l.Debug(ctx, "m") }},
			expected: "DEBUG",
		},
		{
			name: "debug suppressed at default info",
			input: struct {
				level slog.Level
				logFn func(*logger.Logger)
			}{slog.LevelInfo, func(l *logger.Logger) { l.Debug(ctx, "m") }},
			expected: "",
		},
		{
			name: "info logs at info",
			input: struct {
				level slog.Level
				logFn func(*logger.Logger)
			}{slog.LevelInfo, func(l *logger.Logger) { l.Info(ctx, "m") }},
			expected: "INFO",
		},
		{
			name: "warn logs at warn",
			input: struct {
				level slog.Level
				logFn func(*logger.Logger)
			}{slog.LevelWarn, func(l *logger.Logger) { l.Warn(ctx, "m") }},
			expected: "WARN",
		},
		{
			name: "error logs at error",
			input: struct {
				level slog.Level
				logFn func(*logger.Logger)
			}{slog.LevelError, func(l *logger.Logger) { l.Error(ctx, "m") }},
			expected: "ERROR",
		},
		{
			name: "info suppressed at error level",
			input: struct {
				level slog.Level
				logFn func(*logger.Logger)
			}{slog.LevelError, func(l *logger.Logger) { l.Info(ctx, "m") }},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := logger.New(logger.Config{Level: tt.input.level, Writer: &buf})
			tt.input.logFn(l)
			if tt.expected == "" {
				if buf.Len() != 0 {
					t.Errorf("expected no output, got %s", buf.String())
				}
				return
			}
			if got := fields(t, &buf)["level"]; got != tt.expected {
				t.Errorf("level = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestLoggerPanic(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    string // panic message
		expected string // level rendered in the record written before panicking
	}{
		{name: "record written as PANIC before panicking", input: "boom", expected: "PANIC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := logger.New(logger.Config{Writer: &buf})
			defer func() {
				r := recover()
				if r != tt.input {
					t.Errorf("recovered %v, expected %v", r, tt.input)
				}
				if got := fields(t, &buf)["level"]; got != tt.expected {
					t.Errorf("level = %v, expected %v", got, tt.expected)
				}
			}()
			l.Panic(context.Background(), tt.input, "k", "v")
		})
	}
}

func TestLoggerWith(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    []any
		expected map[string]any
	}{
		{
			name:     "bound args appear on child records",
			input:    []any{"request_id", "req_9"},
			expected: map[string]any{"request_id": "req_9"},
		},
		{
			name:     "bound args pass through redaction",
			input:    []any{"password", "hunter2"},
			expected: map[string]any{"password": "[REDACTED]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := logger.New(logger.Config{
				Writer: &buf,
				Redact: logger.RedactConfig{Redacted: []string{"password"}},
			})
			l.With(tt.input...).Info(context.Background(), "m")
			got := fields(t, &buf)
			for k, v := range tt.expected {
				if got[k] != v {
					t.Errorf("field %q = %v, expected %v", k, got[k], v)
				}
			}
		})
	}
}

func TestLoggerSlog(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    struct {
			cfg   logger.Config
			logFn func(*slog.Logger)
		}
		expected map[string]any
	}{
		{
			name: "escape hatch logs through the same pipeline",
			input: struct {
				cfg   logger.Config
				logFn func(*slog.Logger)
			}{
				logger.Config{Redact: logger.RedactConfig{Redacted: []string{"password"}}},
				func(s *slog.Logger) { s.Info("m", "password", "hunter2") },
			},
			expected: map[string]any{"password": "[REDACTED]", "msg": "m"},
		},
		{
			name: "WithGroup flows through the full handler chain",
			input: struct {
				cfg   logger.Config
				logFn func(*slog.Logger)
			}{
				logger.Config{Redact: logger.RedactConfig{Redacted: []string{"password"}}},
				func(s *slog.Logger) { s.WithGroup("req").Info("m", "password", "hunter2") },
			},
			expected: map[string]any{"msg": "m"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.input.cfg.Writer = &buf
			s := logger.New(tt.input.cfg).Slog()
			if s == nil {
				t.Fatal("Slog() returned nil")
			}
			tt.input.logFn(s)
			got := fields(t, &buf)
			for k, v := range tt.expected {
				if got[k] != v {
					t.Errorf("field %q = %v, expected %v", k, got[k], v)
				}
			}
		})
	}
}
