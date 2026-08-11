package httpx

import (
	"errors"
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
	var p Problemer
	if errors.As(err, &p) {
		p.Problem().Respond(w)
		return
	}

	for _, e := range m.entries {
		if errors.Is(err, e.target) {
			e.problem.Respond(w)
			return
		}
	}

	Problem{Status: http.StatusInternalServerError}.Respond(w)
}
