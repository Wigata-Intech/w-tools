package httpx

import (
	"encoding/json"
	"net/http"
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
}

// Respond writes the problem with its own status and defaults filled.
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

	// Marshal cannot fail here: every field is a plain string or int.
	b, _ := json.Marshal(p) //nolint:errchkjson // see above; a handled branch would be untestable dead code

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
