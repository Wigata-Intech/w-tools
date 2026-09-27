package middleware_test

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

func TestSecureHeaders(t *testing.T) {
	defaults := map[string][]string{
		"X-Content-Type-Options":  {"nosniff"},
		"Referrer-Policy":         {"no-referrer"},
		"Content-Security-Policy": {"default-src 'none'; frame-ancestors 'none'"},
	}

	// withHSTS returns the default set plus the given HSTS value.
	withHSTS := func(hsts string) map[string][]string {
		h := map[string][]string{"Strict-Transport-Security": {hsts}}
		maps.Copy(h, defaults)

		return h
	}

	tests := []struct {
		name     string
		input    middleware.SecureHeadersConfig
		expected map[string][]string // the response headers, exactly
	}{
		{
			name:     "zero config sends exactly the three defaults and no HSTS",
			input:    middleware.SecureHeadersConfig{},
			expected: defaults,
		},
		{
			name: "every header value is overridable",
			input: middleware.SecureHeadersConfig{
				ContentTypeOptions:    "nosniff; custom",
				ReferrerPolicy:        "strict-origin-when-cross-origin",
				ContentSecurityPolicy: "default-src 'self'",
			},
			expected: map[string][]string{
				"X-Content-Type-Options":  {"nosniff; custom"},
				"Referrer-Policy":         {"strict-origin-when-cross-origin"},
				"Content-Security-Policy": {"default-src 'self'"},
			},
		},
		{
			name: "OmitHeader on one header drops only that header",
			input: middleware.SecureHeadersConfig{
				ContentSecurityPolicy: middleware.OmitHeader,
			},
			expected: map[string][]string{
				"X-Content-Type-Options": {"nosniff"},
				"Referrer-Policy":        {"no-referrer"},
			},
		},
		{
			name: "OmitHeader on every header sends none",
			input: middleware.SecureHeadersConfig{
				ContentTypeOptions:    middleware.OmitHeader,
				ReferrerPolicy:        middleware.OmitHeader,
				ContentSecurityPolicy: middleware.OmitHeader,
			},
			expected: map[string][]string{},
		},
		{
			name:     "HSTSMaxAge alone sends max-age in whole seconds",
			input:    middleware.SecureHeadersConfig{HSTSMaxAge: time.Hour + 500*time.Millisecond},
			expected: withHSTS("max-age=3600"),
		},
		{
			name: "HSTS two years with includeSubDomains",
			input: middleware.SecureHeadersConfig{
				HSTSMaxAge:            2 * 365 * 24 * time.Hour,
				HSTSIncludeSubDomains: true,
			},
			expected: withHSTS("max-age=63072000; includeSubDomains"),
		},
		{
			name: "HSTS with includeSubDomains and preload",
			input: middleware.SecureHeadersConfig{
				HSTSMaxAge:            2 * 365 * 24 * time.Hour,
				HSTSIncludeSubDomains: true,
				HSTSPreload:           true,
			},
			expected: withHSTS("max-age=63072000; includeSubDomains; preload"),
		},
		{
			name:     "HSTSMaxAge under one second sends no HSTS",
			input:    middleware.SecureHeadersConfig{HSTSMaxAge: 999 * time.Millisecond, HSTSIncludeSubDomains: true},
			expected: defaults,
		},
		{
			name:     "negative HSTSMaxAge sends no HSTS",
			input:    middleware.SecureHeadersConfig{HSTSMaxAge: -time.Hour},
			expected: defaults,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerRan := false
			h := middleware.SecureHeaders(tt.input)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				handlerRan = true
			}))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

			if !handlerRan {
				t.Error("next handler did not run")
			}
			if got := map[string][]string(rec.Header()); !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("header = %v, want %v", got, tt.expected)
			}
		})
	}

	// assertDefaults checks the three default headers are on a response.
	assertDefaults := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()

		for k, v := range defaults {
			if got := rec.Header().Values(k); !reflect.DeepEqual(got, v) {
				t.Errorf("%s = %v, want %v", k, got, v)
			}
		}
	}

	t.Run("headers are on routing 404 and 405 responses", func(t *testing.T) {
		s := httpx.New(httpx.Config{})
		s.Use(middleware.SecureHeaders(middleware.SecureHeadersConfig{}))
		s.Get("/orders", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

		for _, c := range []struct {
			method, path string
			status       int
		}{
			{method: http.MethodGet, path: "/missing", status: http.StatusNotFound},
			{method: http.MethodPost, path: "/orders", status: http.StatusMethodNotAllowed},
		} {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), c.method, c.path, nil))

			if rec.Code != c.status {
				t.Errorf("%s %s status = %d, want %d", c.method, c.path, rec.Code, c.status)
			}
			assertDefaults(t, rec)
		}
	})

	t.Run("headers are on the 500 Recover writes after a downstream panic", func(t *testing.T) {
		h := middleware.SecureHeaders(middleware.SecureHeadersConfig{})(
			middleware.Recover(middleware.RecoverConfig{Log: discardLogger()})(
				http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { panic("boom") }),
			),
		)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("Content-Type = %q, want application/problem+json", ct)
		}
		assertDefaults(t, rec)
	})

	t.Run("a handler may overwrite a header", func(t *testing.T) {
		h := middleware.SecureHeaders(middleware.SecureHeadersConfig{})(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Security-Policy", "default-src 'self'")
			}),
		)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

		if got := rec.Header().Values("Content-Security-Policy"); !reflect.DeepEqual(got, []string{"default-src 'self'"}) {
			t.Errorf("Content-Security-Policy = %v, want the handler's value", got)
		}
	})
}
