package middleware

import (
	"net/http"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// BodyLimitConfig configures BodyLimit. The zero value is production-ready.
type BodyLimitConfig struct {
	// Max is the largest accepted request body in bytes. Zero or
	// negative means httpx.DefaultMaxBind.
	Max int64

	ErrorWriter httpx.ErrorWriter // nil = RFC 9457 Problem
}

// BodyLimit returns middleware that caps request bodies at Max bytes. A
// request whose declared Content-Length exceeds Max is answered 413 with
// detail "request body too large" before next runs. Every other request
// reaches next with its body wrapped by http.MaxBytesReader, so reading a
// chunked or undeclared body past Max fails with *http.MaxBytesError.
func BodyLimit(cfg BodyLimitConfig) httpx.Middleware {
	limit := cfg.Max
	if limit <= 0 {
		limit = httpx.DefaultMaxBind
	}

	errorWriter := cfg.ErrorWriter
	if errorWriter == nil {
		errorWriter = func(w http.ResponseWriter, _ *http.Request, status int, detail string) {
			httpx.Error(w, status, detail)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				errorWriter(w, r, http.StatusRequestEntityTooLarge, "request body too large")

				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
