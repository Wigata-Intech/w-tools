package httpx_test

import (
	"context"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// stubRenderer satisfies httpx.Renderer the way any template engine does.
type stubRenderer struct {
	body   string
	err    error
	gotCtx context.Context //nolint:containedctx // captures the ctx Render passed, for assertion
}

func (s *stubRenderer) Render(ctx context.Context, w io.Writer) error {
	s.gotCtx = ctx
	if s.err != nil {
		return s.err
	}

	_, err := io.WriteString(w, s.body)

	return err
}

type ctxKey struct{}

var errRenderBoom = errors.New("render boom")

func TestRender(t *testing.T) {
	type renderExpected struct {
		status      int
		contentType string
		body        string
		err         error
	}

	tests := []struct {
		name     string
		input    *stubRenderer
		expected renderExpected
	}{
		{
			name:  "streams the component with status and content type",
			input: &stubRenderer{body: "<h1>dashboard</h1>"},
			expected: renderExpected{
				status:      http.StatusOK,
				contentType: "text/html; charset=utf-8",
				body:        "<h1>dashboard</h1>",
			},
		},
		{
			name:  "renderer errors surface to the caller after headers went out",
			input: &stubRenderer{err: errRenderBoom},
			expected: renderExpected{
				status:      http.StatusOK,
				contentType: "text/html; charset=utf-8",
				err:         errRenderBoom,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), ctxKey{}, "threaded")
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			err := httpx.Render(rec, req, http.StatusOK, tt.input)

			if !errors.Is(err, tt.expected.err) {
				t.Errorf("Render() error = %v, want %v", err, tt.expected.err)
			}
			if rec.Code != tt.expected.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.expected.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != tt.expected.contentType {
				t.Errorf("Content-Type = %q, want %q", ct, tt.expected.contentType)
			}
			if rec.Body.String() != tt.expected.body {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.expected.body)
			}
			if v, _ := tt.input.gotCtx.Value(ctxKey{}).(string); v != "threaded" {
				t.Error("renderer did not receive the request's context")
			}
		})
	}
}

func TestRenderFunc(t *testing.T) {
	tests := []struct {
		name     string
		input    string // body the func writes
		expected string
	}{
		{
			name:     "a plain function satisfies Renderer",
			input:    "<p>inline</p>",
			expected: "<p>inline</p>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			var gotCtx context.Context

			f := httpx.RenderFunc(func(ctx context.Context, w io.Writer) error {
				gotCtx = ctx //nolint:fatcontext // captures the ctx once for assertion, no growth
				_, err := io.WriteString(w, tt.input)

				return err
			})

			ctx := context.WithValue(context.Background(), ctxKey{}, "threaded")
			if err := f.Render(ctx, &buf); err != nil {
				t.Fatalf("Render() = %v, want nil", err)
			}

			if buf.String() != tt.expected {
				t.Errorf("body = %q, want %q", buf.String(), tt.expected)
			}
			if v, _ := gotCtx.Value(ctxKey{}).(string); v != "threaded" {
				t.Error("func did not receive the caller's context")
			}
		})
	}
}

func TestTemplate(t *testing.T) {
	tmpl := template.Must(template.New("page").Parse(`<p>hello {{.Name}}</p>`))

	type templateInput struct {
		name string
		data any
	}

	type templateExpected struct {
		body    string
		wantErr bool
	}

	tests := []struct {
		name     string
		input    templateInput
		expected templateExpected
	}{
		{
			name:     "executes the named template with data",
			input:    templateInput{name: "page", data: struct{ Name string }{Name: "dhira"}},
			expected: templateExpected{body: "<p>hello dhira</p>"},
		},
		{
			name:     "unknown template name errors",
			input:    templateInput{name: "missing", data: nil},
			expected: templateExpected{wantErr: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder

			err := httpx.Template(tmpl, tt.input.name, tt.input.data).Render(context.Background(), &buf)

			if (err != nil) != tt.expected.wantErr {
				t.Errorf("Render() error = %v, want error: %t", err, tt.expected.wantErr)
			}
			if buf.String() != tt.expected.body {
				t.Errorf("body = %q, want %q", buf.String(), tt.expected.body)
			}
		})
	}
}
