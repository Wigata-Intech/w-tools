package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Bind error sentinels, asserted with errors.Is. Size-limit violations
// surface as *http.MaxBytesError (use errors.As); an empty body is io.EOF.
var (
	ErrNotJSON       = errors.New("httpx: bind: content type is not JSON")
	ErrNoContentType = errors.New("httpx: bind: QUERY requires an explicit Content-Type (RFC 10008)")
	ErrTrailingData  = errors.New("httpx: bind: unexpected data after JSON body")
)

// BindOption adjusts a single Bind call.
type BindOption func(*bindOptions)

type bindOptions struct {
	maxBody int64
}

// MaxBody overrides the default request-body cap (DefaultMaxBind) for one
// Bind call.
func MaxBody(n int64) BindOption {
	return func(o *bindOptions) { o.maxBody = n }
}

// Bind decodes a JSON request body into v, capped at DefaultMaxBind bytes
// unless overridden. It reads the body, so it serves POST, PUT, PATCH and
// QUERY (RFC 10008) identically. A missing Content-Type is assumed JSON —
// except on QUERY, where RFC 10008 requires servers to fail requests
// without one (ErrNoContentType). An explicit non-JSON Content-Type is
// rejected with ErrNotJSON.
//
// Bind holds no ResponseWriter, so exceeding the cap does not mark the
// connection for closure the way the stdlib's 413 path does; a handler
// that wants that behavior sets "Connection: close" itself.
func Bind(r *http.Request, v any, opts ...BindOption) error {
	o := bindOptions{maxBody: DefaultMaxBind}
	for _, opt := range opts {
		opt(&o)
	}

	if r.Method == MethodQuery && r.Header.Get("Content-Type") == "" {
		return ErrNoContentType
	}

	if ct := r.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || (mt != "application/json" && !strings.HasSuffix(mt, "+json")) {
			return fmt.Errorf("%w: %q", ErrNotJSON, ct)
		}
	}

	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, o.maxBody))

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("httpx: bind: %w", err)
	}

	// Token, not More: More misses trailing close-delimiters ("}"/"]"),
	// which must be rejected the same as any other trailing data.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ErrTrailingData
	}

	return nil
}
