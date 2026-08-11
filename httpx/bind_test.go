package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
)

type bindPayload struct {
	Name string `json:"name"`
	Qty  int    `json:"qty"`
}

type bindInput struct {
	method      string // default POST
	contentType string
	body        string
	opts        []httpx.BindOption
}

type bindExpected struct {
	payload     bindPayload
	errIs       error // asserted with errors.Is when set
	maxBytesErr bool  // asserted as *http.MaxBytesError
	jsonSyntax  bool  // asserted as *json.SyntaxError
}

func TestMaxBody(t *testing.T) {
	tests := []struct {
		name     string
		input    bindInput
		expected bindExpected
	}{
		{
			name: "caps a single Bind call",
			input: bindInput{
				contentType: "application/json",
				body:        `{"name":"acehnese coffee","qty":2}`,
				opts:        []httpx.BindOption{httpx.MaxBody(4)},
			},
			expected: bindExpected{maxBytesErr: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBind(t, tt.input, tt.expected)
		})
	}
}

func TestBind(t *testing.T) {
	tests := []struct {
		name     string
		input    bindInput
		expected bindExpected
	}{
		{
			name: "decodes valid JSON",
			input: bindInput{
				contentType: "application/json",
				body:        `{"name":"kopi","qty":3}`,
			},
			expected: bindExpected{payload: bindPayload{Name: "kopi", Qty: 3}},
		},
		{
			name: "accepts +json content types",
			input: bindInput{
				contentType: "application/vnd.wigata+json",
				body:        `{"name":"kopi","qty":1}`,
			},
			expected: bindExpected{payload: bindPayload{Name: "kopi", Qty: 1}},
		},
		{
			name: "assumes JSON when content type is missing",
			input: bindInput{
				body: `{"name":"kopi","qty":1}`,
			},
			expected: bindExpected{payload: bindPayload{Name: "kopi", Qty: 1}},
		},
		{
			name: "decodes a body exactly at the cap",
			input: bindInput{
				contentType: "application/json",
				body:        `{"name":"kopi","qty":1}`,
				opts:        []httpx.BindOption{httpx.MaxBody(int64(len(`{"name":"kopi","qty":1}`)))},
			},
			expected: bindExpected{payload: bindPayload{Name: "kopi", Qty: 1}},
		},
		{
			name: "rejects QUERY without a content type per RFC 10008",
			input: bindInput{
				method: httpx.MethodQuery,
				body:   `{"name":"kopi","qty":1}`,
			},
			expected: bindExpected{errIs: httpx.ErrNoContentType},
		},
		{
			name: "rejects an explicit non-JSON content type",
			input: bindInput{
				contentType: "text/plain",
				body:        `{"name":"kopi","qty":1}`,
			},
			expected: bindExpected{errIs: httpx.ErrNotJSON},
		},
		{
			name: "rejects an unparseable content type",
			input: bindInput{
				contentType: ";;bad",
				body:        `{"name":"kopi","qty":1}`,
			},
			expected: bindExpected{errIs: httpx.ErrNotJSON},
		},
		{
			name: "rejects a body over the cap",
			input: bindInput{
				contentType: "application/json",
				body:        `{"name":"` + strings.Repeat("k", 32) + `","qty":1}`,
				opts:        []httpx.BindOption{httpx.MaxBody(16)},
			},
			expected: bindExpected{maxBytesErr: true},
		},
		{
			name: "rejects malformed JSON",
			input: bindInput{
				contentType: "application/json",
				body:        `{invalid}`,
			},
			expected: bindExpected{jsonSyntax: true},
		},
		{
			name: "rejects an empty body",
			input: bindInput{
				contentType: "application/json",
				body:        "",
			},
			expected: bindExpected{errIs: io.EOF},
		},
		{
			name: "rejects trailing data after the JSON body",
			input: bindInput{
				contentType: "application/json",
				body:        `{"name":"kopi","qty":1} {"again":true}`,
			},
			expected: bindExpected{errIs: httpx.ErrTrailingData},
		},
		{
			name: "rejects a trailing close-delimiter",
			input: bindInput{
				contentType: "application/json",
				body:        `{"name":"kopi","qty":1}}`,
			},
			expected: bindExpected{errIs: httpx.ErrTrailingData},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBind(t, tt.input, tt.expected)
		})
	}
}

func assertBind(t *testing.T, input bindInput, expected bindExpected) {
	t.Helper()

	method := input.method
	if method == "" {
		method = http.MethodPost
	}

	r := httptest.NewRequestWithContext(context.Background(), method, "/", strings.NewReader(input.body))
	if input.contentType != "" {
		r.Header.Set("Content-Type", input.contentType)
	}

	var got bindPayload
	err := httpx.Bind(r, &got, input.opts...)

	switch {
	case expected.errIs != nil:
		if !errors.Is(err, expected.errIs) {
			t.Fatalf("Bind() error = %v, want errors.Is %v", err, expected.errIs)
		}
	case expected.maxBytesErr:
		var mbe *http.MaxBytesError
		if !errors.As(err, &mbe) {
			t.Fatalf("Bind() error = %v, want *http.MaxBytesError", err)
		}
	case expected.jsonSyntax:
		var se *json.SyntaxError
		if !errors.As(err, &se) {
			t.Fatalf("Bind() error = %v, want *json.SyntaxError", err)
		}
	default:
		if err != nil {
			t.Fatalf("Bind() error = %v, want nil", err)
		}
		if got != expected.payload {
			t.Errorf("payload = %+v, want %+v", got, expected.payload)
		}
	}
}
