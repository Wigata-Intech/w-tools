package logger_test

import (
	"log/slog"
	"os"

	"github.com/Wigata-Intech/w-tools/logger"
)

func ExampleWrap() {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{} // dropped for a deterministic example
			}
			return a
		},
	})
	log := slog.New(logger.Wrap(h, logger.WrapConfig{Redact: logger.RedactConfig{Redacted: []string{"password"}}}))
	log.Info("login", "user", "dhira", "password", "hunter2")
	// Output: {"level":"INFO","msg":"login","user":"dhira","password":"[REDACTED]"}
}
