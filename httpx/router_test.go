package httpx_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// echo writes marker so a test can prove which handler ran.
func echo(marker string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, marker)
	}
}

// tag prepends marker to the response so middleware order shows in the body.
func tag(marker string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, marker)
			next.ServeHTTP(w, r)
		})
	}
}

type routeInput struct {
	register func(s *httpx.Server)
	method   string
	target   string
}

type routeExpected struct {
	status int
	body   string
}

func runRoute(t *testing.T, input routeInput) *httptest.ResponseRecorder {
	t.Helper()

	s := httpx.New(httpx.Config{})
	input.register(s)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), input.method, input.target, nil))

	return rec
}

func assertRoute(t *testing.T, rec *httptest.ResponseRecorder, expected routeExpected) {
	t.Helper()

	if rec.Code != expected.status {
		t.Errorf("status = %d, want %d", rec.Code, expected.status)
	}
	if expected.body != "" && rec.Body.String() != expected.body {
		t.Errorf("body = %q, want %q", rec.Body.String(), expected.body)
	}
}

func TestGroupGroup(t *testing.T) {
	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name: "child composes prefix onto parent",
			input: routeInput{
				register: func(s *httpx.Server) {
					s.Group("/api").Group("/v1").Get("/orders", echo("orders"))
				},
				method: http.MethodGet,
				target: "/api/v1/orders",
			},
			expected: routeExpected{status: http.StatusOK, body: "orders"},
		},
		{
			name: "middleware runs parent to child, outermost first",
			input: routeInput{
				register: func(s *httpx.Server) {
					s.Group("/p", tag("outer;")).Group("/c", tag("inner;")).Get("/x", echo("h"))
				},
				method: http.MethodGet,
				target: "/p/c/x",
			},
			expected: routeExpected{status: http.StatusOK, body: "outer;inner;h"},
		},
		{
			name: "sibling groups do not share middleware",
			input: routeInput{
				register: func(s *httpx.Server) {
					root := s.Group("/api")
					root.Group("/a", tag("A;")).Get("/x", echo("h"))
					root.Group("/b", tag("B;")).Get("/x", echo("h"))
				},
				method: http.MethodGet,
				target: "/api/a/x",
			},
			expected: routeExpected{status: http.StatusOK, body: "A;h"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}

func TestGroupGet(t *testing.T) {
	register := func(s *httpx.Server) { s.Get("/r", echo("get")) }

	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name:     "serves GET",
			input:    routeInput{register: register, method: http.MethodGet, target: "/r"},
			expected: routeExpected{status: http.StatusOK, body: "get"},
		},
		{
			name:     "other methods are 405",
			input:    routeInput{register: register, method: http.MethodPost, target: "/r"},
			expected: routeExpected{status: http.StatusMethodNotAllowed},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}

func TestGroupPost(t *testing.T) {
	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name: "serves POST",
			input: routeInput{
				register: func(s *httpx.Server) { s.Post("/r", echo("post")) },
				method:   http.MethodPost,
				target:   "/r",
			},
			expected: routeExpected{status: http.StatusOK, body: "post"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}

func TestGroupPut(t *testing.T) {
	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name: "serves PUT",
			input: routeInput{
				register: func(s *httpx.Server) { s.Put("/r", echo("put")) },
				method:   http.MethodPut,
				target:   "/r",
			},
			expected: routeExpected{status: http.StatusOK, body: "put"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}

func TestGroupPatch(t *testing.T) {
	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name: "serves PATCH",
			input: routeInput{
				register: func(s *httpx.Server) { s.Patch("/r", echo("patch")) },
				method:   http.MethodPatch,
				target:   "/r",
			},
			expected: routeExpected{status: http.StatusOK, body: "patch"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}

func TestGroupDelete(t *testing.T) {
	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name: "serves DELETE",
			input: routeInput{
				register: func(s *httpx.Server) { s.Delete("/r", echo("delete")) },
				method:   http.MethodDelete,
				target:   "/r",
			},
			expected: routeExpected{status: http.StatusOK, body: "delete"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}

func TestGroupHead(t *testing.T) {
	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name: "serves HEAD",
			input: routeInput{
				register: func(s *httpx.Server) { s.Head("/r", echo("")) },
				method:   http.MethodHead,
				target:   "/r",
			},
			expected: routeExpected{status: http.StatusOK},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}

func TestGroupOptions(t *testing.T) {
	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name: "serves OPTIONS",
			input: routeInput{
				register: func(s *httpx.Server) { s.Options("/r", echo("options")) },
				method:   http.MethodOptions,
				target:   "/r",
			},
			expected: routeExpected{status: http.StatusOK, body: "options"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}

func TestGroupQuery(t *testing.T) {
	register := func(s *httpx.Server) { s.Query("/search", echo("query")) }

	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name:     "serves QUERY",
			input:    routeInput{register: register, method: httpx.MethodQuery, target: "/search"},
			expected: routeExpected{status: http.StatusOK, body: "query"},
		},
		{
			name:     "GET on a QUERY route is 405",
			input:    routeInput{register: register, method: http.MethodGet, target: "/search"},
			expected: routeExpected{status: http.StatusMethodNotAllowed},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}

func TestGroupHandle(t *testing.T) {
	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name: "method token in the pattern is honored under the prefix",
			input: routeInput{
				register: func(s *httpx.Server) {
					s.Group("/dav").Handle("PROPFIND /files", echo("propfind"))
				},
				method: "PROPFIND",
				target: "/dav/files",
			},
			expected: routeExpected{status: http.StatusOK, body: "propfind"},
		},
		{
			name: "extra whitespace after the method token parses like stdlib",
			input: routeInput{
				register: func(s *httpx.Server) {
					s.Group("/api").Handle("GET  /x", echo("spaced"))
				},
				method: http.MethodGet,
				target: "/api/x",
			},
			expected: routeExpected{status: http.StatusOK, body: "spaced"},
		},
		{
			name: "tab separator parses like stdlib",
			input: routeInput{
				register: func(s *httpx.Server) {
					s.Group("/api").Handle("GET\t/x", echo("tabbed"))
				},
				method: http.MethodGet,
				target: "/api/x",
			},
			expected: routeExpected{status: http.StatusOK, body: "tabbed"},
		},
		{
			name: "pattern without a method matches any method",
			input: routeInput{
				register: func(s *httpx.Server) { s.Handle("/any", echo("any")) },
				method:   http.MethodDelete,
				target:   "/any",
			},
			expected: routeExpected{status: http.StatusOK, body: "any"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}

	t.Run("duplicate pattern panics at registration, not request time", func(t *testing.T) {
		s := httpx.New(httpx.Config{})
		s.Get("/dup", echo("a"))

		defer func() {
			if recover() == nil {
				t.Error("second registration of the same pattern did not panic")
			}
		}()
		s.Get("/dup", echo("b"))
	})
}

func TestGroupHandleFunc(t *testing.T) {
	tests := []struct {
		name     string
		input    routeInput
		expected routeExpected
	}{
		{
			name: "mirrors Handle for handler funcs",
			input: routeInput{
				register: func(s *httpx.Server) {
					s.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
						_, _ = io.WriteString(w, r.PathValue("id")) //nolint:gosec // test echoes a path value to assert routing; no browser involved
					})
				},
				method: http.MethodGet,
				target: "/orders/ord_42",
			},
			expected: routeExpected{status: http.StatusOK, body: "ord_42"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRoute(t, runRoute(t, tt.input), tt.expected)
		})
	}
}
