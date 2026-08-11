package logger_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/logger"
)

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
	h := logger.Wrap(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: noTime}), r)
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

func TestWrap(t *testing.T) {
	payRules := logger.RedactConfig{
		Redacted: []string{"password", "cvv"},
		Masked:   map[string]logger.Mask{"card_number": {ShowFirst: 6, ShowLast: 4}},
	}
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    struct {
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
}

func TestWrapNoRules(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    logger.RedactConfig
		expected bool // true = Wrap must return the inner handler unchanged
	}{
		{name: "empty config is a pass-through", input: logger.RedactConfig{}, expected: true},
		{name: "any rule wraps the handler", input: logger.RedactConfig{Redacted: []string{"k"}}, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := slog.NewJSONHandler(&bytes.Buffer{}, nil)
			got := logger.Wrap(inner, tt.input)
			if same := got == slog.Handler(inner); same != tt.expected {
				t.Errorf("Wrap returned same handler = %v, expected %v", same, tt.expected)
			}
		})
	}
}
