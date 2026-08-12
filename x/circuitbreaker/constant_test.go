package circuitbreaker_test

import (
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/x/circuitbreaker"
)

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{name: "DefaultFailureRatio", input: circuitbreaker.DefaultFailureRatio, expected: 0.5},
		{name: "DefaultMinRequests", input: circuitbreaker.DefaultMinRequests, expected: 10},
		{name: "DefaultWindow", input: circuitbreaker.DefaultWindow, expected: 10 * time.Second},
		{name: "DefaultWindowBuckets", input: circuitbreaker.DefaultWindowBuckets, expected: 10},
		{name: "DefaultOpenFor", input: circuitbreaker.DefaultOpenFor, expected: 30 * time.Second},
		{name: "DefaultHalfOpenProbes", input: circuitbreaker.DefaultHalfOpenProbes, expected: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.input != tt.expected {
				t.Errorf("got %v, want %v", tt.input, tt.expected)
			}
		})
	}
}
