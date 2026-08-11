package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

var errEntropy = errors.New("entropy source failed")

// errReader fails every Read, simulating a dead entropy source.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errEntropy }

func failRand(t *testing.T) {
	t.Helper()
	t.Cleanup(middleware.SetRandSource(errReader{}))
}

// isHexN reports whether s is exactly n lowercase hex characters.
func isHexN(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

type requestIDInput struct {
	cfg     middleware.RequestIDConfig
	inbound string // inbound header value; "" = header absent
}

type requestIDExpected struct {
	id   string // exact ID; "" with none=false means freshly generated 32-hex
	none bool   // no ctx value and no response header
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    requestIDInput
		expected requestIDExpected
	}{
		{
			name:     "inbound header value is reused",
			input:    requestIDInput{inbound: "gw-minted-42"},
			expected: requestIDExpected{id: "gw-minted-42"},
		},
		{
			name: "custom config header is honored",
			input: requestIDInput{
				cfg:     middleware.RequestIDConfig{Header: "X-Correlation-ID"},
				inbound: "corr-7",
			},
			expected: requestIDExpected{id: "corr-7"},
		},
		{
			name:     "absent inbound header generates a 32-hex ID",
			input:    requestIDInput{},
			expected: requestIDExpected{},
		},
		{
			name:     "inbound value over 128 chars is replaced by a generated ID",
			input:    requestIDInput{inbound: strings.Repeat("a", 129)},
			expected: requestIDExpected{},
		},
		{
			name:     "rand failure without inbound proceeds without an ID",
			mockFunc: failRand,
			input:    requestIDInput{},
			expected: requestIDExpected{none: true},
		},
		{
			name:     "rand failure with inbound still reuses the inbound ID",
			mockFunc: failRand,
			input:    requestIDInput{inbound: "gw-minted-42"},
			expected: requestIDExpected{id: "gw-minted-42"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFunc != nil {
				tt.mockFunc(t)
			}

			header := tt.input.cfg.Header
			if header == "" {
				header = middleware.DefaultRequestIDHeader
			}

			var ctxID string
			h := middleware.RequestID(tt.input.cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				ctxID = middleware.RequestIDFrom(r.Context())
			}))

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if tt.input.inbound != "" {
				req.Header.Set(header, tt.input.inbound)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			headerID := rec.Header().Get(header)

			switch {
			case tt.expected.none:
				if ctxID != "" {
					t.Errorf("ctx ID = %q, want none", ctxID)
				}
				if headerID != "" {
					t.Errorf("response header = %q, want none", headerID)
				}
			case tt.expected.id != "":
				if ctxID != tt.expected.id {
					t.Errorf("ctx ID = %q, want %q", ctxID, tt.expected.id)
				}
				if headerID != tt.expected.id {
					t.Errorf("response header = %q, want %q", headerID, tt.expected.id)
				}
			default:
				if !isHexN(ctxID, 32) {
					t.Errorf("ctx ID = %q, want 32 lowercase hex chars", ctxID)
				}
				if headerID != ctxID {
					t.Errorf("response header = %q, want ctx ID %q", headerID, ctxID)
				}
				if tt.input.inbound != "" && ctxID == tt.input.inbound {
					t.Errorf("ctx ID = %q, want the inbound value replaced", ctxID)
				}
			}
		})
	}
}

// requestIDContext runs a request with the given inbound ID through
// RequestID and captures the context the handler saw.
func requestIDContext(t *testing.T, id string) context.Context {
	t.Helper()

	var ctx context.Context
	h := middleware.RequestID(middleware.RequestIDConfig{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx = r.Context() //nolint:fatcontext // captures the request context once, no growth
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set(middleware.DefaultRequestIDHeader, id)
	h.ServeHTTP(httptest.NewRecorder(), req)

	return ctx
}

func TestRequestIDFrom(t *testing.T) {
	tests := []struct {
		name     string
		input    string // ID carried by the ctx; "" = ctx without an ID
		expected string
	}{
		{name: "context carrying a request ID", input: "gw-minted-42", expected: "gw-minted-42"},
		{name: "context without a request ID", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.input != "" {
				ctx = requestIDContext(t, tt.input)
			}

			if got := middleware.RequestIDFrom(ctx); got != tt.expected {
				t.Errorf("RequestIDFrom = %q, want %q", got, tt.expected)
			}
		})
	}
}
