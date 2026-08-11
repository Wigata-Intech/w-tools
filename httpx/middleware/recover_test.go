package middleware_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

func logCapture() (*slog.Logger, *bytes.Buffer) {
	buf := new(bytes.Buffer)

	return slog.New(slog.NewJSONHandler(buf, nil)), buf
}

func TestRecover(t *testing.T) {
	defaultBuf := new(bytes.Buffer)

	type recoverInput struct {
		nilLog      bool
		errorWriter httpx.ErrorWriter
		handler     http.HandlerFunc
	}

	type recoverExpected struct {
		status      int
		body        string // exact match when set
		contentType string
		logContains []string
		logEmpty    bool
		logDefault  bool // panic record lands on slog.Default instead
		repanic     bool
	}

	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    recoverInput
		expected recoverExpected
	}{
		{
			name: "healthy handler passes through untouched",
			input: recoverInput{
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, "created")
				},
			},
			expected: recoverExpected{
				status:   http.StatusCreated,
				body:     "created",
				logEmpty: true,
			},
		},
		{
			name: "panic becomes a logged RFC 9457 500",
			input: recoverInput{
				handler: func(_ http.ResponseWriter, _ *http.Request) {
					panic("boom")
				},
			},
			expected: recoverExpected{
				status:      http.StatusInternalServerError,
				contentType: "application/problem+json",
				logContains: []string{"panic recovered", "boom", "stack", "/boom"},
			},
		},
		{
			name: "panic after headers sent writes no second response",
			input: recoverInput{
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, "partial")
					panic("late boom")
				},
			},
			expected: recoverExpected{
				status:      http.StatusOK,
				body:        "partial",
				logContains: []string{"late boom"},
			},
		},
		{
			name: "1xx header does not count as sent; the 500 still goes out",
			input: recoverInput{
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusEarlyHints)
					panic("boom after hints")
				},
			},
			expected: recoverExpected{
				status:      http.StatusEarlyHints, // the recorder latches the first code; the problem body below proves the 500 was written
				contentType: "application/problem+json",
				body:        `{"type":"about:blank","title":"Internal Server Error","status":500,"detail":"internal error"}`,
				logContains: []string{"boom after hints"},
			},
		},
		{
			name: "custom ErrorWriter formats the 500",
			input: recoverInput{
				errorWriter: func(w http.ResponseWriter, _ *http.Request, status int, _ string) {
					w.Header().Set("Content-Type", "text/plain")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "custom-error")
				},
				handler: func(_ http.ResponseWriter, _ *http.Request) {
					panic("boom")
				},
			},
			expected: recoverExpected{
				status:      http.StatusInternalServerError,
				body:        "custom-error",
				contentType: "text/plain",
				logContains: []string{"boom"},
			},
		},
		{
			name: "nil Log falls back to slog.Default",
			mockFunc: func(t *testing.T) {
				t.Helper()

				prev := slog.Default()
				slog.SetDefault(slog.New(slog.NewJSONHandler(defaultBuf, nil)))
				t.Cleanup(func() { slog.SetDefault(prev) })
			},
			input: recoverInput{
				nilLog: true,
				handler: func(_ http.ResponseWriter, _ *http.Request) {
					panic("default boom")
				},
			},
			expected: recoverExpected{
				status:     http.StatusInternalServerError,
				logDefault: true,
			},
		},
		{
			name: "http.ErrAbortHandler re-panics unlogged",
			input: recoverInput{
				handler: func(_ http.ResponseWriter, _ *http.Request) {
					panic(http.ErrAbortHandler)
				},
			},
			expected: recoverExpected{
				status:   http.StatusOK, // recorder default; nothing was written
				logEmpty: true,
				repanic:  true,
			},
		},
		{
			name: "a wrapped abort sentinel is a normal panic — identity only, matching net/http",
			input: recoverInput{
				handler: func(_ http.ResponseWriter, _ *http.Request) {
					panic(fmt.Errorf("wrapping: %w", http.ErrAbortHandler))
				},
			},
			expected: recoverExpected{
				status:      http.StatusInternalServerError,
				logContains: []string{"wrapping"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFunc != nil {
				tt.mockFunc(t)
			}

			log, buf := logCapture()

			cfg := middleware.RecoverConfig{ErrorWriter: tt.input.errorWriter}
			if !tt.input.nilLog {
				cfg.Log = log
			}

			h := middleware.Recover(cfg)(tt.input.handler)

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil)

			var repanicked any

			func() {
				defer func() { repanicked = recover() }()
				h.ServeHTTP(rec, req)
			}()

			if (repanicked != nil) != tt.expected.repanic {
				t.Errorf("repanic = %v, want %t", repanicked, tt.expected.repanic)
			}
			if rec.Code != tt.expected.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.expected.status)
			}
			if tt.expected.body != "" && rec.Body.String() != tt.expected.body {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.expected.body)
			}
			if tt.expected.contentType != "" && rec.Header().Get("Content-Type") != tt.expected.contentType {
				t.Errorf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), tt.expected.contentType)
			}
			if tt.expected.logEmpty && buf.Len() != 0 {
				t.Errorf("log = %q, want empty", buf.String())
			}
			for _, want := range tt.expected.logContains {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("log %q does not contain %q", buf.String(), want)
				}
			}
			if tt.expected.logDefault && !strings.Contains(defaultBuf.String(), "panic recovered") {
				t.Errorf("default log %q does not contain the panic record", defaultBuf.String())
			}
		})
	}

	t.Run("Unwrap keeps ResponseController flush working", func(t *testing.T) {
		log, _ := logCapture()

		h := middleware.Recover(middleware.RecoverConfig{Log: log})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("Flush() = %v, want nil", err)
			}
		}))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil))

		if !rec.Flushed {
			t.Error("flush did not reach the underlying writer")
		}
	})
}
