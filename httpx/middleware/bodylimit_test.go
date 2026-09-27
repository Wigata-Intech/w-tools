package middleware_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

func TestBodyLimit(t *testing.T) {
	type bodyLimitInput struct {
		cfg     middleware.BodyLimitConfig
		size    int64 // body bytes sent
		chunked bool  // no declared Content-Length
	}

	type bodyLimitExpected struct {
		status      int
		body        string // exact match when set
		contentType string // checked when set
		handlerRan  bool
		readLimit   int64 // the handler's read failed with *http.MaxBytesError of this Limit; 0 = read succeeded
	}

	// customErrorWriter writes the status and detail it receives as plain text.
	customErrorWriter := func(w http.ResponseWriter, _ *http.Request, status int, detail string) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, detail)
	}

	tests := []struct {
		name     string
		input    bodyLimitInput
		expected bodyLimitExpected
	}{
		{
			name:     "zero Max defaults to httpx.DefaultMaxBind: a body of exactly that size passes",
			input:    bodyLimitInput{size: httpx.DefaultMaxBind},
			expected: bodyLimitExpected{status: http.StatusOK, handlerRan: true},
		},
		{
			name:     "negative Max defaults to httpx.DefaultMaxBind: one byte more is 413",
			input:    bodyLimitInput{cfg: middleware.BodyLimitConfig{Max: -1}, size: httpx.DefaultMaxBind + 1},
			expected: bodyLimitExpected{status: http.StatusRequestEntityTooLarge},
		},
		{
			name:     "Content-Length equal to Max passes",
			input:    bodyLimitInput{cfg: middleware.BodyLimitConfig{Max: 10}, size: 10},
			expected: bodyLimitExpected{status: http.StatusOK, handlerRan: true},
		},
		{
			name:     "chunked body within Max passes",
			input:    bodyLimitInput{cfg: middleware.BodyLimitConfig{Max: 10}, size: 10, chunked: true},
			expected: bodyLimitExpected{status: http.StatusOK, handlerRan: true},
		},
		{
			name:  "Content-Length over Max is a 413 Problem and next never runs",
			input: bodyLimitInput{cfg: middleware.BodyLimitConfig{Max: 10}, size: 11},
			expected: bodyLimitExpected{
				status:      http.StatusRequestEntityTooLarge,
				body:        `{"type":"about:blank","title":"Request Entity Too Large","status":413,"detail":"request body too large"}`,
				contentType: "application/problem+json",
			},
		},
		{
			name: "custom ErrorWriter receives 413 and the stable detail",
			input: bodyLimitInput{
				cfg:  middleware.BodyLimitConfig{Max: 10, ErrorWriter: customErrorWriter},
				size: 11,
			},
			expected: bodyLimitExpected{
				status:      http.StatusRequestEntityTooLarge,
				body:        "request body too large",
				contentType: "text/plain",
			},
		},
		{
			name:     "chunked body over Max fails the handler's read with *http.MaxBytesError",
			input:    bodyLimitInput{cfg: middleware.BodyLimitConfig{Max: 10}, size: 11, chunked: true},
			expected: bodyLimitExpected{status: http.StatusRequestEntityTooLarge, handlerRan: true, readLimit: 10},
		},
		{
			name:     "chunked body over the default cap fails at httpx.DefaultMaxBind",
			input:    bodyLimitInput{size: httpx.DefaultMaxBind + 1, chunked: true},
			expected: bodyLimitExpected{status: http.StatusRequestEntityTooLarge, handlerRan: true, readLimit: httpx.DefaultMaxBind},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerRan := false
			var readLimit int64

			h := middleware.BodyLimit(tt.input.cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerRan = true

				if _, err := io.ReadAll(r.Body); err != nil {
					var mbe *http.MaxBytesError
					if !errors.As(err, &mbe) {
						t.Fatalf("read error = %v, want *http.MaxBytesError", err)
					}
					readLimit = mbe.Limit
					w.WriteHeader(http.StatusRequestEntityTooLarge)

					return
				}

				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(strings.Repeat("a", int(tt.input.size))))
			if tt.input.chunked {
				req.ContentLength = -1
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.expected.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.expected.status)
			}
			if handlerRan != tt.expected.handlerRan {
				t.Errorf("handlerRan = %t, want %t", handlerRan, tt.expected.handlerRan)
			}
			if readLimit != tt.expected.readLimit {
				t.Errorf("MaxBytesError.Limit = %d, want %d", readLimit, tt.expected.readLimit)
			}
			if tt.expected.body != "" && rec.Body.String() != tt.expected.body {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.expected.body)
			}
			if ct := rec.Header().Get("Content-Type"); tt.expected.contentType != "" && ct != tt.expected.contentType {
				t.Errorf("Content-Type = %q, want %q", ct, tt.expected.contentType)
			}
		})
	}

	t.Run("as outer middleware the 413 wins over routing 404 and 405", func(t *testing.T) {
		s := httpx.New(httpx.Config{})
		s.Use(middleware.BodyLimit(middleware.BodyLimitConfig{Max: 10}))
		s.Get("/orders", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

		for _, path := range []string{"/missing", "/orders"} {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(strings.Repeat("a", 11))))

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("POST %s status = %d, want 413", path, rec.Code)
			}
		}
	})
}
