package httpx_test

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// reserveAddr grabs a free loopback port and releases it for the server
// under test. Tiny reuse race, standard for testing Run-style APIs.
func reserveAddr(t *testing.T) string {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	return addr
}

// get retries until the server under test is listening, then returns the
// response body. Meant to be called from a goroutine; failures surface as
// an error string through the channel-reading caller.
func get(ctx context.Context, addr, path string) string {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
		if err != nil {
			return "request: " + err.Error()
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			select {
			case <-ctx.Done():
				return "ctx: " + ctx.Err().Error()
			case <-time.After(5 * time.Millisecond):
				continue
			}
		}

		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return "read: " + err.Error()
		}

		return string(b)
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		input    httpx.Config
		expected *http.Server
	}{
		{
			name:  "zero config gets production defaults",
			input: httpx.Config{Addr: ":8080"},
			expected: &http.Server{
				Addr:              ":8080",
				ReadHeaderTimeout: httpx.DefaultReadHeaderTimeout,
				ReadTimeout:       httpx.DefaultReadTimeout,
				WriteTimeout:      httpx.DefaultWriteTimeout,
				IdleTimeout:       httpx.DefaultIdleTimeout,
				MaxHeaderBytes:    httpx.DefaultMaxHeaderBytes,
			},
		},
		{
			name: "explicit config is respected",
			input: httpx.Config{
				Addr:              ":9090",
				ReadHeaderTimeout: 1 * time.Second,
				ReadTimeout:       2 * time.Second,
				WriteTimeout:      3 * time.Second,
				IdleTimeout:       4 * time.Second,
				MaxHeaderBytes:    512,
				ShutdownGrace:     5 * time.Second,
			},
			expected: &http.Server{
				Addr:              ":9090",
				ReadHeaderTimeout: 1 * time.Second,
				ReadTimeout:       2 * time.Second,
				WriteTimeout:      3 * time.Second,
				IdleTimeout:       4 * time.Second,
				MaxHeaderBytes:    512,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httpx.New(tt.input).HTTPServer()

			if srv.Addr != tt.expected.Addr {
				t.Errorf("Addr = %q, want %q", srv.Addr, tt.expected.Addr)
			}
			if srv.ReadHeaderTimeout != tt.expected.ReadHeaderTimeout {
				t.Errorf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, tt.expected.ReadHeaderTimeout)
			}
			if srv.ReadTimeout != tt.expected.ReadTimeout {
				t.Errorf("ReadTimeout = %v, want %v", srv.ReadTimeout, tt.expected.ReadTimeout)
			}
			if srv.WriteTimeout != tt.expected.WriteTimeout {
				t.Errorf("WriteTimeout = %v, want %v", srv.WriteTimeout, tt.expected.WriteTimeout)
			}
			if srv.IdleTimeout != tt.expected.IdleTimeout {
				t.Errorf("IdleTimeout = %v, want %v", srv.IdleTimeout, tt.expected.IdleTimeout)
			}
			if srv.MaxHeaderBytes != tt.expected.MaxHeaderBytes {
				t.Errorf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, tt.expected.MaxHeaderBytes)
			}
		})
	}
}

func TestServerUse(t *testing.T) {
	mark := func(name string, log *[]string) httpx.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				*log = append(*log, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	tests := []struct {
		name     string
		input    string // request target
		expected []string
	}{
		{
			name:     "wraps matched routes in registration order",
			input:    "/hit",
			expected: []string{"first", "second", "handler"},
		},
		{
			name:     "wraps unmatched requests too",
			input:    "/missing",
			expected: []string{"first", "second"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log []string

			s := httpx.New(httpx.Config{})
			s.Get("/hit", func(_ http.ResponseWriter, _ *http.Request) {
				log = append(log, "handler")
			})
			s.Use(mark("first", &log), mark("second", &log))

			s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.input, nil))

			if !slices.Equal(log, tt.expected) {
				t.Errorf("execution order = %v, want %v", log, tt.expected)
			}
		})
	}

	t.Run("panics once the server started serving", func(t *testing.T) {
		s := httpx.New(httpx.Config{})
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

		defer func() {
			if recover() == nil {
				t.Error("Use after first request did not panic")
			}
		}()
		s.Use(func(next http.Handler) http.Handler { return next })
	})

	t.Run("racing Use and first request is defined behavior", func(_ *testing.T) {
		s := httpx.New(httpx.Config{})
		s.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			defer func() { _ = recover() }() // losing the race panics — that's the contract
			s.Use(func(next http.Handler) http.Handler { return next })
		}()
		go func() {
			defer wg.Done()
			s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
		}()
		wg.Wait()
	})
}

