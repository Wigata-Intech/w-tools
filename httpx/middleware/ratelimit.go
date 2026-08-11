package middleware

import (
	"context"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// Limiter is the rate decision. The in-package default is a token
// bucket; any other algorithm — or any backend, including a store for
// cluster-wide limits — plugs in by implementing it.
type Limiter interface {
	Allow(ctx context.Context, key string) (ok bool, retryAfter time.Duration)
}

// RateLimitConfig configures RateLimit.
type RateLimitConfig struct {
	// Rate is sustained requests per second per key. Required when
	// Limiter is nil; RateLimit panics at construction otherwise.
	Rate float64

	// Burst is the bucket size. Default max(1, Rate).
	Burst int

	// Limiter overrides the algorithm or backend. Nil means the
	// in-package token bucket built from Rate and Burst.
	Limiter Limiter

	// Key buckets requests. Default: the client IP — which means RealIP
	// must run earlier in the chain, or the key is the proxy's address.
	Key func(r *http.Request) string

	// MaxKeys caps live buckets for the in-package limiter (default
	// DefaultMaxKeys). The key space is attacker-controlled, so memory
	// is bounded by construction. A custom Limiter owns its own memory.
	MaxKeys int

	ErrorWriter httpx.ErrorWriter // nil = RFC 9457 Problem
}

// RateLimit rejects requests over the per-key limit with a 429 carrying
// Retry-After. Limits are per instance and in memory: the edge (CDN,
// reverse proxy) stays the first line of defense; this is the
// application backstop.
func RateLimit(cfg RateLimitConfig) httpx.Middleware {
	limiter := cfg.Limiter
	if limiter == nil {
		if cfg.Rate <= 0 {
			panic("middleware: RateLimit needs Rate > 0 or a custom Limiter")
		}

		limiter = newTokenBucket(cfg.Rate, cfg.Burst, cfg.MaxKeys)
	}

	key := cfg.Key
	if key == nil {
		key = func(r *http.Request) string { return remoteIP(r.RemoteAddr) }
	}

	errorWriter := cfg.ErrorWriter
	if errorWriter == nil {
		errorWriter = func(w http.ResponseWriter, _ *http.Request, status int, detail string) {
			httpx.Error(w, status, detail)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, retryAfter := limiter.Allow(r.Context(), key(r))
			if !ok {
				if retryAfter > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
				}

				errorWriter(w, r, http.StatusTooManyRequests, "rate limit exceeded")

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// tokenBucket is the in-package Limiter: lazily refilled buckets keyed
// by client, one mutex, no background goroutines.
type tokenBucket struct {
	rate    float64
	burst   float64
	maxKeys int

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newTokenBucket(rate float64, burst, maxKeys int) *tokenBucket {
	if burst < 1 {
		burst = int(math.Max(1, rate))
	}
	if maxKeys <= 0 {
		maxKeys = DefaultMaxKeys
	}

	return &tokenBucket{
		rate:    rate,
		burst:   float64(burst),
		maxKeys: maxKeys,
		buckets: make(map[string]*bucket),
	}
}

func (t *tokenBucket) Allow(_ context.Context, key string) (bool, time.Duration) {
	now := timeNow()

	t.mu.Lock()
	defer t.mu.Unlock()

	b, ok := t.buckets[key]
	if !ok {
		if len(t.buckets) >= t.maxKeys {
			t.evict(now)
		}

		b = &bucket{tokens: t.burst, last: now}
		t.buckets[key] = b
	} else {
		b.tokens = math.Min(t.burst, b.tokens+t.rate*now.Sub(b.last).Seconds())
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--

		return true, 0
	}

	return false, time.Duration((1 - b.tokens) / t.rate * float64(time.Second))
}

// evict drops every bucket idle past twice its full-refill horizon — an
// idle-refilled bucket is indistinguishable from a fresh one. If the map
// is still full, the coldest tenth goes in one batch, so the scan cost
// amortizes across many inserts instead of repeating per request.
func (t *tokenBucket) evict(now time.Time) {
	// Float seconds, not time.Duration: burst/rate can exceed the
	// Duration range at extreme configs and must not overflow.
	horizonSec := 2 * t.burst / t.rate

	for k, b := range t.buckets {
		if now.Sub(b.last).Seconds() >= horizonSec {
			delete(t.buckets, k)
		}
	}

	if len(t.buckets) < t.maxKeys {
		return
	}

	type entry struct {
		key  string
		last time.Time
	}

	all := make([]entry, 0, len(t.buckets))
	for k, b := range t.buckets {
		all = append(all, entry{key: k, last: b.last})
	}

	slices.SortFunc(all, func(a, b entry) int { return a.last.Compare(b.last) })

	for _, e := range all[:len(all)/10+1] {
		delete(t.buckets, e.key)
	}
}
