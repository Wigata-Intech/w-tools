package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// RecoverConfig configures Recover.
type RecoverConfig struct {
	// Log receives the panic record. Nil means slog.Default().
	Log *slog.Logger

	// ErrorWriter writes the 500. Nil means an RFC 9457 Problem.
	ErrorWriter httpx.ErrorWriter
}

// Recover turns handler panics into logged 500 responses instead of dead
// connections. http.ErrAbortHandler is re-panicked, honoring its
// abort-silently contract. The 500 is written only when no response
// header has gone out yet.
func Recover(cfg RecoverConfig) httpx.Middleware {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}

	errorWriter := cfg.ErrorWriter
	if errorWriter == nil {
		errorWriter = func(w http.ResponseWriter, _ *http.Request, status int, detail string) {
			httpx.Error(w, status, detail)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &sentWriter{ResponseWriter: w}

			defer func() { //nolint:contextcheck // the deferred recover logs with the request's own context
				p := recover()
				if p == nil {
					return
				}
				if p == http.ErrAbortHandler { //nolint:err113,errorlint // net/http recognizes this sentinel by identity only; matching its contract exactly
					panic(p)
				}

				log.ErrorContext(r.Context(), "panic recovered",
					"panic", p,
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
				)

				if !sw.sent {
					errorWriter(sw, r, http.StatusInternalServerError, "internal error")
				}
			}()

			next.ServeHTTP(sw, r)
		})
	}
}

// sentWriter records whether a final response header already went out.
// Informational (1xx) headers don't count: after 103 Early Hints the
// final status is still writable, so a panic must still become a 500.
type sentWriter struct {
	http.ResponseWriter

	sent bool
}

func (w *sentWriter) WriteHeader(status int) {
	if status >= http.StatusOK {
		w.sent = true
	}

	w.ResponseWriter.WriteHeader(status)
}

func (w *sentWriter) Write(b []byte) (int, error) {
	w.sent = true
	return w.ResponseWriter.Write(b)
}

func (w *sentWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
