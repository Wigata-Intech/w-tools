package middleware

import (
	"context"
	"net/http"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// RequestIDConfig configures RequestID. The zero value is production-ready.
type RequestIDConfig struct {
	// Header carrying the ID. Default DefaultRequestIDHeader.
	Header string

	// Valid decides whether a non-empty inbound ID is accepted. A
	// rejected ID is treated as absent: a fresh ID replaces it and it is
	// never echoed. Nil accepts any value of at most 128 bytes;
	// StrictRequestID is the ready-made strict rule.
	Valid func(id string) bool
}

// requestIDKey keys the request ID in a context.
type requestIDKey struct{}

// RequestID returns middleware that gives every request a correlation ID:
// the inbound header value when present and valid (a gateway may have
// minted it), a freshly generated 32-char hex ID otherwise. The ID is
// stored in the request context — read it with RequestIDFrom — and
// echoed on the response so clients can quote it. If ID generation fails
// and no valid inbound value exists, the request proceeds without an ID
// rather than failing.
func RequestID(cfg RequestIDConfig) httpx.Middleware {
	header := cfg.Header
	if header == "" {
		header = DefaultRequestIDHeader
	}

	valid := cfg.Valid
	if valid == nil {
		// 128 caps what an untrusted inbound value can make us echo and log.
		valid = func(id string) bool { return len(id) <= 128 }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(header)
			if id == "" || !valid(id) {
				var ok bool
				if id, ok = randomHex(16); !ok {
					next.ServeHTTP(w, r)

					return
				}
			}

			// Set before next so the ID rides out with the first write.
			w.Header().Set(header, id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
		})
	}
}

// StrictRequestID reports whether id is 1 to 64 characters, each an
// ASCII letter, digit, '.', '_' or '-'. Use it as RequestIDConfig.Valid
// when inbound IDs must be safe to echo and log verbatim.
func StrictRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}

	for i := range len(id) {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}

	return true
}

// RequestIDFrom returns the request ID stored by RequestID, or "" when
// absent.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)

	return id
}
