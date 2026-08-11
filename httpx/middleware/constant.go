package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

// DefaultRequestIDHeader is the header RequestID reads and echoes when
// RequestIDConfig.Header is empty.
const DefaultRequestIDHeader = "X-Request-ID"

// DefaultMaxBody caps middleware body capture (used by the Logger middleware).
const DefaultMaxBody = 64 << 10 // 64 KiB

// randSource feeds ID generation; swapped only by tests.
var randSource = rand.Reader //nolint:gochecknoglobals // test seam: rand failure is unreachable otherwise

// randomHex returns n random bytes hex-encoded, or ok=false if the
// source fails.
func randomHex(n int) (string, bool) {
	b := make([]byte, n)
	if _, err := io.ReadFull(randSource, b); err != nil {
		return "", false
	}

	return hex.EncodeToString(b), true
}
