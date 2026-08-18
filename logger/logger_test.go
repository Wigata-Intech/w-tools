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
		name  string
		input struct {
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
		name  string
		input struct {
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

func TestWrap(t *testing.T) {
	payRules := logger.RedactConfig{
		Redacted: []string{"password", "cvv"},
		Masked:   map[string]logger.Mask{"card_number": {ShowFirst: 6, ShowLast: 4}},
	}
	tests := []struct {
		name  string
		input struct {
			rules logger.RedactConfig
			logFn func(*slog.Logger)
		}
		expected string
	}{
		{
			name: "golden line with mask and redact at top level",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("payment created", "order_id", "ord_123", "card_number", "4111111111111111", "password", "hunter2")
			}},
			expected: `{"level":"INFO","msg":"payment created","order_id":"ord_123","card_number":"411111******1111","password":"[REDACTED]"}`,
		},
		{
			name: "key match is case-insensitive",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "PassWord", "hunter2")
			}},
			expected: `{"level":"INFO","msg":"m","PassWord":"[REDACTED]"}`,
		},
		{
			name: "untouched record passes through unchanged",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "order_id", "ord_123")
			}},
			expected: `{"level":"INFO","msg":"m","order_id":"ord_123"}`,
		},
		{
			name: "rules apply inside groups",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", slog.Group("req", slog.String("password", "hunter2"), slog.String("path", "/login")))
			}},
			expected: `{"level":"INFO","msg":"m","req":{"password":"[REDACTED]","path":"/login"}}`,
		},
		{
			name: "groups nested past the depth cap fail closed",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", deepGroups(20))
			}},
			expected: `"[UNLOGGABLE]"`,
		},
		{
			name: "group with no matches passes through",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", slog.Group("req", slog.String("path", "/login")))
			}},
			expected: `{"level":"INFO","msg":"m","req":{"path":"/login"}}`,
		},
		{
			name: "struct fields redacted two levels deep, json tags honored, unexported skipped",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "user", user{Name: "d", Password: "hunter2", Card: card{Number: "4111111111111111", CVV: "123", Holder: "D W"}, internal: "x"})
			}},
			expected: `{"level":"INFO","msg":"m","user":{"Card":{"card_number":"411111******1111","cvv":"[REDACTED]","holder":"D W"},"Name":"d","Password":"[REDACTED]"}}`,
		},
		{
			name: "struct with no matches keeps json.Marshal path",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "who", struct {
					Name string `json:"name,omitempty"`
					Age  int    `json:"-"`
				}{Name: "", Age: 7})
			}},
			expected: `{"level":"INFO","msg":"m","who":{}}`,
		},
		{
			name: "pointer to struct followed",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "card", &card{Number: "4111111111111111", CVV: "123", Holder: "D"})
			}},
			expected: `{"level":"INFO","msg":"m","card":{"card_number":"411111******1111","cvv":"[REDACTED]","holder":"D"}}`,
		},
		{
			name: "nil pointer stays nil",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				var c *card
				l.Info("m", "card", c, "password", "x")
			}},
			expected: `{"level":"INFO","msg":"m","card":null,"password":"[REDACTED]"}`,
		},
		{
			name: "untyped nil value stays null",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "extra", nil, "password", "x")
			}},
			expected: `{"level":"INFO","msg":"m","extra":null,"password":"[REDACTED]"}`,
		},
		{
			name: "map keys matched at any depth",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "form", map[string]any{"password": "hunter2", "next": map[string]string{"cvv": "123"}})
			}},
			expected: `{"level":"INFO","msg":"m","form":{"next":{"cvv":"[REDACTED]"},"password":"[REDACTED]"}}`,
		},
		{
			name: "map values masked not only redacted",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "form", map[string]string{"card_number": "4111111111111111"})
			}},
			expected: `{"level":"INFO","msg":"m","form":{"card_number":"411111******1111"}}`,
		},
		{
			name: "slice of structs sanitized element-wise",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "cards", []card{{Number: "4111111111111111", CVV: "1", Holder: "A"}})
			}},
			expected: `{"level":"INFO","msg":"m","cards":[{"card_number":"411111******1111","cvv":"[REDACTED]","holder":"A"}]}`,
		},
		{
			name: "logvaluer resolved before rules apply",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "account", groupValuer{})
			}},
			expected: `{"level":"INFO","msg":"m","account":{"password":"[REDACTED]","name":"d"}}`,
		},
		{
			name: "panicking logvaluer fails closed",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "account", panicValuer{})
			}},
			expected: `{"level":"INFO","msg":"m","account":"[UNLOGGABLE]"}`,
		},
		{
			name: "endless logvaluer chain fails closed",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "account", chainValuer{})
			}},
			expected: `{"level":"INFO","msg":"m","account":"[UNLOGGABLE]"}`,
		},
		{
			name: "panicking logvaluer under a redacted key still redacts",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "password", panicValuer{})
			}},
			expected: `{"level":"INFO","msg":"m","password":"[REDACTED]"}`,
		},
		{
			name: "panicking logvaluer under a masked key fails closed",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.Info("m", "card_number", panicValuer{})
			}},
			expected: `{"level":"INFO","msg":"m","card_number":"[UNLOGGABLE]"}`,
		},
		{
			name: "redact wins over mask on the same key",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{logger.RedactConfig{
				Redacted: []string{"token"},
				Masked:   map[string]logger.Mask{"token": {ShowFirst: 2}},
			}, func(l *slog.Logger) {
				l.Info("m", "token", "abcdef")
			}},
			expected: `{"level":"INFO","msg":"m","token":"[REDACTED]"}`,
		},
		{
			name: "custom replacement wording",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{logger.RedactConfig{Redacted: []string{"password"}, Replacement: "***"}, func(l *slog.Logger) {
				l.Info("m", "password", "hunter2")
			}},
			expected: `{"level":"INFO","msg":"m","password":"***"}`,
		},
		{
			name: "attrs bound via With are redacted",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.With("password", "hunter2").Info("m")
			}},
			expected: `{"level":"INFO","msg":"m","password":"[REDACTED]"}`,
		},
		{
			name: "rules apply inside WithGroup",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				l.WithGroup("req").Info("m", "password", "hunter2")
			}},
			expected: `{"level":"INFO","msg":"m","req":{"password":"[REDACTED]"}}`,
		},
		{
			name: "pointer cycle terminates as unloggable",
			input: struct {
				rules logger.RedactConfig
				logFn func(*slog.Logger)
			}{payRules, func(l *slog.Logger) {
				a := &cyclic{Name: "a"}
				a.Next = a
				l.Info("m", "chain", a)
			}},
			expected: `"[UNLOGGABLE]"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := logLine(t, tt.input.rules, tt.input.logFn)
			// A full-line expectation starts with "{"; anything else is a
			// contains-check for output too deep to assert byte-for-byte.
			if !strings.HasPrefix(tt.expected, "{") {
				if !strings.Contains(got, tt.expected) {
					t.Errorf("line %q does not contain %q", got, tt.expected)
				}
				return
			}
			if got != tt.expected {
				t.Errorf("line = %s, expected %s", got, tt.expected)
			}
		})
	}

	t.Run("no layers configured returns the handler unchanged", func(t *testing.T) {
		inner := slog.NewJSONHandler(&bytes.Buffer{}, nil)
		if got := logger.Wrap(inner, logger.WrapConfig{}); got != slog.Handler(inner) {
			t.Fatalf("Wrap(h, WrapConfig{}) = %T, want the identical handler", got)
		}
		if got := logger.Wrap(inner, logger.WrapConfig{Redact: logger.RedactConfig{Redacted: []string{"k"}}}); got == slog.Handler(inner) {
			t.Fatal("Wrap with a rule must wrap the handler")
		}
	})
}

// noTime drops the time attr so output lines compare byte-for-byte.
func noTime(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

// logLine logs one record through Wrap and returns the JSON line without time.
func logLine(t *testing.T, r logger.RedactConfig, logFn func(*slog.Logger)) string {
	t.Helper()
	var buf bytes.Buffer
	h := logger.Wrap(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: noTime}), logger.WrapConfig{Redact: r})
	logFn(slog.New(h))
	return strings.TrimSuffix(buf.String(), "\n")
}

type card struct {
	Number string `json:"card_number"`
	CVV    string `json:"cvv"`
	Holder string `json:"holder"`
}

type user struct {
	Name     string
	Password string
	Card     card
	internal string
}

type panicValuer struct{}

func (panicValuer) LogValue() slog.Value { panic("boom") }

type groupValuer struct{}

func (groupValuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("password", "hunter2"), slog.String("name", "d"))
}

type cyclic struct {
	Name string
	Next *cyclic
}

// chainValuer resolves to another chainValuer forever.
type chainValuer struct{}

func (chainValuer) LogValue() slog.Value { return slog.AnyValue(chainValuer{}) }

// deepGroups nests a group n levels around a redacted key.
func deepGroups(n int) slog.Attr {
	a := slog.String("password", "hunter2")
	for range n {
		a = slog.Group("g", a)
	}
	return a
}
