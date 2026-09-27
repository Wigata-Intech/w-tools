package middleware_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
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

// FuzzIdempotency feeds attacker-controlled keys, paths, and bodies
// through Idempotency. Invariants: never panic; an identical duplicate
// is replayed byte-for-byte and marked; a different body under the same
// key is refused with 422 and never reaches the handler.
func FuzzIdempotency(f *testing.F) {
	f.Add("key-1", "/orders", "{\"a\":1}", "{\"a\":2}")
	f.Add("", "/x", "", "body")
	f.Add("k\x00k", "/a%2Fb", "…!?", "…!?x")
	f.Add("same", "/p", "payload", "payload")

	f.Fuzz(func(t *testing.T, key, path, body1, body2 string) {
		executions := 0
		h := middleware.Idempotency(middleware.IdempotencyConfig{
			Store: middleware.NewMemoryStore(0),
		})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executions++
			b, _ := io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			w.Write(b) //nolint:errcheck,gosec // test writer never fails
		}))

		send := func(body string) *httptest.ResponseRecorder {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/fixed", strings.NewReader(body))
			// Assigned after construction so arbitrary fuzzed paths reach
			// the fingerprint without tripping request-line validation.
			r.URL.Path = path
			if key != "" {
				r.Header.Set("Idempotency-Key", key)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, r)

			return rr
		}

		first := send(body1)
		second := send(body2)

		if key == "" {
			if executions != 2 {
				t.Fatalf("keyless requests executed %d times, want 2", executions)
			}

			return
		}

		if body1 == body2 {
			if executions != 1 {
				t.Fatalf("identical duplicate executed handler %d times, want 1", executions)
			}
			if second.Code != first.Code || second.Body.String() != first.Body.String() {
				t.Fatalf("replay differs: %d %q vs %d %q", second.Code, second.Body.String(), first.Code, first.Body.String())
			}
			if second.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatal("replay not marked")
			}

			return
		}

		if second.Code != http.StatusUnprocessableEntity {
			t.Fatalf("mismatched duplicate status = %d, want 422", second.Code)
		}
		if executions != 1 {
			t.Fatalf("mismatched duplicate executed handler %d times, want 1", executions)
		}
	})
}

// strictIDRe is StrictRequestID's documented rule as an independent oracle.
var strictIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// FuzzStrictRequestID feeds arbitrary inbound IDs through RequestID with
// StrictRequestID. Invariants: never panic; StrictRequestID accepts
// exactly the documented alphabet and length; an accepted ID is echoed
// as-is, a rejected one never is — a fresh 32-hex ID replaces it.
func FuzzStrictRequestID(f *testing.F) {
	f.Add("a")
	f.Add(strings.Repeat("aZ09._-x", 8))
	f.Add(strings.Repeat("a", 65))
	f.Add("bad id")
	f.Add("<script>")
	f.Add("a/b")
	f.Add("é\x00\n")
	f.Add("")

	f.Fuzz(func(t *testing.T, inbound string) {
		accepted := middleware.StrictRequestID(inbound)
		if accepted != strictIDRe.MatchString(inbound) {
			t.Fatalf("StrictRequestID(%q) = %t, disagrees with the documented rule", inbound, accepted)
		}

		var ctxID string
		h := middleware.RequestID(middleware.RequestIDConfig{
			Valid: middleware.StrictRequestID,
		})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			ctxID = middleware.RequestIDFrom(r.Context())
		}))

		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		if inbound != "" {
			r.Header.Set(middleware.DefaultRequestIDHeader, inbound)
		}

		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		echoed := rr.Header().Get(middleware.DefaultRequestIDHeader)

		if echoed != ctxID {
			t.Fatalf("echoed %q differs from ctx ID %q", echoed, ctxID)
		}
		if accepted {
			if echoed != inbound {
				t.Fatalf("accepted ID %q echoed as %q", inbound, echoed)
			}

			return
		}
		if len(echoed) != 32 || !hexRe.MatchString(echoed) {
			t.Fatalf("rejected ID %q replaced by %q, want 32-char lowercase hex", inbound, echoed)
		}
	})
}
