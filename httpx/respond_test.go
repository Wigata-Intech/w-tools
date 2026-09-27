package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
)

type respondExpected struct {
	status      int
	contentType string
	body        string
}

func assertRespond(t *testing.T, rec *httptest.ResponseRecorder, expected respondExpected) {
	t.Helper()

	if rec.Code != expected.status {
		t.Errorf("status = %d, want %d", rec.Code, expected.status)
	}
	if ct := rec.Header().Get("Content-Type"); ct != expected.contentType {
		t.Errorf("Content-Type = %q, want %q", ct, expected.contentType)
	}
	if rec.Body.String() != expected.body {
		t.Errorf("body = %q, want %q", rec.Body.String(), expected.body)
	}
}

func TestJSON(t *testing.T) {
	type payload struct {
		ID    string `json:"id"`
		Total int    `json:"total"`
	}

	tests := []struct {
		name     string
		input    any
		expected respondExpected
	}{
		{
			name:  "marshals the value with status and content type",
			input: payload{ID: "ord_1", Total: 42},
			expected: respondExpected{
				status:      http.StatusCreated,
				contentType: "application/json",
				body:        `{"id":"ord_1","total":42}`,
			},
		},
		{
			name:  "unmarshalable value degrades to a 500 problem",
			input: make(chan int),
			expected: respondExpected{
				status:      http.StatusInternalServerError,
				contentType: "application/problem+json",
				body:        `{"type":"about:blank","title":"Internal Server Error","status":500,"detail":"response encoding failed"}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			httpx.JSON(rec, http.StatusCreated, tt.input)

			assertRespond(t, rec, tt.expected)
		})
	}
}

func TestProblemMarshalJSON(t *testing.T) {
	type marshalExpected struct {
		body        string
		unsupported bool // a *json.UnsupportedTypeError is wanted
	}

	tests := []struct {
		name     string
		input    httpx.Problem
		expected marshalExpected
	}{
		{
			name: "without extensions the encoding is the plain struct's",
			input: httpx.Problem{
				Type:     "https://example.com/problems/insufficient-funds",
				Title:    "Insufficient Funds",
				Status:   http.StatusUnprocessableEntity,
				Detail:   "balance is 30, cost is 50",
				Instance: "/orders/ord_1",
			},
			expected: marshalExpected{
				body: `{"type":"https://example.com/problems/insufficient-funds","title":"Insufficient Funds","status":422,"detail":"balance is 30, cost is 50","instance":"/orders/ord_1"}`,
			},
		},
		{
			name:     "defaults are not filled",
			input:    httpx.Problem{},
			expected: marshalExpected{body: `{"status":0}`},
		},
		{
			name: "an empty extensions map encodes like none",
			input: httpx.Problem{
				Status:     http.StatusNotFound,
				Extensions: map[string]any{},
			},
			expected: marshalExpected{body: `{"status":404}`},
		},
		{
			name: "extensions only naming standard members encode like none",
			input: httpx.Problem{
				Title:      "Not Found",
				Status:     http.StatusNotFound,
				Extensions: map[string]any{"status": 200, "title": "OK"},
			},
			expected: marshalExpected{body: `{"title":"Not Found","status":404}`},
		},
		{
			name: "extensions follow the standard members at the top level, sorted by key",
			input: httpx.Problem{
				Type:   "about:blank",
				Title:  "Not Found",
				Status: http.StatusNotFound,
				Extensions: map[string]any{
					"request_id": "abc",
					"balance":    30,
					"errors":     []string{"a", "b"},
				},
			},
			expected: marshalExpected{
				body: `{"type":"about:blank","title":"Not Found","status":404,"balance":30,"errors":["a","b"],"request_id":"abc"}`,
			},
		},
		{
			name: "standard members win over same-named extensions, compared case-insensitively",
			input: httpx.Problem{
				Type:   "about:blank",
				Title:  "Not Found",
				Status: http.StatusNotFound,
				Extensions: map[string]any{
					"type":     "https://evil.example",
					"Title":    "OK",
					"STATUS":   200,
					"detail":   "overridden",
					"Instance": "/elsewhere",
					"code":     "E404",
				},
			},
			expected: marshalExpected{
				body: `{"type":"about:blank","title":"Not Found","status":404,"code":"E404"}`,
			},
		},
		{
			name: "an unmarshalable extension value is an error",
			input: httpx.Problem{
				Status:     http.StatusBadRequest,
				Extensions: map[string]any{"bad": make(chan int)},
			},
			expected: marshalExpected{unsupported: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := tt.input.MarshalJSON()

			var unsupported *json.UnsupportedTypeError
			if got := errors.As(err, &unsupported); got != tt.expected.unsupported {
				t.Fatalf("MarshalJSON() error = %v, want UnsupportedTypeError: %t", err, tt.expected.unsupported)
			}
			if string(b) != tt.expected.body {
				t.Errorf("MarshalJSON() = %s, want %s", b, tt.expected.body)
			}
		})
	}
}

func TestProblemRespond(t *testing.T) {
	tests := []struct {
		name     string
		input    httpx.Problem
		expected respondExpected
	}{
		{
			name: "explicit fields pass through untouched",
			input: httpx.Problem{
				Type:     "https://wigataintech.com/problems/insufficient-funds",
				Title:    "Insufficient Funds",
				Status:   http.StatusUnprocessableEntity,
				Detail:   "balance is 30, cost is 50",
				Instance: "/orders/ord_1",
			},
			expected: respondExpected{
				status:      http.StatusUnprocessableEntity,
				contentType: "application/problem+json",
				body:        `{"type":"https://wigataintech.com/problems/insufficient-funds","title":"Insufficient Funds","status":422,"detail":"balance is 30, cost is 50","instance":"/orders/ord_1"}`,
			},
		},
		{
			name:  "zero value fills every default",
			input: httpx.Problem{},
			expected: respondExpected{
				status:      http.StatusInternalServerError,
				contentType: "application/problem+json",
				body:        `{"type":"about:blank","title":"Internal Server Error","status":500}`,
			},
		},
		{
			name: "extensions are written at the top level after the filled defaults",
			input: httpx.Problem{
				Status:     http.StatusNotFound,
				Extensions: map[string]any{"request_id": "abc"},
			},
			expected: respondExpected{
				status:      http.StatusNotFound,
				contentType: "application/problem+json",
				body:        `{"type":"about:blank","title":"Not Found","status":404,"request_id":"abc"}`,
			},
		},
		{
			name: "extensions never override the filled defaults",
			input: httpx.Problem{
				Status:     http.StatusNotFound,
				Extensions: map[string]any{"type": "https://evil.example", "title": "OK", "status": 200},
			},
			expected: respondExpected{
				status:      http.StatusNotFound,
				contentType: "application/problem+json",
				body:        `{"type":"about:blank","title":"Not Found","status":404}`,
			},
		},
		{
			name: "an unmarshalable extension drops the extensions, keeping the problem",
			input: httpx.Problem{
				Status:     http.StatusBadRequest,
				Extensions: map[string]any{"bad": make(chan int), "request_id": "abc"},
			},
			expected: respondExpected{
				status:      http.StatusBadRequest,
				contentType: "application/problem+json",
				body:        `{"type":"about:blank","title":"Bad Request","status":400}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			tt.input.Respond(rec)

			assertRespond(t, rec, tt.expected)
		})
	}

	t.Run("a stale Content-Length from an earlier writer is removed", func(t *testing.T) {
		const want = `{"type":"about:blank","title":"Not Found","status":404,"request_id":"abc"}`

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "5")
			httpx.Problem{Status: http.StatusNotFound, Extensions: map[string]any{"request_id": "abc"}}.Respond(w)
		}))
		defer srv.Close()

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read: %v", err)
		}

		if string(body) != want {
			t.Errorf("body = %q, want %q", body, want)
		}
		if !json.Valid(body) {
			t.Errorf("body %q is not valid JSON", body)
		}
		if cl := resp.Header.Get("Content-Length"); cl != strconv.Itoa(len(want)) {
			t.Errorf("Content-Length = %q, want %d — the stale 5 must not survive", cl, len(want))
		}
	})
}

func TestError(t *testing.T) {
	tests := []struct {
		name     string
		input    string // detail
		expected respondExpected
	}{
		{
			name:  "writes the minimal problem for the status",
			input: "order ord_404 does not exist",
			expected: respondExpected{
				status:      http.StatusNotFound,
				contentType: "application/problem+json",
				body:        `{"type":"about:blank","title":"Not Found","status":404,"detail":"order ord_404 does not exist"}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			httpx.Error(rec, http.StatusNotFound, tt.input)

			assertRespond(t, rec, tt.expected)
		})
	}
}
