package httpx_test

import (
	"net/http"
	"net/http/httptest"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			tt.input.Respond(rec)

			assertRespond(t, rec, tt.expected)
		})
	}
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
