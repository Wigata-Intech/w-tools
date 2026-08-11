package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

func TestCORS(t *testing.T) {
	type corsInput struct {
		cfg     middleware.CORSConfig
		method  string
		headers map[string]string
	}

	type corsExpected struct {
		status     int
		handlerRan bool
		header     map[string][]string // the response headers, exactly
	}

	tests := []struct {
		name     string
		input    corsInput
		expected corsExpected
	}{
		{
			name: "no Origin header is not CORS but still varies by Origin for caches",
			input: corsInput{
				cfg:    middleware.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}},
				method: http.MethodGet,
			},
			expected: corsExpected{
				status:     http.StatusOK,
				handlerRan: true,
				header:     map[string][]string{"Vary": {"Origin"}},
			},
		},
		{
			name: "actual request from allowed origin gets the origin echoed",
			input: corsInput{
				cfg:     middleware.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}},
				method:  http.MethodGet,
				headers: map[string]string{"Origin": "https://app.example.com"},
			},
			expected: corsExpected{
				status:     http.StatusOK,
				handlerRan: true,
				header: map[string][]string{
					"Vary":                        {"Origin"},
					"Access-Control-Allow-Origin": {"https://app.example.com"},
				},
			},
		},
		{
			name: "wildcard config without credentials sends the literal star",
			input: corsInput{
				cfg:     middleware.CORSConfig{AllowedOrigins: []string{"*"}},
				method:  http.MethodGet,
				headers: map[string]string{"Origin": "https://anywhere.example.com"},
			},
			expected: corsExpected{
				status:     http.StatusOK,
				handlerRan: true,
				header: map[string][]string{
					"Vary":                        {"Origin"},
					"Access-Control-Allow-Origin": {"*"},
				},
			},
		},
		{
			name: "credentials echo the origin and set the credentials header",
			input: corsInput{
				cfg: middleware.CORSConfig{
					AllowedOrigins:   []string{"https://app.example.com"},
					AllowCredentials: true,
				},
				method:  http.MethodGet,
				headers: map[string]string{"Origin": "https://app.example.com"},
			},
			expected: corsExpected{
				status:     http.StatusOK,
				handlerRan: true,
				header: map[string][]string{
					"Vary":                             {"Origin"},
					"Access-Control-Allow-Origin":      {"https://app.example.com"},
					"Access-Control-Allow-Credentials": {"true"},
				},
			},
		},
		{
			name: "ExposedHeaders are joined onto the actual response",
			input: corsInput{
				cfg: middleware.CORSConfig{
					AllowedOrigins: []string{"https://app.example.com"},
					ExposedHeaders: []string{"X-Total-Count", "X-Request-ID"},
				},
				method:  http.MethodGet,
				headers: map[string]string{"Origin": "https://app.example.com"},
			},
			expected: corsExpected{
				status:     http.StatusOK,
				handlerRan: true,
				header: map[string][]string{
					"Vary":                          {"Origin"},
					"Access-Control-Allow-Origin":   {"https://app.example.com"},
					"Access-Control-Expose-Headers": {"X-Total-Count, X-Request-ID"},
				},
			},
		},
		{
			name: "allowed preflight short-circuits 204 with the full header set",
			input: corsInput{
				cfg: middleware.CORSConfig{
					AllowedOrigins: []string{"https://app.example.com"},
					MaxAge:         10 * time.Minute,
				},
				method: http.MethodOptions,
				headers: map[string]string{
					"Origin":                         "https://app.example.com",
					"Access-Control-Request-Method":  http.MethodPost,
					"Access-Control-Request-Headers": "content-type, x-anything",
				},
			},
			expected: corsExpected{
				status: http.StatusNoContent,
				header: map[string][]string{
					"Vary":                         {"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"},
					"Access-Control-Allow-Origin":  {"https://app.example.com"},
					"Access-Control-Allow-Methods": {"GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS, QUERY"},
					"Access-Control-Allow-Headers": {"content-type, x-anything"},
					"Access-Control-Max-Age":       {"600"},
				},
			},
		},
		{
			name: "configured AllowedHeaders win over the requested ones",
			input: corsInput{
				cfg: middleware.CORSConfig{
					AllowedOrigins:   []string{"https://app.example.com"},
					AllowedHeaders:   []string{"Content-Type", "Authorization"},
					AllowCredentials: true,
				},
				method: http.MethodOptions,
				headers: map[string]string{
					"Origin":                         "https://app.example.com",
					"Access-Control-Request-Method":  http.MethodPost,
					"Access-Control-Request-Headers": "x-ignored",
				},
			},
			expected: corsExpected{
				status: http.StatusNoContent,
				header: map[string][]string{
					"Vary":                             {"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"},
					"Access-Control-Allow-Origin":      {"https://app.example.com"},
					"Access-Control-Allow-Methods":     {"GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS, QUERY"},
					"Access-Control-Allow-Headers":     {"Content-Type, Authorization"},
					"Access-Control-Allow-Credentials": {"true"},
				},
			},
		},
		{
			name: "QUERY preflight is allowed under the default method list",
			input: corsInput{
				cfg:    middleware.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}},
				method: http.MethodOptions,
				headers: map[string]string{
					"Origin":                        "https://app.example.com",
					"Access-Control-Request-Method": "QUERY",
				},
			},
			expected: corsExpected{
				status: http.StatusNoContent,
				header: map[string][]string{
					"Vary":                         {"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"},
					"Access-Control-Allow-Origin":  {"https://app.example.com"},
					"Access-Control-Allow-Methods": {"GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS, QUERY"},
				},
			},
		},
		{
			name: "OPTIONS without a requested method is an actual request, not a preflight",
			input: corsInput{
				cfg:     middleware.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}},
				method:  http.MethodOptions,
				headers: map[string]string{"Origin": "https://app.example.com"},
			},
			expected: corsExpected{
				status:     http.StatusOK,
				handlerRan: true,
				header: map[string][]string{
					"Vary":                        {"Origin"},
					"Access-Control-Allow-Origin": {"https://app.example.com"},
				},
			},
		},
		{
			name: "preflight for a method outside AllowedMethods answers 204 with only Vary",
			input: corsInput{
				cfg: middleware.CORSConfig{
					AllowedOrigins: []string{"https://app.example.com"},
					AllowedMethods: []string{http.MethodGet, http.MethodPost},
				},
				method: http.MethodOptions,
				headers: map[string]string{
					"Origin":                        "https://app.example.com",
					"Access-Control-Request-Method": http.MethodDelete,
				},
			},
			expected: corsExpected{
				status: http.StatusNoContent,
				header: map[string][]string{
					"Vary": {"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"},
				},
			},
		},
		{
			name: "preflight from a disallowed origin answers 204 with only Vary",
			input: corsInput{
				cfg:    middleware.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}},
				method: http.MethodOptions,
				headers: map[string]string{
					"Origin":                        "https://evil.example.com",
					"Access-Control-Request-Method": http.MethodGet,
				},
			},
			expected: corsExpected{
				status: http.StatusNoContent,
				header: map[string][]string{
					"Vary": {"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"},
				},
			},
		},
		{
			name: "actual request from a disallowed origin still runs, with only Vary",
			input: corsInput{
				cfg:     middleware.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}},
				method:  http.MethodGet,
				headers: map[string]string{"Origin": "https://evil.example.com"},
			},
			expected: corsExpected{
				status:     http.StatusOK,
				handlerRan: true,
				header: map[string][]string{
					"Vary": {"Origin"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerRan := false

			h := middleware.CORS(tt.input.cfg)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				handlerRan = true
			}))

			req := httptest.NewRequestWithContext(context.Background(), tt.input.method, "/resource", nil)
			for k, v := range tt.input.headers {
				req.Header.Set(k, v)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.expected.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.expected.status)
			}
			if handlerRan != tt.expected.handlerRan {
				t.Errorf("handlerRan = %t, want %t", handlerRan, tt.expected.handlerRan)
			}
			if got := map[string][]string(rec.Header()); !reflect.DeepEqual(got, tt.expected.header) {
				t.Errorf("header = %v, want %v", got, tt.expected.header)
			}
		})
	}

	t.Run("wildcard origin with credentials panics at construction", func(t *testing.T) {
		defer func() {
			const want = "middleware: CORS wildcard origin cannot be combined with credentials"
			if p := recover(); p != want {
				t.Errorf("panic = %v, want %q", p, want)
			}
		}()

		middleware.CORS(middleware.CORSConfig{
			AllowedOrigins:   []string{"*"},
			AllowCredentials: true,
		})
	})
}
