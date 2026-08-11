package middleware

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// headerVary is added per response; a cache must never serve one
// origin's answer to another.
const headerVary = "Vary"

// CORSConfig configures CORS.
type CORSConfig struct {
	// AllowedOrigins are exact origins; "*" allows any. "*" combined
	// with AllowCredentials is forbidden by the Fetch spec and panics
	// at construction.
	AllowedOrigins   []string
	AllowedMethods   []string // default: GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS, QUERY
	AllowedHeaders   []string // default: reflect the preflight's requested headers
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           time.Duration // preflight cache; 0 omits the header
}

// CORS returns a middleware implementing the CORS protocol (Fetch
// standard). Requests without an Origin header are not CORS and pass
// through untouched. Preflights — OPTIONS carrying
// Access-Control-Request-Method — are answered 204 without reaching the
// handler, with the Access-Control-* set only when origin and method are
// allowed. Every other request runs regardless of origin (blocking is
// the browser's job); allowed origins get the Access-Control-* response
// headers stamped. Every response to a request with an Origin carries
// Vary: Origin. A wildcard origin combined with AllowCredentials panics
// at construction.
func CORS(cfg CORSConfig) httpx.Middleware {
	wildcard := slices.Contains(cfg.AllowedOrigins, "*")
	if wildcard && cfg.AllowCredentials {
		panic("middleware: CORS wildcard origin cannot be combined with credentials")
	}

	methods := cfg.AllowedMethods
	if methods == nil {
		methods = []string{
			http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
			http.MethodDelete, http.MethodHead, http.MethodOptions, httpx.MethodQuery,
		}
	}

	allowMethods := strings.Join(methods, ", ")
	allowHeaders := strings.Join(cfg.AllowedHeaders, ", ")
	exposeHeaders := strings.Join(cfg.ExposedHeaders, ", ")
	maxAge := strconv.FormatInt(int64(cfg.MaxAge/time.Second), 10)

	preflightAllow := func(h http.Header, allowOrigin, requestedHeaders string) {
		h.Set("Access-Control-Allow-Origin", allowOrigin)
		h.Set("Access-Control-Allow-Methods", allowMethods)

		if allowHeaders != "" {
			h.Set("Access-Control-Allow-Headers", allowHeaders)
		} else if requestedHeaders != "" {
			h.Set("Access-Control-Allow-Headers", requestedHeaders)
		}

		if cfg.AllowCredentials {
			h.Set("Access-Control-Allow-Credentials", "true")
		}

		if cfg.MaxAge > 0 {
			h.Set("Access-Control-Max-Age", maxAge)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Every response varies by Origin — including same-origin ones —
			// or a shared cache could serve one origin's answer to another.
			h := w.Header()
			h.Add(headerVary, "Origin")

			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := wildcard || slices.Contains(cfg.AllowedOrigins, origin)

			allowOrigin := origin
			if wildcard {
				allowOrigin = "*"
			}

			reqMethod := r.Header.Get("Access-Control-Request-Method")
			if r.Method == http.MethodOptions && reqMethod != "" {
				h.Add(headerVary, "Access-Control-Request-Method")
				h.Add(headerVary, "Access-Control-Request-Headers")

				if allowed && slices.Contains(methods, reqMethod) {
					preflightAllow(h, allowOrigin, r.Header.Get("Access-Control-Request-Headers"))
				}

				w.WriteHeader(http.StatusNoContent)

				return
			}

			if allowed {
				h.Set("Access-Control-Allow-Origin", allowOrigin)

				if cfg.AllowCredentials {
					h.Set("Access-Control-Allow-Credentials", "true")
				}

				if exposeHeaders != "" {
					h.Set("Access-Control-Expose-Headers", exposeHeaders)
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
