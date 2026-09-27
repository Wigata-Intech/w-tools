package httpx

import (
	"errors"
	"maps"
	"net/http"
)

// Problemer lets an error type carry its own Problem mapping; ErrorMap
// checks it before the registry, unwrapping as errors.As does.
type Problemer interface {
	Problem() Problem
}

// ErrorMap translates domain errors into Problem responses: register
// the service's error taxonomy once at startup, then handlers respond
// with one line. Build it before serving — it is read-only afterward,
// so the request path takes no locks.
type ErrorMap struct {
	// Enrich, when set, adjusts every problem RespondRequest writes —
	// Problemer, registry match, and bare 500 alike — before it goes out,
	// typically adding request-scoped Extensions such as a request ID.
	// p is a private copy whose Extensions map is non-nil and owned by
	// this call. Nil leaves problems untouched; Respond never calls it.
	Enrich func(r *http.Request, p *Problem)

	entries []errorMapping
}

type errorMapping struct {
	target  error
	problem Problem
}

// NewErrorMap returns an empty map; unmapped errors respond as a bare
// 500.
func NewErrorMap() *ErrorMap {
	return &ErrorMap{}
}

// Map registers a translation: when errors.Is(err, target), respond
// with p. Entries match in registration order; the first match wins.
// A Problemer anywhere in the error tree always wins over the registry —
// an error that describes itself cannot be overridden by registration.
func (m *ErrorMap) Map(target error, p Problem) {
	m.entries = append(m.entries, errorMapping{target: target, problem: p})
}

// Respond writes the Problem for err, checking in order: the error's
// own Problemer, the registry via errors.Is, then a bare 500 —
// deliberately without err.Error(), which leaks internals into
// responses.
func (m *ErrorMap) Respond(w http.ResponseWriter, err error) {
	m.problem(err).Respond(w)
}

// RespondRequest is Respond with the request in hand: the Problem is
// resolved exactly as Respond resolves it, then passed through Enrich
// when one is set.
func (m *ErrorMap) RespondRequest(w http.ResponseWriter, r *http.Request, err error) {
	p := m.problem(err)
	if m.Enrich != nil {
		p.Extensions = maps.Clone(p.Extensions)
		if p.Extensions == nil {
			p.Extensions = map[string]any{}
		}
		m.Enrich(r, &p)
	}

	p.Respond(w)
}

func (m *ErrorMap) problem(err error) Problem {
	var p Problemer
	if errors.As(err, &p) {
		return p.Problem()
	}

	for _, e := range m.entries {
		if errors.Is(err, e.target) {
			return e.problem
		}
	}

	return Problem{Status: http.StatusInternalServerError}
}
