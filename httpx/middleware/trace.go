package middleware

import (
	"context"
	"net/http"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// traceIDKey keys the trace ID in a context.
type traceIDKey struct{}

// spanIDKey keys the span ID in a context.
type spanIDKey struct{}

// traceFlagsKey keys the trace flags in a context.
type traceFlagsKey struct{}

// Trace returns middleware speaking the W3C Trace Context wire format
// with no OTel dependency: a valid inbound traceparent header has its
// trace-id adopted and a new span-id minted; an absent or malformed one
// gets both minted. Both IDs are stored in the request context — read
// them with TraceIDFrom and SpanIDFrom. Nothing is written to the
// response; outbound propagation belongs to the HTTP client. If minting
// fails, the request proceeds untraced rather than failing.
func Trace() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID, flags, ok := parseTraceparent(r.Header.Get("Traceparent"))
			if !ok {
				// A trace we start ourselves defaults to sampled.
				flags = "01"

				if traceID, ok = randomHex(16); !ok {
					next.ServeHTTP(w, r)

					return
				}
			}

			spanID, ok := randomHex(8)
			if !ok {
				next.ServeHTTP(w, r)

				return
			}

			ctx := context.WithValue(r.Context(), traceIDKey{}, traceID)
			ctx = context.WithValue(ctx, spanIDKey{}, spanID)
			ctx = context.WithValue(ctx, traceFlagsKey{}, flags)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TraceIDFrom returns the trace ID stored by Trace, or "" when absent.
func TraceIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(traceIDKey{}).(string)

	return id
}

// SpanIDFrom returns the span ID stored by Trace, or "" when absent.
func SpanIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(spanIDKey{}).(string)

	return id
}

// TraceFlagsFrom returns the trace flags stored by Trace — an adopted
// inbound value keeps its flags (the sampling decision survives the
// hop), a locally started trace carries "01". "" when absent.
func TraceFlagsFrom(ctx context.Context) string {
	flags, _ := ctx.Value(traceFlagsKey{}).(string)

	return flags
}

// parseTraceparent extracts the trace-id and flags from a traceparent
// value: {2 hex version}-{32 hex trace-id}-{16 hex parent-id}-{2 hex
// flags}, lowercase hex only per the spec. ok is false for any malformed
// value, version "ff", or an all-zero trace-id or parent-id.
func parseTraceparent(s string) (string, string, bool) {
	if len(s) != 55 || s[2] != '-' || s[35] != '-' || s[52] != '-' {
		return "", "", false
	}

	version, traceID, parentID, flags := s[:2], s[3:35], s[36:52], s[53:]
	if !isLowerHex(version) || !isLowerHex(traceID) || !isLowerHex(parentID) || !isLowerHex(flags) {
		return "", "", false
	}

	if version == "ff" || allZero(traceID) || allZero(parentID) {
		return "", "", false
	}

	return traceID, flags, true
}

func isLowerHex(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

func allZero(s string) bool {
	for i := range len(s) {
		if s[i] != '0' {
			return false
		}
	}

	return true
}
