package keys_test

import (
	"testing"

	"github.com/Wigata-Intech/w-tools/x/sshx/keys"
)

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{name: "MinRSABits", input: keys.MinRSABits, expected: 2048},
		{name: "DefaultRSABits", input: keys.DefaultRSABits, expected: 3072},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.input != tt.expected {
				t.Errorf("got %v, want %v", tt.input, tt.expected)
			}
		})
	}
}
