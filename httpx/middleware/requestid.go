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
}

// requestIDKey keys the request ID in a context.
type requestIDKey struct{}

// RequestID returns middleware that gives every request a correlation ID:
// the inbound header value when present (a gateway may have minted it),
// a freshly generated 32-char hex ID otherwise. The ID is stored in the
// request context — read it with RequestIDFrom — and echoed on the
// response so clients can quote it. If ID generation fails and no inbound
// value exists, the request proceeds without an ID rather than failing.
func RequestID(cfg RequestIDConfig) httpx.Middleware {
	header := cfg.Header
	if header == "" {
		header = DefaultRequestIDHeader
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 128 caps what an untrusted inbound value can make us echo and log.
			id := r.Header.Get(header)
			if id == "" || len(id) > 128 {
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

// RequestIDFrom returns the request ID stored by RequestID, or "" when
// absent.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)

	return id
}
