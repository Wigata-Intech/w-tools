package middleware_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

// FuzzRealIP feeds attacker-controlled proxy headers and peer addresses
// through RealIP. Invariants: never panic, and the rewritten RemoteAddr
// is either the original value or a valid "ip:port" built from it.
func FuzzRealIP(f *testing.F) {
	f.Add("10.0.0.2:9000", "203.0.113.9", "", "")
	f.Add("10.0.0.2:9000", "", "198.51.100.4", "203.0.113.7, 10.0.0.3")
	f.Add("[2001:db8::1]:443", "", "", "2001:db8::9")
	f.Add("garbage", "not-an-ip", ",,,,", "….!?")
	f.Add("10.0.0.2:9000", "", "", "10.0.0.4, 10.0.0.5")

	trusted := netip.MustParsePrefix("10.0.0.0/8")

	f.Fuzz(func(t *testing.T, remoteAddr, cf, xri, xff string) {
		var got string
		h := middleware.RealIP(middleware.RealIPConfig{
			TrustedProxies: []netip.Prefix{trusted},
		})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = r.RemoteAddr
		}))

		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		r.RemoteAddr = remoteAddr
		headers := map[string]string{"CF-Connecting-IP": cf, "X-Real-IP": xri, "X-Forwarded-For": xff}
		for k, v := range headers {
			if v != "" {
				r.Header.Set(k, v)
			}
		}

		h.ServeHTTP(httptest.NewRecorder(), r)

		if got == remoteAddr {
			return
		}

		host, port, err := net.SplitHostPort(got)
		if err != nil {
			t.Fatalf("rewritten RemoteAddr %q is not host:port", got)
		}
		if _, err := netip.ParseAddr(host); err != nil {
			t.Fatalf("rewritten host %q is not an IP", host)
		}
		if _, origPort, splitErr := net.SplitHostPort(remoteAddr); splitErr == nil && port != origPort {
			t.Fatalf("port changed: %q -> %q", origPort, port)
		}
	})
}

// FuzzTraceparent feeds arbitrary traceparent headers through Trace.
// Invariants: never panic, and whatever lands in ctx is well-formed —
// a 32-hex trace id and a 16-hex span id.
func FuzzTraceparent(f *testing.F) {
	f.Add("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	f.Add("00-00000000000000000000000000000000-00f067aa0ba902b7-01")
	f.Add("00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01")
	f.Add("ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	f.Add("00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01")
	f.Add("not a traceparent")
	f.Add("")

	f.Fuzz(func(t *testing.T, header string) {
		var traceID, spanID string
		h := middleware.Trace()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			traceID = middleware.TraceIDFrom(r.Context())
			spanID = middleware.SpanIDFrom(r.Context())
		}))

		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("Traceparent", header)
		}

		h.ServeHTTP(httptest.NewRecorder(), r)

		if len(traceID) != 32 || !hexRe.MatchString(traceID) {
			t.Fatalf("trace id %q is not 32-char lowercase hex", traceID)
		}
		if len(spanID) != 16 || !hexRe.MatchString(spanID) {
			t.Fatalf("span id %q is not 16-char lowercase hex", spanID)
		}
	})
}
