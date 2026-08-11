package httpx

import (
	"context"
	"html/template"
	"io"
	"net/http"
)

// Renderer is anything that can stream itself as HTML. templ components
// satisfy it natively (identical method); other engines adapt in a few
// lines — httpx never imports one.
type Renderer interface {
	Render(ctx context.Context, w io.Writer) error
}

// Render writes an HTML response, streaming c with the request's
// context so a canceled request stops rendering. By the time c can
// fail the status line is already gone, so the returned error is for
// logging — never for writing a second response.
func Render(w http.ResponseWriter, r *http.Request, status int, c Renderer) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	return c.Render(r.Context(), w)
}

// RenderFunc adapts a plain function to Renderer, the way
// http.HandlerFunc adapts handlers.
type RenderFunc func(ctx context.Context, w io.Writer) error

// Render calls f.
func (f RenderFunc) Render(ctx context.Context, w io.Writer) error {
	return f(ctx, w)
}

// Template adapts html/template to Renderer. The context is ignored:
// html/template has no cancellation of its own.
func Template(t *template.Template, name string, data any) Renderer { //nolint:ireturn // an adapter's whole job is returning the interface
	return RenderFunc(func(_ context.Context, w io.Writer) error {
		return t.ExecuteTemplate(w, name, data)
	})
}
