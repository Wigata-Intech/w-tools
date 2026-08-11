package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

var hexRe = regexp.MustCompile(`^[0-9a-f]+$`)

func TestLogger(t *testing.T) {
	type loggerInput struct {
		cfg         middleware.LoggerConfig // Log is filled by the runner
		withServer  bool                    // route through httpx.Server so r.Pattern is set
		withIDs     bool                    // compose RequestID and Trace upstream
		method      string
		target      string
		contentType string
		body        string
		remoteAddr  string
		handler     http.HandlerFunc // nil = default: echo body, 200 "ok"
	}

	type loggerExpected struct {
		attrs       map[string]any // exact-match subset of the log line
		present     []string       // keys that must exist
		absent      []string       // keys that must not exist
		hexAttrs    map[string]int // key -> required hex length
		handlerBody string         // when set, the handler must have received exactly this body
	}

	tests := []struct {
		name     string
		input    loggerInput
		expected loggerExpected
	}{
		{
			name: "access line carries the request fundamentals",
			input: loggerInput{
				withServer: true,
				method:     http.MethodGet,
				target:     "/orders/42",
			},
			expected: loggerExpected{
				attrs: map[string]any{
					"msg":     "request",
					"method":  "GET",
					"path":    "/orders/42",
					"pattern": "GET /orders/{id}",
					"status":  float64(200),
				},
				present: []string{"bytes", "duration_ms", "remote_ip"},
				absent:  []string{"request_id", "trace_id", "request_body_size", "response_body_size"},
			},
		},
		{
			name: "explicit status and empty response recorded",
			input: loggerInput{
				method: http.MethodGet,
				target: "/x",
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				},
			},
			expected: loggerExpected{
				attrs:  map[string]any{"status": float64(404), "bytes": float64(0)},
				absent: []string{"pattern"},
			},
		},
		{
			name: "informational status never becomes the access-line status",
			input: loggerInput{
				method: http.MethodGet,
				target: "/x",
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusEarlyHints)
					_, _ = io.WriteString(w, "ok")
				},
			},
			expected: loggerExpected{
				attrs: map[string]any{"status": float64(200)},
			},
		},
		{
			name: "request and trace IDs ride the line",
			input: loggerInput{
				withIDs: true,
				method:  http.MethodGet,
				target:  "/x",
			},
			expected: loggerExpected{
				hexAttrs: map[string]int{"request_id": 32, "trace_id": 32},
			},
		},
		{
			name: "unparseable RemoteAddr logs verbatim",
			input: loggerInput{
				method:     http.MethodGet,
				target:     "/x",
				remoteAddr: "badaddr",
			},
			expected: loggerExpected{
				attrs: map[string]any{"remote_ip": "badaddr"},
			},
		},
		{
			name: "JSON request body becomes a structured attr",
			input: loggerInput{
				cfg:         middleware.LoggerConfig{LogRequestBody: true},
				method:      http.MethodPost,
				target:      "/x",
				contentType: "application/json",
				body:        `{"user":"dhira","password":"hunter2"}`,
			},
			expected: loggerExpected{
				attrs: map[string]any{
					"request_body_size": float64(37),
					"request_body":      map[string]any{"user": "dhira", "password": "hunter2"},
				},
				handlerBody: `{"user":"dhira","password":"hunter2"}`,
			},
		},
		{
			name: "empty request body logs size zero",
			input: loggerInput{
				cfg:         middleware.LoggerConfig{LogRequestBody: true},
				method:      http.MethodPost,
				target:      "/x",
				contentType: "application/json",
			},
			expected: loggerExpected{
				attrs:  map[string]any{"request_body_size": float64(0)},
				absent: []string{"request_body"},
			},
		},
		{
			name: "truncated request body marks and never parses, handler still reads it whole",
			input: loggerInput{
				cfg:         middleware.LoggerConfig{LogRequestBody: true, MaxBody: 8},
				method:      http.MethodPost,
				target:      "/x",
				contentType: "application/json",
				body:        `{"user":"dhira","password":"hunter2"}`,
			},
			expected: loggerExpected{
				attrs: map[string]any{
					"request_body_size":      float64(8),
					"request_body_truncated": true,
				},
				absent:      []string{"request_body"},
				handlerBody: `{"user":"dhira","password":"hunter2"}`,
			},
		},
		{
			name: "non-JSON request body logs size only",
			input: loggerInput{
				cfg:         middleware.LoggerConfig{LogRequestBody: true},
				method:      http.MethodPost,
				target:      "/x",
				contentType: "text/plain",
				body:        "secret raw text",
			},
			expected: loggerExpected{
				attrs:  map[string]any{"request_body_size": float64(15)},
				absent: []string{"request_body"},
			},
		},
		{
			name: "malformed JSON request body logs size only",
			input: loggerInput{
				cfg:         middleware.LoggerConfig{LogRequestBody: true},
				method:      http.MethodPost,
				target:      "/x",
				contentType: "application/json",
				body:        `{broken`,
			},
			expected: loggerExpected{
				attrs:  map[string]any{"request_body_size": float64(7)},
				absent: []string{"request_body"},
			},
		},
		{
			name: "JSON response body becomes a structured attr",
			input: loggerInput{
				cfg:    middleware.LoggerConfig{LogResponseBody: true},
				method: http.MethodGet,
				target: "/x",
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"ok":true}`)
				},
			},
			expected: loggerExpected{
				attrs: map[string]any{
					"response_body_size": float64(11),
					"response_body":      map[string]any{"ok": true},
				},
			},
		},
		{
			name: "truncated response body marks and never parses, client still gets it whole",
			input: loggerInput{
				cfg:    middleware.LoggerConfig{LogResponseBody: true, MaxBody: 4},
				method: http.MethodGet,
				target: "/x",
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"ok":true}`)
				},
			},
			expected: loggerExpected{
				attrs: map[string]any{
					"response_body_size":      float64(4),
					"response_body_truncated": true,
				},
				absent: []string{"response_body"},
			},
		},
		{
			name: "non-JSON response body logs size only",
			input: loggerInput{
				cfg:    middleware.LoggerConfig{LogResponseBody: true},
				method: http.MethodGet,
				target: "/x",
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					_, _ = io.WriteString(w, "<html>secret</html>")
				},
			},
			expected: loggerExpected{
				attrs:  map[string]any{"response_body_size": float64(19)},
				absent: []string{"response_body"},
			},
		},
		{
			name: "capture is off by default",
			input: loggerInput{
				method:      http.MethodPost,
				target:      "/x",
				contentType: "application/json",
				body:        `{"password":"hunter2"}`,
			},
			expected: loggerExpected{
				absent: []string{"request_body", "request_body_size", "response_body", "response_body_size"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, buf := logCapture()
			tt.input.cfg.Log = log

			var gotBody string
			handler := tt.input.handler
			if handler == nil {
				handler = func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					gotBody = string(b)
					_, _ = io.WriteString(w, "ok")
				}
			}

			var h http.Handler
			switch {
			case tt.input.withServer:
				s := httpx.New(httpx.Config{})
				s.Use(middleware.Logger(tt.input.cfg))
				s.Get("/orders/{id}", handler)
				h = s
			case tt.input.withIDs:
				h = middleware.RequestID(middleware.RequestIDConfig{})(
					middleware.Trace()(
						middleware.Logger(tt.input.cfg)(handler)))
			default:
				h = middleware.Logger(tt.input.cfg)(handler)
			}

			var reqBody io.Reader
			if tt.input.body != "" {
				reqBody = strings.NewReader(tt.input.body)
			}
			req := httptest.NewRequestWithContext(context.Background(), tt.input.method, tt.input.target, reqBody)
			if tt.input.contentType != "" {
				req.Header.Set("Content-Type", tt.input.contentType)
			}
			if tt.input.remoteAddr != "" {
				req.RemoteAddr = tt.input.remoteAddr
			}

			h.ServeHTTP(httptest.NewRecorder(), req)

			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
			}

			for key, want := range tt.expected.attrs {
				got, ok := line[key]
				if !ok {
					t.Errorf("attr %q missing from %v", key, line)
					continue
				}
				if m, isMap := want.(map[string]any); isMap {
					gm, _ := got.(map[string]any)
					for mk, mv := range m {
						if gm[mk] != mv {
							t.Errorf("attr %q[%q] = %v, want %v", key, mk, gm[mk], mv)
						}
					}
					continue
				}
				if got != want {
					t.Errorf("attr %q = %v, want %v", key, got, want)
				}
			}
			for _, key := range tt.expected.present {
				if _, ok := line[key]; !ok {
					t.Errorf("attr %q missing from %v", key, line)
				}
			}
			for _, key := range tt.expected.absent {
				if _, ok := line[key]; ok {
					t.Errorf("attr %q unexpectedly present: %v", key, line[key])
				}
			}
			for key, hexLen := range tt.expected.hexAttrs {
				s, _ := line[key].(string)
				if len(s) != hexLen || !hexRe.MatchString(s) {
					t.Errorf("attr %q = %q, want %d-char hex", key, s, hexLen)
				}
			}
			if tt.expected.handlerBody != "" && gotBody != tt.expected.handlerBody {
				t.Errorf("handler received body %q, want %q", gotBody, tt.expected.handlerBody)
			}
		})
	}

	t.Run("nil Log falls back to slog.Default", func(t *testing.T) {
		buf := new(bytes.Buffer)
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
		t.Cleanup(func() { slog.SetDefault(prev) })

		h := middleware.Logger(middleware.LoggerConfig{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil))

		if !strings.Contains(buf.String(), `"msg":"request"`) {
			t.Errorf("default log %q missing the access line", buf.String())
		}
	})

	t.Run("Unwrap keeps ResponseController flush working", func(t *testing.T) {
		log, _ := logCapture()

		h := middleware.Logger(middleware.LoggerConfig{Log: log})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	t.Run("safe under concurrent requests sharing one pool", func(_ *testing.T) {
		log, _ := logCapture()

		h := middleware.Logger(middleware.LoggerConfig{
			Log:             log,
			LogRequestBody:  true,
			LogResponseBody: true,
			MaxBody:         64,
		})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		}))

		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", strings.NewReader(`{"n":1}`))
				req.Header.Set("Content-Type", "application/json")
				h.ServeHTTP(httptest.NewRecorder(), req)
			}()
		}
		wg.Wait()
	})
}
