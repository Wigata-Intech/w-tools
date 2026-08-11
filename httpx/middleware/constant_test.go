package middleware_test

import (
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{name: "DefaultRequestIDHeader is the conventional name", input: middleware.DefaultRequestIDHeader, expected: "X-Request-ID"},
		{name: "DefaultMaxBody is 64 KiB", input: middleware.DefaultMaxBody, expected: 65536},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.input != tt.expected {
				t.Errorf("got %v, want %v", tt.input, tt.expected)
			}
		})
	}
}
