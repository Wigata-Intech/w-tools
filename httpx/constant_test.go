package httpx_test

import (
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
)

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{name: "MethodQuery is the RFC 10008 token", input: httpx.MethodQuery, expected: "QUERY"},
		{name: "DefaultReadHeaderTimeout", input: httpx.DefaultReadHeaderTimeout, expected: 5 * time.Second},
		{name: "DefaultReadTimeout", input: httpx.DefaultReadTimeout, expected: 10 * time.Second},
		{name: "DefaultWriteTimeout", input: httpx.DefaultWriteTimeout, expected: 30 * time.Second},
		{name: "DefaultIdleTimeout", input: httpx.DefaultIdleTimeout, expected: 120 * time.Second},
		{name: "DefaultShutdownGrace", input: httpx.DefaultShutdownGrace, expected: 15 * time.Second},
		{name: "DefaultMaxHeaderBytes is 1 MiB", input: httpx.DefaultMaxHeaderBytes, expected: 1 << 20},
		{name: "DefaultMaxBind is 1 MiB", input: httpx.DefaultMaxBind, expected: int64(1) << 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.input != tt.expected {
				t.Errorf("got %v, want %v", tt.input, tt.expected)
			}
		})
	}
}
