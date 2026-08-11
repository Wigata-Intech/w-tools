package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"math"
	"testing"

	"github.com/Wigata-Intech/w-tools/logger"
)

// maskVia runs one value through a masking rule using the public API and
// returns the masked string as it would be logged.
func maskVia(t *testing.T, m logger.Mask, value any) string {
	t.Helper()
	var buf bytes.Buffer
	h := logger.Wrap(slog.NewJSONHandler(&buf, nil), logger.RedactConfig{
		Masked: map[string]logger.Mask{"secret": m},
	})
	slog.New(h).Info("m", "secret", value)
	var line struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	return line.Secret
}

func TestMaskString(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    struct {
			mask  logger.Mask
			value any
		}
		expected string
	}{
		{
			name: "middle masked with defaults applied",
			input: struct {
				mask  logger.Mask
				value any
			}{logger.Mask{ShowFirst: 6, ShowLast: 4}, "4111111111111111"},
			expected: "411111******1111",
		},
		{
			name: "custom mask char",
			input: struct {
				mask  logger.Mask
				value any
			}{logger.Mask{ShowFirst: 2, MaskChar: '•'}, "dhira@example.com"},
			expected: "dh•••••••••••••••",
		},
		{
			name: "unicode masked by rune not byte",
			input: struct {
				mask  logger.Mask
				value any
			}{logger.Mask{ShowFirst: 1, ShowLast: 1}, "パスワード"},
			expected: "パ***ド",
		},
		{
			name: "non-string value stringified before masking",
			input: struct {
				mask  logger.Mask
				value any
			}{logger.Mask{ShowFirst: 2, ShowLast: 2}, 4111111111111111},
			expected: "41************11",
		},
		{
			name: "negative bounds clamp to zero",
			input: struct {
				mask  logger.Mask
				value any
			}{logger.Mask{ShowFirst: -3, ShowLast: -1}, "abc"},
			expected: "***",
		},
		{
			name: "exact boundary masks everything",
			input: struct {
				mask  logger.Mask
				value any
			}{logger.Mask{ShowFirst: 2, ShowLast: 2}, "abcd"},
			expected: "****",
		},
		{
			name: "huge bounds mask everything without overflow",
			input: struct {
				mask  logger.Mask
				value any
			}{logger.Mask{ShowFirst: math.MaxInt, ShowLast: math.MaxInt}, "abc"},
			expected: "***",
		},
		{
			name: "shorter than bounds masks everything",
			input: struct {
				mask  logger.Mask
				value any
			}{logger.Mask{ShowFirst: 6, ShowLast: 4}, "123"},
			expected: "***",
		},
		{
			name: "empty value stays empty",
			input: struct {
				mask  logger.Mask
				value any
			}{logger.Mask{ShowFirst: 6, ShowLast: 4}, ""},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskVia(t, tt.input.mask, tt.input.value)
			if got != tt.expected {
				t.Errorf("masked = %q, expected %q", got, tt.expected)
			}
		})
	}
}
