package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/logger"
)

// FuzzMaskString proves the masking invariants hold for input nobody
// anticipated: never panic, rune count preserved, and never reveal more than
// the configured windows.
func FuzzMaskString(f *testing.F) {
	f.Add("4111111111111111", 6, 4)
	f.Add("", 0, 0)
	f.Add("パスワード123", 2, 2)
	f.Add("ab", 5, 5)
	f.Add("secret", -3, -1)
	f.Add("x", math.MaxInt, math.MaxInt)
	f.Fuzz(func(t *testing.T, s string, first, last int) {
		out := logger.MaskString(s, logger.Mask{ShowFirst: first, ShowLast: last})
		rs, ro := []rune(s), []rune(out)
		if len(ro) != len(rs) {
			t.Fatalf("rune count changed: %d -> %d", len(rs), len(ro))
		}
		ef, el := max(first, 0), max(last, 0)
		n := len(rs)
		maskStart, maskEnd := 0, n // full mask unless the windows leave a middle
		if ef < n && el < n-ef {
			maskStart, maskEnd = ef, n-el
		}
		for i, r := range ro {
			if i >= maskStart && i < maskEnd {
				if r != '*' {
					t.Fatalf("position %d not masked in %q -> %q", i, s, out)
				}
				continue
			}
			if r != rs[i] {
				t.Fatalf("revealed position %d altered in %q -> %q", i, s, out)
			}
		}
	})
}

// FuzzRedact proves the pipeline end to end: arbitrary input never panics,
// always emits valid JSON, and a redacted key's value never survives.
func FuzzRedact(f *testing.F) {
	f.Add("user", "dhira", "hunter2")
	f.Add("", "", "")
	f.Add("パス", `"}{`, "秘密") //nolint:gosmopolitan // multi-byte seed is the point: redaction must survive CJK input.
	f.Add("password", "collide", "leak?")
	f.Fuzz(func(t *testing.T, key, val, secret string) {
		var buf bytes.Buffer
		l := logger.New(logger.Config{
			Writer: &buf,
			Redact: logger.RedactConfig{Redacted: []string{"password"}},
		})
		l.Info(context.Background(), "m", key, val, "password", secret)
		line := bytes.TrimSpace(buf.Bytes())
		if !json.Valid(line) {
			t.Fatalf("invalid JSON: %s", line)
		}
		m := make(map[string]any)
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !strings.EqualFold(key, "password") && m["password"] != "[REDACTED]" {
			t.Fatalf("password not redacted: %s", line)
		}
	})
}
