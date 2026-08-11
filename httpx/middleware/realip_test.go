package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

type realIPInput struct {
	remoteAddr string
	cfg        middleware.RealIPConfig
	headers    map[string]string
}

func TestRealIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

	tests := []struct {
		name     string
		input    realIPInput
		expected string
	}{
		{
			name: "no trusted proxies ignores headers",
			input: realIPInput{
				remoteAddr: "203.0.113.9:4567",
				cfg:        middleware.RealIPConfig{},
				headers:    map[string]string{"X-Real-IP": "198.51.100.4"},
			},
			expected: "203.0.113.9:4567",
		},
		{
			name: "trusted peer CF-Connecting-IP wins over later headers",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers: map[string]string{
					"CF-Connecting-IP": "203.0.113.7",
					"X-Real-IP":        "198.51.100.4",
				},
			},
			expected: "203.0.113.7:1234",
		},
		{
			name: "trusted peer X-Real-IP when CF header absent",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Real-IP": "198.51.100.4"},
			},
			expected: "198.51.100.4:1234",
		},
		{
			name: "XFF picks rightmost untrusted entry",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Forwarded-For": "203.0.113.7, 10.0.0.2"},
			},
			expected: "203.0.113.7:1234",
		},
		{
			name: "XFF garbage beyond the first untrusted hop is irrelevant",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Forwarded-For": "garbage, 203.0.113.7, 10.0.0.2"},
			},
			expected: "203.0.113.7:1234",
		},
		{
			name: "XFF spoof behind a garbage boundary is not walked past",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Forwarded-For": "6.6.6.6, garbage, 10.0.0.3"},
			},
			expected: "10.0.0.2:1234",
		},
		{
			name: "zoned IPv6 addresses are rejected",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Real-IP": "fe80::1%eth0"},
			},
			expected: "10.0.0.2:1234",
		},
		{
			name: "single-value header tolerates surrounding whitespace",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Real-IP": " 198.51.100.4 "},
			},
			expected: "198.51.100.4:1234",
		},
		{
			name: "IPv4-mapped IPv6 entries match v4 trusted prefixes",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Forwarded-For": "203.0.113.7, ::ffff:10.0.0.3"},
			},
			expected: "203.0.113.7:1234",
		},
		{
			name: "XFF entirely trusted uses leftmost entry",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Forwarded-For": "10.0.0.9, 10.0.0.2"},
			},
			expected: "10.0.0.9:1234",
		},
		{
			name: "custom header list treats x-forwarded-for case-insensitively",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg: middleware.RealIPConfig{
					TrustedProxies: trusted,
					Headers:        []string{"x-forwarded-for"},
				},
				headers: map[string]string{
					"CF-Connecting-IP": "198.51.100.4",
					"X-Forwarded-For":  "203.0.113.7, 10.0.0.2",
				},
			},
			expected: "203.0.113.7:1234",
		},
		{
			name: "port from original RemoteAddr preserved",
			input: realIPInput{
				remoteAddr: "10.0.0.2:5555",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Real-IP": "203.0.113.7"},
			},
			expected: "203.0.113.7:5555",
		},
		{
			name: "IPv6 client address handled",
			input: realIPInput{
				remoteAddr: "[::1]:1234",
				cfg: middleware.RealIPConfig{
					TrustedProxies: []netip.Prefix{netip.MustParsePrefix("::1/128")},
				},
				headers: map[string]string{"X-Real-IP": "2001:db8::1"},
			},
			expected: "[2001:db8::1]:1234",
		},
		{
			name: "untrusted peer spoofed headers ignored",
			input: realIPInput{
				remoteAddr: "192.0.2.5:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers: map[string]string{
					"CF-Connecting-IP": "203.0.113.7",
					"X-Forwarded-For":  "203.0.113.7",
				},
			},
			expected: "192.0.2.5:1234",
		},
		{
			name: "malformed first header falls through to next",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers: map[string]string{
					"CF-Connecting-IP": "not-an-ip",
					"X-Real-IP":        "198.51.100.4",
				},
			},
			expected: "198.51.100.4:1234",
		},
		{
			name: "all headers malformed leaves RemoteAddr unchanged",
			input: realIPInput{
				remoteAddr: "10.0.0.2:1234",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers: map[string]string{
					"CF-Connecting-IP": "not-an-ip",
					"X-Real-IP":        "also-bad",
					"X-Forwarded-For":  "nope, nada",
				},
			},
			expected: "10.0.0.2:1234",
		},
		{
			name: "peer host that is not an IP passes through",
			input: realIPInput{
				remoteAddr: "localhost:8080",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Real-IP": "198.51.100.4"},
			},
			expected: "localhost:8080",
		},
		{
			name: "unparseable RemoteAddr passes through",
			input: realIPInput{
				remoteAddr: "garbage",
				cfg:        middleware.RealIPConfig{TrustedProxies: trusted},
				headers:    map[string]string{"X-Real-IP": "198.51.100.4"},
			},
			expected: "garbage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			req.RemoteAddr = tt.input.remoteAddr
			for k, v := range tt.input.headers {
				req.Header.Set(k, v)
			}

			var got string
			handler := middleware.RealIP(tt.input.cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			handler.ServeHTTP(httptest.NewRecorder(), req)

			if got != tt.expected {
				t.Errorf("RemoteAddr = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPrivateNetworks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool // contained in some PrivateNetworks prefix
	}{
		{name: "loopback", input: "127.0.0.1", expected: true},
		{name: "RFC 1918 10/8", input: "10.20.30.40", expected: true},
		{name: "RFC 1918 172.16/12", input: "172.31.255.1", expected: true},
		{name: "RFC 1918 192.168/16", input: "192.168.1.1", expected: true},
		{name: "IPv6 loopback", input: "::1", expected: true},
		{name: "IPv6 unique local", input: "fd12::1", expected: true},
		{name: "public IPv4 excluded", input: "8.8.8.8", expected: false},
		{name: "public IPv6 excluded", input: "2001:db8::1", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.input)

			var contained bool
			for _, p := range middleware.PrivateNetworks() {
				if p.Contains(addr) {
					contained = true
					break
				}
			}

			if contained != tt.expected {
				t.Errorf("contained(%s) = %t, want %t", tt.input, contained, tt.expected)
			}
		})
	}
}
