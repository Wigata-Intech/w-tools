package logger_test

import (
	"log/slog"
	"testing"

	"github.com/Wigata-Intech/w-tools/logger"
)

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{name: "LevelPanic ranks above error", input: logger.LevelPanic > slog.LevelError, expected: true},
		{name: "default replacement wording", input: logger.DefaultReplacement, expected: "[REDACTED]"},
		{name: "unloggable wording", input: logger.Unloggable, expected: "[UNLOGGABLE]"},
		{name: "protocol http", input: string(logger.ProtocolHTTP), expected: "http"},
		{name: "protocol grpc", input: string(logger.ProtocolGRPC), expected: "grpc"},
		{name: "protocol graphql", input: string(logger.ProtocolGraphQL), expected: "graphql"},
		{name: "protocol cron", input: string(logger.ProtocolCron), expected: "cron"},
		{name: "protocol consumer", input: string(logger.ProtocolConsumer), expected: "consumer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.input != tt.expected {
				t.Errorf("got %v, expected %v", tt.input, tt.expected)
			}
		})
	}
}
