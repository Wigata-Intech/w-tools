package httpx_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// nopWriter is the cheapest possible ResponseWriter, so the numbers
// price the router, not the recorder.
type nopWriter struct {
	h http.Header
}

func (w *nopWriter) Header() http.Header         { return w.h }
func (w *nopWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nopWriter) WriteHeader(int)             {}

func benchRequest(b *testing.B) *http.Request {
	b.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/orders/42", nil)
	if err != nil {
		b.Fatal(err)
	}

	return req
}

func benchHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.PathValue("id") // touch the wildcard so both routers do equal work
		_, _ = w.Write([]byte("ok"))
	}
}

// BenchmarkServeMuxBaseline is raw stdlib routing — the floor every
// other number compares against.
func BenchmarkServeMuxBaseline(b *testing.B) {
	mux := http.NewServeMux()
	mux.Handle("GET /orders/{id}", benchHandler())

	w := &nopWriter{h: make(http.Header)}
	req := benchRequest(b)

	b.ReportAllocs()

	for range b.N {
		mux.ServeHTTP(w, req)
	}
}

// BenchmarkGroupRoute is the same route registered through nested httpx
// groups — the claim under test: grouping is registration-time sugar
// with zero per-request cost.
func BenchmarkGroupRoute(b *testing.B) {
	s := httpx.New(httpx.Config{})
	s.Group("/orders").Get("/{id}", benchHandler())

	w := &nopWriter{h: make(http.Header)}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/orders/42", nil)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for range b.N {
		s.ServeHTTP(w, req)
	}
}
