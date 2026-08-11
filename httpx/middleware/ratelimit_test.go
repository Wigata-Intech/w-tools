package middleware_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

// stubLimiter satisfies middleware.Limiter for the bring-your-own case.
type stubLimiter struct {
	ok    bool
	retry time.Duration
	keys  []string
}

func (s *stubLimiter) Allow(_ context.Context, key string) (bool, time.Duration) {
	s.keys = append(s.keys, key)

	return s.ok, s.retry
}

func TestRateLimit(t *testing.T) {
	type rateLimitInput struct {
		cfg      middleware.RateLimitConfig
		requests []string      // RemoteAddrs, fired in order
		step     time.Duration // clock advance between requests; keeps "coldest" well-defined
		advance  time.Duration
		after    []string // RemoteAddrs fired after the clock advance
	}

	type rateLimitExpected struct {
		statuses   []int // one per request, in order
		retryAfter bool  // final response must carry Retry-After >= 1
		keys       []string
	}

	tests := []struct {
		name     string
		input    rateLimitInput
		expected rateLimitExpected
	}{
		{
			name: "burst allows, the next request is 429 with Retry-After",
			input: rateLimitInput{
				cfg:      middleware.RateLimitConfig{Rate: 1, Burst: 2},
				requests: []string{"203.0.113.7:1", "203.0.113.7:2", "203.0.113.7:3"},
			},
			expected: rateLimitExpected{
				statuses:   []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests},
				retryAfter: true,
			},
		},
		{
			name: "Burst defaults from Rate",
			input: rateLimitInput{
				cfg:      middleware.RateLimitConfig{Rate: 2},
				requests: []string{"203.0.113.7:1", "203.0.113.7:2", "203.0.113.7:3"},
			},
			expected: rateLimitExpected{
				statuses: []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests},
			},
		},
		{
			name: "tokens refill over a driven clock",
			input: rateLimitInput{
				cfg:      middleware.RateLimitConfig{Rate: 1, Burst: 1},
				requests: []string{"203.0.113.7:1", "203.0.113.7:2"},
				advance:  1500 * time.Millisecond,
				after:    []string{"203.0.113.7:3"},
			},
			expected: rateLimitExpected{
				statuses: []int{http.StatusOK, http.StatusTooManyRequests, http.StatusOK},
			},
		},
		{
			name: "keys are isolated: one hot client cannot starve another",
			input: rateLimitInput{
				cfg:      middleware.RateLimitConfig{Rate: 1, Burst: 1},
				requests: []string{"203.0.113.7:1", "203.0.113.7:2", "198.51.100.4:1"},
			},
			expected: rateLimitExpected{
				statuses: []int{http.StatusOK, http.StatusTooManyRequests, http.StatusOK},
			},
		},
		{
			name: "custom Key func buckets by its value",
			input: rateLimitInput{
				cfg: middleware.RateLimitConfig{
					Rate:  1,
					Burst: 1,
					Key:   func(r *http.Request) string { return r.Header.Get("X-Api-Key") },
				},
				// same RemoteAddr; the header is the key, absent = one shared bucket
				requests: []string{"203.0.113.7:1", "203.0.113.7:2"},
			},
			expected: rateLimitExpected{
				statuses: []int{http.StatusOK, http.StatusTooManyRequests},
			},
		},
		{
			name: "eviction at MaxKeys: the coldest key restarts with a full burst",
			input: rateLimitInput{
				cfg: middleware.RateLimitConfig{Rate: 1, Burst: 1, MaxKeys: 2},
				// exhaust A, touch B, insert C (evicts coldest = A), then A again: fresh burst
				requests: []string{"10.0.0.1:1", "10.0.0.2:1", "10.0.0.3:1", "10.0.0.1:2"},
				step:     time.Millisecond, // distinct timestamps make "coldest" deterministic
			},
			expected: rateLimitExpected{
				statuses: []int{http.StatusOK, http.StatusOK, http.StatusOK, http.StatusOK},
			},
		},
		{
			name: "custom Limiter is consulted with the derived key",
			input: rateLimitInput{
				cfg: middleware.RateLimitConfig{
					Limiter: &stubLimiter{ok: false, retry: 3 * time.Second},
				},
				requests: []string{"203.0.113.7:1"},
			},
			expected: rateLimitExpected{
				statuses:   []int{http.StatusTooManyRequests},
				retryAfter: true,
				keys:       []string{"203.0.113.7"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := time.Unix(1700000000, 0)
			restore := middleware.SetTimeNow(func() time.Time { return clock })
			t.Cleanup(restore)

			h := middleware.RateLimit(tt.input.cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			var last *httptest.ResponseRecorder

			fire := func(remoteAddr string) {
				req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
				req.RemoteAddr = remoteAddr
				last = httptest.NewRecorder()
				h.ServeHTTP(last, req)
			}

			var got []int
			for _, addr := range tt.input.requests {
				fire(addr)
				got = append(got, last.Code)
				clock = clock.Add(tt.input.step)
			}

			clock = clock.Add(tt.input.advance)
			for _, addr := range tt.input.after {
				fire(addr)
				got = append(got, last.Code)
			}

			for i, want := range tt.expected.statuses {
				if got[i] != want {
					t.Errorf("request %d status = %d, want %d (all: %v)", i, got[i], want, got)
				}
			}

			if tt.expected.retryAfter {
				ra := last.Header().Get("Retry-After")
				if ra == "" || ra == "0" {
					t.Errorf("Retry-After = %q, want >= 1 second", ra)
				}
				if ct := last.Header().Get("Content-Type"); ct != "application/problem+json" {
					t.Errorf("Content-Type = %q, want problem+json default", ct)
				}
			}

			if tt.expected.keys != nil {
				sl, _ := tt.input.cfg.Limiter.(*stubLimiter)
				if len(sl.keys) != len(tt.expected.keys) || sl.keys[0] != tt.expected.keys[0] {
					t.Errorf("limiter saw keys %v, want %v", sl.keys, tt.expected.keys)
				}
			}
		})
	}

	t.Run("custom ErrorWriter formats the 429", func(t *testing.T) {
		h := middleware.RateLimit(middleware.RateLimitConfig{
			Rate:  1,
			Burst: 1,
			ErrorWriter: func(w http.ResponseWriter, _ *http.Request, status int, _ string) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "slow down")
			},
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		for range 2 {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			req.RemoteAddr = "203.0.113.7:1"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code == http.StatusTooManyRequests && rec.Body.String() != "slow down" {
				t.Errorf("429 body = %q, want %q", rec.Body.String(), "slow down")
			}
		}
	})

	t.Run("idle buckets are swept before the coldest is considered", func(t *testing.T) {
		clock := time.Unix(1700000000, 0)
		restore := middleware.SetTimeNow(func() time.Time { return clock })
		t.Cleanup(restore)

		h := middleware.RateLimit(middleware.RateLimitConfig{Rate: 1, Burst: 1, MaxKeys: 2})(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

		fire := func(remoteAddr string) int {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			req.RemoteAddr = remoteAddr
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			return rec.Code
		}

		fire("10.0.0.1:1")
		fire("10.0.0.2:1")
		clock = clock.Add(10 * time.Second) // both now idle past the horizon
		if got := fire("10.0.0.3:1"); got != http.StatusOK {
			t.Errorf("new key after sweep = %d, want 200", got)
		}
	})

	t.Run("nil Limiter with no Rate panics at construction", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("RateLimit(RateLimitConfig{}) did not panic")
			}
		}()
		middleware.RateLimit(middleware.RateLimitConfig{})
	})

	t.Run("safe under concurrent requests", func(_ *testing.T) {
		h := middleware.RateLimit(middleware.RateLimitConfig{Rate: 1000, Burst: 1000})(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

		done := make(chan struct{})
		for i := range 8 {
			go func(n int) {
				defer func() { done <- struct{}{} }()
				req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
				req.RemoteAddr = "10.0.0." + strings.Repeat("1", n%3+1) + ":1"
				h.ServeHTTP(httptest.NewRecorder(), req)
			}(i)
		}
		for range 8 {
			<-done
		}
	})
}
