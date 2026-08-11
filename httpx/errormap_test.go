package httpx_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
)

var (
	errNotFound     = errors.New("order does not exist")
	errInsufficient = errors.New("balance too low")
	errInternal     = errors.New("database password is hunter2") // must never reach a response
)

// quotaError carries its own mapping via the Problemer interface.
type quotaError struct{}

func (*quotaError) Error() string { return "quota exceeded" }

func (*quotaError) Problem() httpx.Problem {
	return httpx.Problem{Title: "Quota Exceeded", Status: http.StatusTooManyRequests}
}

func newErrorMap() *httpx.ErrorMap {
	m := httpx.NewErrorMap()
	m.Map(errNotFound, httpx.Problem{Status: http.StatusNotFound})
	m.Map(errInsufficient, httpx.Problem{Title: "Insufficient Funds", Status: http.StatusUnprocessableEntity})

	return m
}

func TestNewErrorMap(t *testing.T) {
	tests := []struct {
		name     string
		input    error
		expected int
	}{
		{
			name:     "empty map responds with a bare 500",
			input:    errInternal,
			expected: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			httpx.NewErrorMap().Respond(rec, tt.input)

			if rec.Code != tt.expected {
				t.Errorf("status = %d, want %d", rec.Code, tt.expected)
			}
		})
	}
}

func TestErrorMapMap(t *testing.T) {
	tests := []struct {
		name     string
		input    error
		expected int
	}{
		{
			name:     "first registered match wins over a later one",
			input:    fmt.Errorf("%w: %w", errNotFound, errInsufficient),
			expected: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			newErrorMap().Respond(rec, tt.input)

			if rec.Code != tt.expected {
				t.Errorf("status = %d, want %d", rec.Code, tt.expected)
			}
		})
	}
}

func TestErrorMapRespond(t *testing.T) {
	type respondExpected struct {
		status       int
		bodyContains string
		bodyExcludes string
	}

	tests := []struct {
		name     string
		input    error
		expected respondExpected
	}{
		{
			name:     "a Problemer error carries its own mapping",
			input:    &quotaError{},
			expected: respondExpected{status: http.StatusTooManyRequests, bodyContains: "Quota Exceeded"},
		},
		{
			name:     "a wrapped Problemer is still found",
			input:    fmt.Errorf("charging: %w", &quotaError{}),
			expected: respondExpected{status: http.StatusTooManyRequests},
		},
		{
			name:     "a registered sentinel matches",
			input:    errNotFound,
			expected: respondExpected{status: http.StatusNotFound, bodyContains: "Not Found"},
		},
		{
			name:     "a wrapped sentinel matches via errors.Is",
			input:    fmt.Errorf("fetching order: %w", errInsufficient),
			expected: respondExpected{status: http.StatusUnprocessableEntity, bodyContains: "Insufficient Funds"},
		},
		{
			name:  "an unmapped error is a bare 500 that never leaks the error text",
			input: errInternal,
			expected: respondExpected{
				status:       http.StatusInternalServerError,
				bodyContains: "Internal Server Error",
				bodyExcludes: "hunter2",
			},
		},
		{
			name:     "a nil error degrades to the bare 500",
			input:    nil,
			expected: respondExpected{status: http.StatusInternalServerError},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			newErrorMap().Respond(rec, tt.input)

			if rec.Code != tt.expected.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.expected.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("Content-Type = %q, want problem+json", ct)
			}
			if tt.expected.bodyContains != "" && !strings.Contains(rec.Body.String(), tt.expected.bodyContains) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tt.expected.bodyContains)
			}
			if tt.expected.bodyExcludes != "" && strings.Contains(rec.Body.String(), tt.expected.bodyExcludes) {
				t.Errorf("body %q leaks %q", rec.Body.String(), tt.expected.bodyExcludes)
			}
		})
	}

	t.Run("one map serves concurrent handlers lock-free", func(t *testing.T) {
		m := newErrorMap()

		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rec := httptest.NewRecorder()
				m.Respond(rec, fmt.Errorf("wrapped: %w", errNotFound))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
			}()
		}
		wg.Wait()
	})
}
