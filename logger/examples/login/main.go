// Login flows: credentials are redacted wherever they appear — as direct
// args, bound via With, or buried in a request map.
package main

import (
	"context"

	"github.com/Wigata-Intech/w-tools/logger"
)

func main() {
	log := logger.New(logger.Config{
		Env:      "production",
		App:      "auth",
		Protocol: logger.ProtocolHTTP,
		Redact: logger.RedactConfig{
			Redacted: []string{"password", "authorization", "session_token"},
			Masked:   map[string]logger.Mask{"email": {ShowFirst: 2, ShowLast: 0}},
		},
	})

	ctx := context.Background()
	form := map[string]string{
		"email":    "dhira@example.com",
		"password": "hunter2",
	}
	log.Info(ctx, "login attempt", "form", form)
	// "form":{"email":"dh***************","password":"[REDACTED]"}

	session := log.With("session_token", "tok_8f3a9c")
	session.Info(ctx, "login succeeded", "user_id", "u_1042")
	// "session_token":"[REDACTED]","user_id":"u_1042"

	log.Warn(ctx, "upstream rejected token", "authorization", "Bearer eyJhbGciOi...")
	// "authorization":"[REDACTED]"
}