// recordingErrorWriter is an ErrorWriter that writes the default Problem
// and records every status it was handed.
type recordingErrorWriter struct {
	mu       sync.Mutex
	statuses []int
}

func (e *recordingErrorWriter) write(w http.ResponseWriter, _ *http.Request, status int, detail string) {
	e.mu.Lock()
	e.statuses = append(e.statuses, status)
	e.mu.Unlock()

	httpx.Error(w, status, detail)
}

func (e *recordingErrorWriter) got() []int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return slices.Clone(e.statuses)
}

// newServeHTTPServer is the server TestServerServeHTTP drives: a GET-only
// /ok, a /orders/{id} echoing its path value, and a /gone whose handler
// writes its own plain-text 404.
func newServeHTTPServer(errorWriter httpx.ErrorWriter) *httpx.Server {
	s := httpx.New(httpx.Config{ErrorWriter: errorWriter})
	s.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s.Get("/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"pattern": r.Pattern, "id": r.PathValue("id")})
	})
	s.Get("/gone", http.NotFound)

	return s
}

func TestServerServeHTTP(t *testing.T) {
	type serveInput struct {
		errorWriter bool // route unmatched requests through a recordingErrorWriter
		method      string
		target      string
	}

	type serveExpected struct {
		status  int
		header  http.Header // the complete response header
		body    string
		written []int // statuses the ErrorWriter was handed
	}

	stdlibPlainText := func(allow string) http.Header {
		h := http.Header{
			"Content-Type":           {"text/plain; charset=utf-8"},
			"X-Content-Type-Options": {"nosniff"},
		}
		if allow != "" {
			h.Set("Allow", allow)
		}
		return h
	}

	tests := []struct {
		name     string
		input    serveInput
		expected serveExpected
	}{
		{
			name:     "serves registered routes",
			input:    serveInput{method: http.MethodGet, target: "/ok"},
			expected: serveExpected{status: http.StatusOK, header: http.Header{}},
		},
		{
			name:  "unmatched routes are the stdlib 404",
			input: serveInput{method: http.MethodGet, target: "/nope"},
			expected: serveExpected{
				status: http.StatusNotFound,
				header: stdlibPlainText(""),
				body:   "404 page not found\n",
			},
		},
		{
			name:  "a wrong method is the stdlib 405",
			input: serveInput{method: http.MethodDelete, target: "/ok"},
			expected: serveExpected{
				status: http.StatusMethodNotAllowed,
				header: stdlibPlainText("GET, HEAD"),
				body:   "Method Not Allowed\n",
			},
		},
		{
			name:  "an unclean unmatched path is the stdlib redirect",
			input: serveInput{method: http.MethodGet, target: "/a/../nope"},
			expected: serveExpected{
				status: http.StatusTemporaryRedirect,
				header: http.Header{
					"Content-Type": {"text/html; charset=utf-8"},
					"Location":     {"/nope"},
				},
				body: "<a href=\"/nope\">Temporary Redirect</a>.\n\n",
			},
		},
		{
			name:  "with an ErrorWriter, matched routes keep their pattern and path values",
			input: serveInput{errorWriter: true, method: http.MethodGet, target: "/orders/ord_1"},
			expected: serveExpected{
				status: http.StatusOK,
				header: http.Header{"Content-Type": {"application/json"}},
				body:   `{"id":"ord_1","pattern":"GET /orders/{id}"}`,
			},
		},
		{
			name:  "with an ErrorWriter, an unclean path to a route keeps ServeMux's redirect",
			input: serveInput{errorWriter: true, method: http.MethodGet, target: "/a/../ok"},
			expected: serveExpected{
				status: http.StatusTemporaryRedirect,
				header: http.Header{
					"Content-Type": {"text/html; charset=utf-8"},
					"Location":     {"/ok"},
				},
				body: "<a href=\"/ok\">Temporary Redirect</a>.\n\n",
			},
		},
		{
			name:  "with an ErrorWriter, a handler-written 404 is untouched",
			input: serveInput{errorWriter: true, method: http.MethodGet, target: "/gone"},
			expected: serveExpected{
				status: http.StatusNotFound,
				header: stdlibPlainText(""),
				body:   "404 page not found\n",
			},
		},
		{
			name:  "with an ErrorWriter, an unclean unmatched path keeps ServeMux's redirect",
			input: serveInput{errorWriter: true, method: http.MethodGet, target: "/a/../nope"},
			expected: serveExpected{
				status: http.StatusTemporaryRedirect,
				header: http.Header{
					"Content-Type": {"text/html; charset=utf-8"},
					"Location":     {"/nope"},
				},
				body: "<a href=\"/nope\">Temporary Redirect</a>.\n\n",
			},
		},
		{
			name:  "with an ErrorWriter, a double-slash unmatched path keeps ServeMux's redirect",
			input: serveInput{errorWriter: true, method: http.MethodGet, target: "//nope"},
			expected: serveExpected{
				status: http.StatusTemporaryRedirect,
				header: http.Header{
					"Content-Type": {"text/html; charset=utf-8"},
					"Location":     {"/nope"},
				},
				body: "<a href=\"/nope\">Temporary Redirect</a>.\n\n",
			},
		},
		{
			name:  "with an ErrorWriter, unmatched routes are a 404 through it",
			input: serveInput{errorWriter: true, method: http.MethodGet, target: "/nope"},
			expected: serveExpected{
				status:  http.StatusNotFound,
				header:  http.Header{"Content-Type": {"application/problem+json"}},
				body:    `{"type":"about:blank","title":"Not Found","status":404}`,
				written: []int{http.StatusNotFound},
			},
		},
		{
			name:  "with an ErrorWriter, HEAD on an unmatched route is a 404 through it",
			input: serveInput{errorWriter: true, method: http.MethodHead, target: "/nope"},
			expected: serveExpected{
				status:  http.StatusNotFound,
				header:  http.Header{"Content-Type": {"application/problem+json"}},
				body:    `{"type":"about:blank","title":"Not Found","status":404}`, // the recorder keeps it; a real server drops it
				written: []int{http.StatusNotFound},
			},
		},
		{
			name:  "with an ErrorWriter, a wrong method is a 405 through it with ServeMux's Allow",
			input: serveInput{errorWriter: true, method: http.MethodDelete, target: "/ok"},
			expected: serveExpected{
				status: http.StatusMethodNotAllowed,
				header: http.Header{
					"Allow":        {"GET, HEAD"},
					"Content-Type": {"application/problem+json"},
				},
				body:    `{"type":"about:blank","title":"Method Not Allowed","status":405}`,
				written: []int{http.StatusMethodNotAllowed},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ew recordingErrorWriter
			var errorWriter httpx.ErrorWriter
			if tt.input.errorWriter {
				errorWriter = ew.write
			}
			s := newServeHTTPServer(errorWriter)

			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), tt.input.method, tt.input.target, nil))

			if rec.Code != tt.expected.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.expected.status)
			}
			if !maps.EqualFunc(rec.Header(), tt.expected.header, slices.Equal) {
				t.Errorf("header = %v, want %v", rec.Header(), tt.expected.header)
			}
			if rec.Body.String() != tt.expected.body {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.expected.body)
			}
			if !slices.Equal(ew.got(), tt.expected.written) {
				t.Errorf("ErrorWriter statuses = %v, want %v", ew.got(), tt.expected.written)
			}
		})
	}

	t.Run("with an ErrorWriter, HEAD on an unmatched route sends no body over the wire", func(t *testing.T) {
		var ew recordingErrorWriter
		srv := httptest.NewServer(newServeHTTPServer(ew.write))
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodHead, srv.URL+"/nope", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read: %v", err)
		}

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
		if len(body) != 0 {
			t.Errorf("body = %q, want none", body)
		}
		if !slices.Equal(ew.got(), []int{http.StatusNotFound}) {
			t.Errorf("ErrorWriter statuses = %v, want [404]", ew.got())
		}
	})

	t.Run("with an ErrorWriter, Use middleware still wraps unmatched responses", func(t *testing.T) {
		var ew recordingErrorWriter
		s := newServeHTTPServer(ew.write)
		s.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Wrapped", "yes")
				next.ServeHTTP(w, r)
			})
		})

		requests := []struct {
			method, target string
			status         int
		}{
			{http.MethodGet, "/nope", http.StatusNotFound},
			{http.MethodDelete, "/ok", http.StatusMethodNotAllowed},
		}
		for _, req := range requests {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), req.method, req.target, nil))

			if rec.Code != req.status {
				t.Errorf("%s %s: status = %d, want %d", req.method, req.target, rec.Code, req.status)
			}
			if got := rec.Header().Get("X-Wrapped"); got != "yes" {
				t.Errorf("%s %s: X-Wrapped = %q, want yes", req.method, req.target, got)
			}
		}
		if !slices.Equal(ew.got(), []int{http.StatusNotFound, http.StatusMethodNotAllowed}) {
			t.Errorf("ErrorWriter statuses = %v, want [404 405]", ew.got())
		}
	})

	t.Run("safe under concurrent requests", func(t *testing.T) {
		s := httpx.New(httpx.Config{})
		s.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
				if rec.Code != http.StatusOK {
					t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
				}
			})
		}
		wg.Wait()
	})

	t.Run("with an ErrorWriter, safe under concurrent matched and unmatched requests", func(t *testing.T) {
		var ew recordingErrorWriter
		s := newServeHTTPServer(ew.write)

		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				target, want := "/ok", http.StatusOK
				if i%2 == 0 {
					target, want = "/nope", http.StatusNotFound
				}
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
				if rec.Code != want {
					t.Errorf("%s: status = %d, want %d", target, rec.Code, want)
				}
			})
		}
		wg.Wait()

		if len(ew.got()) != 4 {
			t.Errorf("ErrorWriter calls = %d, want 4", len(ew.got()))
		}
	})
}

