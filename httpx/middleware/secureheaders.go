package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// SecureHeadersConfig configures SecureHeaders. The zero value is
// production-ready for a JSON API. Each header field takes "" for its
// default, OmitHeader to not send the header, or any other value to send
// verbatim.
type SecureHeadersConfig struct {
	// ContentTypeOptions is X-Content-Type-Options. Default "nosniff".
	ContentTypeOptions string

	// ReferrerPolicy is Referrer-Policy. Default DefaultReferrerPolicy.
	ReferrerPolicy string

	// ContentSecurityPolicy is Content-Security-Policy. Default
	// DefaultContentSecurityPolicy.
	ContentSecurityPolicy string

	// HSTSMaxAge turns on Strict-Transport-Security, sent as
	// "max-age=<whole seconds>". Under one second (the default) the
	// header is not sent.
	HSTSMaxAge time.Duration

	// HSTSIncludeSubDomains appends "; includeSubDomains" to
	// Strict-Transport-Security.
	HSTSIncludeSubDomains bool

	// HSTSPreload appends "; preload" to Strict-Transport-Security.
	// Preload-list submission also requires HSTSIncludeSubDomains and a
	// max-age of at least one year; SecureHeaders enforces neither.
	HSTSPreload bool
}

// headerValue is one response header SecureHeaders sets.
type headerValue struct {
	name  string
	value string
}

// SecureHeaders returns middleware that sets the configured security
// headers on the response before calling next, so every response passing
// through it carries them — handler output, routing 404/405, gate
// short-circuits, and 500s written by a Recover placed inside it.
// Handlers may still overwrite any of them. Values are fixed at
// construction.
func SecureHeaders(cfg SecureHeadersConfig) httpx.Middleware {
	headers := make([]headerValue, 0, 4)

	add := func(name, value, def string) {
		switch value {
		case "":
			value = def
		case OmitHeader:
			return
		}

		headers = append(headers, headerValue{name: name, value: value})
	}

	add("X-Content-Type-Options", cfg.ContentTypeOptions, "nosniff")
	add("Referrer-Policy", cfg.ReferrerPolicy, DefaultReferrerPolicy)
	add("Content-Security-Policy", cfg.ContentSecurityPolicy, DefaultContentSecurityPolicy)

	if secs := int64(cfg.HSTSMaxAge / time.Second); secs > 0 {
		hsts := "max-age=" + strconv.FormatInt(secs, 10)
		if cfg.HSTSIncludeSubDomains {
			hsts += "; includeSubDomains"
		}
		if cfg.HSTSPreload {
			hsts += "; preload"
		}

		headers = append(headers, headerValue{name: "Strict-Transport-Security", value: hsts})
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			for _, hv := range headers {
				h.Set(hv.name, hv.value)
			}

			next.ServeHTTP(w, r)
		})
	}
}
