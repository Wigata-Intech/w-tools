package httpx

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

// JSON writes v as an application/json response with the given status.
// If v cannot be marshaled, a 500 Problem is written instead — the
// encoding failure surfaces before any header goes out, never as a
// half-written body.
func JSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		Error(w, http.StatusInternalServerError, "response encoding failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// Problem is an RFC 9457 error response (application/problem+json).
// The helpers fill sensible defaults; fill the struct yourself for a
// richer error taxonomy — the struct is the API, Error is convenience.
type Problem struct {
	Type     string `json:"type,omitempty"`     // URI reference; default "about:blank"
	Title    string `json:"title,omitempty"`    // short, stable per Type; default from status code
	Status   int    `json:"status"`             // HTTP status; default 500
	Detail   string `json:"detail,omitempty"`   // occurrence-specific explanation
	Instance string `json:"instance,omitempty"` // URI of this occurrence

	// Extensions are extension members (RFC 9457 §3.2). Respond writes
	// them at the top level of the object after the standard members,
	// sorted by key; a key naming a standard member, compared
	// case-insensitively, is ignored — the standard members always win.
	// json.Marshal of a Problem omits them.
	Extensions map[string]any `json:"-"`
}

// marshalProblem encodes the standard members in declaration order, then
// the Extensions sorted by key. Without extensions the output is exactly
// the struct's plain encoding. It fails only when an extension value
// cannot be marshaled. Problem has no MarshalJSON method, so a struct
// embedding it keeps encoding its own fields.
func marshalProblem(p Problem) ([]byte, error) {
	// Marshal cannot fail here: every encoded field is a plain string or int.
	b, _ := json.Marshal(p) //nolint:errchkjson // see above; a handled branch would be untestable dead code
	if len(p.Extensions) == 0 {
		return b, nil
	}

	standardMembers := []string{"type", "title", "status", "detail", "instance"}
	ext := make(map[string]any, len(p.Extensions))
	for k, v := range p.Extensions {
		if !slices.ContainsFunc(standardMembers, func(s string) bool { return strings.EqualFold(s, k) }) {
			ext[k] = v
		}
	}
	if len(ext) == 0 {
		return b, nil
	}

	e, err := json.Marshal(ext) // map keys encode sorted
	if err != nil {
		return nil, err
	}

	b[len(b)-1] = ','

	return append(b, e[1:]...), nil
}

// Respond writes the problem with its own status and defaults filled. A
// Content-Length header set earlier is removed so it cannot contradict
// the body. If an extension value cannot be marshaled, the problem is
// written without its extensions.
func (p Problem) Respond(w http.ResponseWriter) {
	if p.Status == 0 {
		p.Status = http.StatusInternalServerError
	}
	if p.Type == "" {
		p.Type = "about:blank"
	}
	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}

	b, err := marshalProblem(p)
	if err != nil {
		p.Extensions = nil
		// Marshal cannot fail here: without extensions every field is a plain string or int.
		b, _ = json.Marshal(p) //nolint:errchkjson // see above; a handled branch would be untestable dead code
	}

	w.Header().Del("Content-Length")
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_, _ = w.Write(b)
}

// Error writes a minimal RFC 9457 response: the status, its canonical
// title, and the given detail.
func Error(w http.ResponseWriter, status int, detail string) {
	Problem{Status: status, Detail: detail}.Respond(w)
}

// ErrorWriter swaps the RFC 9457 default anywhere httpx itself writes an
// error on a service's behalf (middleware such as Recover and RateLimit).
// Nil always means Problem JSON.
type ErrorWriter func(w http.ResponseWriter, r *http.Request, status int, detail string)
