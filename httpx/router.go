package httpx

import (
	"net/http"
	"slices"
	"strings"
)

// Middleware is the standard chain shape. A type alias on purpose: any
// func(http.Handler) http.Handler — yours, chi's, the ecosystem's — is
// assignment-compatible without conversion.
type Middleware = func(http.Handler) http.Handler

// Group registers routes under a shared prefix and middleware chain.
// Groups are registration-time sugar: at request time there is only the
// one underlying ServeMux, so grouping costs nothing per request.
type Group struct {
	mux    *http.ServeMux
	prefix string
	chain  []Middleware
}

// Group returns a child group. Its prefix appends to the parent's and its
// chain extends the parent's; the parent is never mutated. Prefixes are
// joined verbatim, so pass them without a trailing slash: "/api", "/v1".
func (g *Group) Group(prefix string, mw ...Middleware) *Group {
	chain := make([]Middleware, 0, len(g.chain)+len(mw))
	chain = append(chain, g.chain...)
	chain = append(chain, mw...)

	return &Group{mux: g.mux, prefix: g.prefix + prefix, chain: chain}
}

// Get registers a GET handler for the pattern.
func (g *Group) Get(pattern string, h http.HandlerFunc) {
	g.register(http.MethodGet, pattern, h)
}

// Post registers a POST handler for the pattern.
func (g *Group) Post(pattern string, h http.HandlerFunc) {
	g.register(http.MethodPost, pattern, h)
}

// Put registers a PUT handler for the pattern.
func (g *Group) Put(pattern string, h http.HandlerFunc) {
	g.register(http.MethodPut, pattern, h)
}

// Patch registers a PATCH handler for the pattern.
func (g *Group) Patch(pattern string, h http.HandlerFunc) {
	g.register(http.MethodPatch, pattern, h)
}

// Delete registers a DELETE handler for the pattern.
func (g *Group) Delete(pattern string, h http.HandlerFunc) {
	g.register(http.MethodDelete, pattern, h)
}

// Head registers a HEAD handler for the pattern.
func (g *Group) Head(pattern string, h http.HandlerFunc) {
	g.register(http.MethodHead, pattern, h)
}

// Options registers an OPTIONS handler for the pattern.
func (g *Group) Options(pattern string, h http.HandlerFunc) {
	g.register(http.MethodOptions, pattern, h)
}

// Query registers a QUERY (RFC 10008) handler for the pattern.
func (g *Group) Query(pattern string, h http.HandlerFunc) {
	g.register(MethodQuery, pattern, h)
}

// Handle is the escape hatch for anything the typed helpers don't cover.
// Its signature mirrors ServeMux.Handle exactly: the pattern may carry its
// own method token, as in g.Handle("PROPFIND /dav/{path...}", h).
func (g *Group) Handle(pattern string, h http.Handler) {
	if i := strings.IndexAny(pattern, " \t"); i >= 0 {
		g.register(pattern[:i], strings.TrimLeft(pattern[i+1:], " \t"), h)
		return
	}

	g.register("", pattern, h)
}

// HandleFunc is Handle for a plain handler func, mirroring ServeMux.
func (g *Group) HandleFunc(pattern string, h http.HandlerFunc) {
	g.Handle(pattern, h)
}

func (g *Group) register(method, pattern string, h http.Handler) {
	p := g.prefix + pattern
	if method != "" {
		p = method + " " + p
	}

	// Wrap at registration, outermost first; ServeMux precedence and its
	// duplicate-pattern panic stay exactly as the stdlib defines them.
	for _, mw := range slices.Backward(g.chain) {
		h = mw(h)
	}

	g.mux.Handle(p, h)
}
