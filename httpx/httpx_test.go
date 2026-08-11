package httpx_test

import (
	"context"
	"errors"
	"io"
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

func TestServerServeHTTP(t *testing.T) {
	tests := []struct {
		name     string
		input    string // request target
		expected int
	}{
		{
			name:     "serves registered routes",
			input:    "/ok",
			expected: http.StatusOK,
		},
		{
			name:     "unmatched routes are the stdlib 404",
			input:    "/nope",
			expected: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := httpx.New(httpx.Config{})
			s.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.input, nil))

			if rec.Code != tt.expected {
				t.Errorf("status = %d, want %d", rec.Code, tt.expected)
			}
		})
	}

	t.Run("safe under concurrent requests", func(t *testing.T) {
		s := httpx.New(httpx.Config{})
		s.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
				if rec.Code != http.StatusOK {
					t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
				}
			}()
		}
		wg.Wait()
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
