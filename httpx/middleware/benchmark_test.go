package middleware_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

// nopWriter keeps the recorder out of the numbers.
type nopWriter struct {
	h http.Header
}

func (w *nopWriter) Header() http.Header         { return w.h }
func (w *nopWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nopWriter) WriteHeader(int)             {}

func benchOK() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func benchServe(b *testing.B, h http.Handler, body string) {
	b.Helper()

	w := &nopWriter{h: make(http.Header)}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/x", reader)
		if err != nil {
			b.Fatal(err)
		}
		req.RemoteAddr = "10.0.0.2:1234"
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}

		h.ServeHTTP(w, req)
	}
}

// BenchmarkBareHandler is the floor: request building plus the handler,
// no middleware — the number the others subtract.
func BenchmarkBareHandler(b *testing.B) {
	benchServe(b, benchOK(), "")
}

// BenchmarkLogger prices the access line alone, capture off — the
// default every request pays once the middleware is on.
func BenchmarkLogger(b *testing.B) {
	h := middleware.Logger(middleware.LoggerConfig{Log: discardLogger()})(benchOK())

	benchServe(b, h, "")
}

// BenchmarkLoggerCapture prices the opt-in: JSON request body captured,
// parsed, and logged as a structured attr.
func BenchmarkLoggerCapture(b *testing.B) {
	h := middleware.Logger(middleware.LoggerConfig{
		Log:            discardLogger(),
		LogRequestBody: true,
	})(benchOK())

	benchServe(b, h, `{"user":"dhira","password":"hunter2"}`)
}

// BenchmarkCanonicalChain prices the full identity stack a production
// request flows through: RealIP → RequestID → Trace → Logger → Recover.
func BenchmarkCanonicalChain(b *testing.B) {
	log := discardLogger()

	h := middleware.RealIP(middleware.RealIPConfig{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	})(middleware.RequestID(middleware.RequestIDConfig{})(
		middleware.Trace()(
			middleware.Logger(middleware.LoggerConfig{Log: log})(
				middleware.Recover(middleware.RecoverConfig{Log: log})(benchOK())))))

	benchServe(b, h, "")
}

// BenchmarkCanonicalChainParallel is the full identity stack under
// concurrent requests — shared entropy source, buffer pool, and slog
// handler all contended. Run with -cpu 1,4,8 for the traffic curve.
func BenchmarkCanonicalChainParallel(b *testing.B) {
	log := discardLogger()

	h := middleware.RealIP(middleware.RealIPConfig{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	})(middleware.RequestID(middleware.RequestIDConfig{})(
		middleware.Trace()(
			middleware.Logger(middleware.LoggerConfig{Log: log})(
				middleware.Recover(middleware.RecoverConfig{Log: log})(benchOK())))))

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		w := &nopWriter{h: make(http.Header)}
		for pb.Next() {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil)
			if err != nil {
				panic(err)
			}
			req.RemoteAddr = "10.0.0.2:1234"
			h.ServeHTTP(w, req)
		}
	})
}

// BenchmarkRateLimitParallel is concurrent clients through one limiter —
// the single bucket-map mutex is the number under test.
func BenchmarkRateLimitParallel(b *testing.B) {
	h := middleware.RateLimit(middleware.RateLimitConfig{Rate: 1 << 30, Burst: 1 << 30})(benchOK())

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		w := &nopWriter{h: make(http.Header)}
		for pb.Next() {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
			if err != nil {
				panic(err)
			}
			req.RemoteAddr = "10.0.0.2:1234"
			h.ServeHTTP(w, req)
		}
	})
}

// BenchmarkIdempotencyFirst is the winner's path: claim, execute,
// capture, store — a unique key per iteration.
func BenchmarkIdempotencyFirst(b *testing.B) {
	// Sized to b.N so capacity behavior never enters the measurement.
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(b.N + 1)})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"ord_1"}`)) //nolint:errcheck,gosec // benchmark writer never fails
		}))

	b.ReportAllocs()
	for i := range b.N {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/orders", strings.NewReader(`{"amount":1}`))
		r.Header.Set("Idempotency-Key", "bench-"+strconv.Itoa(i))
		handler.ServeHTTP(&nopWriter{h: make(http.Header)}, r)
	}
}

// BenchmarkIdempotencyReplay is the duplicate's path: one stored
// response served from the store on every iteration.
func BenchmarkIdempotencyReplay(b *testing.B) {
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"ord_1"}`)) //nolint:errcheck,gosec // benchmark writer never fails
		}))
	seed := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/orders", strings.NewReader(`{"amount":1}`))
	seed.Header.Set("Idempotency-Key", "bench-replay")
	handler.ServeHTTP(&nopWriter{h: make(http.Header)}, seed)

	b.ReportAllocs()
	for range b.N {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/orders", strings.NewReader(`{"amount":1}`))
		r.Header.Set("Idempotency-Key", "bench-replay")
		handler.ServeHTTP(&nopWriter{h: make(http.Header)}, r)
	}
}
