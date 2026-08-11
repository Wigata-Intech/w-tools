package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// RealIPConfig configures RealIP.
type RealIPConfig struct {
	// TrustedProxies are the peers allowed to assert client IPs.
	// Empty means trust nobody: RemoteAddr is used as-is.
	TrustedProxies []netip.Prefix

	// Headers checked in order; default: CF-Connecting-IP, X-Real-IP, X-Forwarded-For.
	Headers []string
}

// RealIP returns a middleware that rewrites r.RemoteAddr to the client IP
// asserted by a trusted proxy, preserving the original port. Headers from
// peers outside cfg.TrustedProxies are ignored and the request passes
// through unchanged.
func RealIP(cfg RealIPConfig) httpx.Middleware {
	if len(cfg.TrustedProxies) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	headers := cfg.Headers
	if headers == nil {
		headers = []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, port, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			peer, err := netip.ParseAddr(host)
			if err != nil || !containsAddr(cfg.TrustedProxies, peer.Unmap()) {
				next.ServeHTTP(w, r)
				return
			}

			for _, h := range headers {
				client, ok := clientFromHeader(h, r.Header.Get(h), cfg.TrustedProxies)
				if ok {
					r.RemoteAddr = net.JoinHostPort(client.String(), port)
					break
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// PrivateNetworks returns loopback plus the RFC 1918 and RFC 4193
// ranges — the usual "my own infrastructure" set for TrustedProxies when
// services sit behind a proxy on a private network:
//
//	middleware.RealIP(middleware.RealIPConfig{TrustedProxies: middleware.PrivateNetworks()})
//
// Passing it is still an explicit trust decision; RealIP has no implicit
// default because trusting a network you don't fully own lets anyone on
// it spoof client IPs.
func PrivateNetworks() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("fc00::/7"),
	}
}

// parseClientAddr parses one asserted client IP. Zoned IPv6 addresses
// are rejected: a zone is interface-local, meaningless coming from a
// proxy, and breaks JoinHostPort round-tripping. IPv4-mapped forms are
// unmapped so they match v4 prefixes.
func parseClientAddr(s string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, false
	}

	return addr.Unmap(), true
}

func containsAddr(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}

	return false
}

// clientFromHeader reports the client IP a header value asserts.
// For X-Forwarded-For the chain is walked right to left and every entry
// must parse: a trusted entry keeps walking, the first untrusted entry is
// the client, and an unparseable entry breaks the verifiable chain — the
// whole header is abandoned rather than walked past. If every entry is
// trusted, the leftmost is the origin. IPv4-mapped IPv6 addresses are
// unmapped so dual-stack forms match v4 prefixes.
func clientFromHeader(name, value string, trustedProxies []netip.Prefix) (netip.Addr, bool) {
	if value == "" {
		return netip.Addr{}, false
	}

	if !strings.EqualFold(name, "X-Forwarded-For") {
		addr, ok := parseClientAddr(value)
		if !ok {
			return netip.Addr{}, false
		}

		return addr, true
	}

	var leftmost netip.Addr

	for _, entry := range slices.Backward(strings.Split(value, ",")) {
		addr, ok := parseClientAddr(entry)
		if !ok {
			return netip.Addr{}, false
		}

		if !containsAddr(trustedProxies, addr) {
			return addr, true
		}

		leftmost = addr
	}

	return leftmost, true
}
