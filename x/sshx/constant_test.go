package sshx_test

import (
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/x/sshx"
)

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{name: "DefaultDialTimeout", input: sshx.DefaultDialTimeout, expected: 10 * time.Second},
		{name: "DefaultPingTimeout", input: sshx.DefaultPingTimeout, expected: 5 * time.Second},
		{name: "DefaultTerm", input: sshx.DefaultTerm, expected: "xterm-256color"},
		{name: "DefaultCols", input: sshx.DefaultCols, expected: 80},
		{name: "DefaultRows", input: sshx.DefaultRows, expected: 24},
		{name: "DefaultMaxDials", input: sshx.DefaultMaxDials, expected: 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.input != tt.expected {
				t.Errorf("got %v, want %v", tt.input, tt.expected)
			}
		})
	}
}
