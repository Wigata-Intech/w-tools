package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

const (
	validTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	inboundTraceID   = "4bf92f3577b34da6a3ce929d0e0e4736"
	inboundParentID  = "00f067aa0ba902b7"
)

type traceExpected struct {
	traceID  string // exact adopted trace-id; "" with none=false means freshly minted 32-hex
	rejected string // inbound trace-id that must NOT be adopted; "" = n/a
	flags    string // expected TraceFlagsFrom value; "" = don't assert
	none     bool   // rand failure: no IDs stored
}

func TestTrace(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    string // traceparent header value; "" = header absent
		expected traceExpected
	}{
		{
			name:     "valid traceparent adopts the trace-id and mints a span-id",
			input:    validTraceparent,
			expected: traceExpected{traceID: inboundTraceID, flags: "01"},
		},
		{
			name:     "inbound flags survive adoption",
			input:    "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00",
			expected: traceExpected{traceID: inboundTraceID, flags: "00"},
		},
		{
			name:     "absent header mints both IDs",
			input:    "",
			expected: traceExpected{flags: "01"},
		},
		{
			name:     "wrong length mints both IDs",
			input:    "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-1",
			expected: traceExpected{rejected: inboundTraceID},
		},
		{
			name:     "non-hex trace-id mints both IDs",
			input:    "00-gbf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			expected: traceExpected{rejected: "gbf92f3577b34da6a3ce929d0e0e4736"},
		},
		{
			name:     "uppercase hex mints both IDs",
			input:    "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
			expected: traceExpected{rejected: "4BF92F3577B34DA6A3CE929D0E0E4736"},
		},
		{
			name:     "version ff mints both IDs",
			input:    "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			expected: traceExpected{rejected: inboundTraceID},
		},
		{
			name:     "all-zero trace-id mints both IDs",
			input:    "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
			expected: traceExpected{rejected: "00000000000000000000000000000000"},
		},
		{
			name:     "all-zero parent-id mints both IDs",
			input:    "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
			expected: traceExpected{rejected: inboundTraceID},
		},
		{
			name:     "rand failure stores no IDs",
			mockFunc: failRand,
			input:    "",
			expected: traceExpected{none: true},
		},
		{
			name:     "rand failure on the span mint drops the adopted trace-id too",
			mockFunc: failRand,
			input:    validTraceparent,
			expected: traceExpected{none: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFunc != nil {
				tt.mockFunc(t)
			}

			var traceID, spanID, flags string
			h := middleware.Trace()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				traceID = middleware.TraceIDFrom(r.Context())
				spanID = middleware.SpanIDFrom(r.Context())
				flags = middleware.TraceFlagsFrom(r.Context())
			}))

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if tt.input != "" {
				req.Header.Set("Traceparent", tt.input)
			}

			h.ServeHTTP(httptest.NewRecorder(), req)

			if tt.expected.none {
				if traceID != "" {
					t.Errorf("trace ID = %q, want none", traceID)
				}
				if spanID != "" {
					t.Errorf("span ID = %q, want none", spanID)
				}

				return
			}

			if tt.expected.traceID != "" {
				if traceID != tt.expected.traceID {
					t.Errorf("trace ID = %q, want adopted %q", traceID, tt.expected.traceID)
				}
			} else {
				if !isHexN(traceID, 32) {
					t.Errorf("trace ID = %q, want 32 lowercase hex chars", traceID)
				}
				if tt.expected.rejected != "" && traceID == tt.expected.rejected {
					t.Errorf("trace ID = %q, want the inbound trace-id rejected", traceID)
				}
			}

			if !isHexN(spanID, 16) {
				t.Errorf("span ID = %q, want 16 lowercase hex chars", spanID)
			}
			if spanID == inboundParentID {
				t.Errorf("span ID = %q, want a new span, not the inbound parent-id", spanID)
			}
			if tt.expected.flags != "" && flags != tt.expected.flags {
				t.Errorf("flags = %q, want %q", flags, tt.expected.flags)
			}
		})
	}
}

func TestTraceFlagsFrom(t *testing.T) {
	tests := []struct {
		name     string
		input    string // traceparent to run through Trace; "" = plain ctx
		expected string
	}{
		{name: "context carrying adopted flags", input: validTraceparent, expected: "01"},
		{name: "context without flags", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.input != "" {
				ctx = traceContext(t, tt.input)
			}

			if got := middleware.TraceFlagsFrom(ctx); got != tt.expected {
				t.Errorf("TraceFlagsFrom = %q, want %q", got, tt.expected)
			}
		})
	}
}

// traceContext runs a request with the given traceparent through Trace
// and captures the context the handler saw.
func traceContext(t *testing.T, traceparent string) context.Context {
	t.Helper()

	var ctx context.Context
	h := middleware.Trace()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx = r.Context() //nolint:fatcontext // captures the request context once, no growth
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set("Traceparent", traceparent)
	h.ServeHTTP(httptest.NewRecorder(), req)

	return ctx
}

func TestTraceIDFrom(t *testing.T) {
	tests := []struct {
		name     string
		input    string // traceparent to run through Trace; "" = plain ctx
		expected string
	}{
		{name: "context carrying a trace ID", input: validTraceparent, expected: inboundTraceID},
		{name: "context without a trace ID", input: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.input != "" {
				ctx = traceContext(t, tt.input)
			}

			if got := middleware.TraceIDFrom(ctx); got != tt.expected {
				t.Errorf("TraceIDFrom = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSpanIDFrom(t *testing.T) {
	tests := []struct {
		name     string
		input    bool // run the ctx through Trace
		expected int  // expected span ID length; 0 = absent
	}{
		{name: "context carrying a span ID", input: true, expected: 16},
		{name: "context without a span ID", input: false, expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.input {
				ctx = traceContext(t, validTraceparent)
			}

			got := middleware.SpanIDFrom(ctx)
			if len(got) != tt.expected {
				t.Errorf("SpanIDFrom = %q, want length %d", got, tt.expected)
			}
			if tt.input && !isHexN(got, 16) {
				t.Errorf("SpanIDFrom = %q, want 16 lowercase hex chars", got)
			}
		})
	}
}