func TestServerRun(t *testing.T) {
	tests := []struct {
		name     string
		input    func(t *testing.T) (*httpx.Server, context.Context)
		expected bool // error wanted
	}{
		{
			name: "canceled context shuts down gracefully",
			input: func(t *testing.T) (*httpx.Server, context.Context) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return httpx.New(httpx.Config{Addr: "127.0.0.1:0"}), ctx
			},
			expected: false,
		},
		{
			name: "unusable address surfaces the serve error",
			input: func(t *testing.T) (*httpx.Server, context.Context) {
				t.Helper()
				ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatalf("listen: %v", err)
				}
				t.Cleanup(func() { _ = ln.Close() })
				return httpx.New(httpx.Config{Addr: ln.Addr().String()}), context.Background()
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ctx := tt.input(t)

			err := s.Run(ctx)

			if (err != nil) != tt.expected {
				t.Errorf("Run() error = %v, want error: %t", err, tt.expected)
			}
		})
	}

	t.Run("in-flight request drains fully before Run returns", func(t *testing.T) {
		addr := reserveAddr(t)
		inFlight := make(chan struct{})
		release := make(chan struct{})

		s := httpx.New(httpx.Config{Addr: addr})
		s.Get("/slow", func(w http.ResponseWriter, _ *http.Request) {
			close(inFlight)
			<-release
			_, _ = io.WriteString(w, "drained")
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		runErr := make(chan error, 1)
		go func() { runErr <- s.Run(ctx) }()

		body := make(chan string, 1)
		go func() { body <- get(context.Background(), addr, "/slow") }()

		<-inFlight     // the request is inside the handler...
		cancel()       // ...when shutdown begins
		close(release) // handler finishes during the drain window

		if err := <-runErr; err != nil {
			t.Errorf("Run() = %v, want nil", err)
		}
		if got := <-body; got != "drained" {
			t.Errorf("body = %q, want %q — the drain must deliver the full response", got, "drained")
		}
	})

	t.Run("handler outliving the grace period surfaces DeadlineExceeded", func(t *testing.T) {
		addr := reserveAddr(t)
		inFlight := make(chan struct{})
		block := make(chan struct{})
		t.Cleanup(func() { close(block) })

		s := httpx.New(httpx.Config{Addr: addr, ShutdownGrace: 50 * time.Millisecond})
		s.Get("/hang", func(_ http.ResponseWriter, _ *http.Request) {
			close(inFlight)
			<-block
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		runErr := make(chan error, 1)
		go func() { runErr <- s.Run(ctx) }()

		clientCtx, clientCancel := context.WithCancel(context.Background())
		t.Cleanup(clientCancel)
		go func() { _ = get(clientCtx, addr, "/hang") }() // result irrelevant; the conn just has to hang

		<-inFlight
		cancel()

		if err := <-runErr; !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Run() = %v, want context.DeadlineExceeded", err)
		}
	})
}

func TestServerHTTPServer(t *testing.T) {
	tests := []struct {
		name     string
		input    httpx.Config
		expected string
	}{
		{
			name:     "exposes the underlying server",
			input:    httpx.Config{Addr: ":7070"},
			expected: ":7070",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httpx.New(tt.input).HTTPServer()

			if srv == nil {
				t.Fatal("HTTPServer() = nil")
			}
			if srv.Addr != tt.expected {
				t.Errorf("Addr = %q, want %q", srv.Addr, tt.expected)
			}
		})
	}
}
