package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// DuplicatePolicy decides what a completed duplicate request receives.
type DuplicatePolicy int

const (
	// Replay serves the stored response — same status, headers, and
	// body as the first execution — marked "Idempotency-Replayed: true".
	Replay DuplicatePolicy = iota

	// Reject answers duplicates with IdempotencyConfig.RejectStatus
	// instead of the stored response.
	Reject
)

// Store is the key-value persistence behind Idempotency. Each method
// maps 1:1 to a Redis command — SetNX = SET NX PX, Get = GET,
// Set = SET XX KEEPTTL, Delete = DEL — so a Redis-backed
// implementation makes the middleware distributed with no other
// change. Values are opaque bytes owned by the middleware. MemoryStore
// is the in-package implementation.
type Store interface {
	// SetNX stores value under key with ttl iff key is absent,
	// reporting whether it stored. Of N concurrent SetNX calls for an
	// absent key, exactly one may report true.
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)

	// Get returns the value under key; ok is false when absent.
	Get(ctx context.Context, key string) (value []byte, ok bool, err error)

	// Set replaces the value under an existing key, keeping its TTL.
	// Setting an absent (or expired) key is a no-op.
	Set(ctx context.Context, key string, value []byte) error

	// Delete removes key.
	Delete(ctx context.Context, key string) error
}

// record is one idempotency key's stored state, JSON-encoded into the
// Store: the claim while the original executes, the response after.
type record struct {
	Fingerprint []byte      `json:"fp"`
	InFlight    bool        `json:"in_flight,omitempty"`
	Status      int         `json:"status,omitempty"`
	Header      http.Header `json:"header,omitempty"`
	Body        []byte      `json:"body,omitempty"`
}

// IdempotencyConfig configures Idempotency.
type IdempotencyConfig struct {
	// Store persists records. Required; Idempotency panics at
	// construction without one.
	Store Store

	// Prefix namespaces every store key: "<Prefix><Idempotency-Key>".
	// Set it per service or route group when stores are shared.
	Prefix string

	// Header carries the client's key. Default DefaultIdempotencyHeader.
	Header string

	// Methods covered by the middleware. Default POST only.
	Methods []string

	// TTL bounds a record's lifetime. Default DefaultIdempotencyTTL.
	TTL time.Duration

	// Required rejects covered requests that lack the header with 400.
	// Default false: no header, the middleware passes through.
	Required bool

	// MaxBodyBytes caps the captured response body. A larger response
	// is served normally but not stored — the claim is released so a
	// retry re-executes. Default DefaultIdempotencyMaxBody.
	MaxBodyBytes int

	// MaxRequestBodyBytes caps how much request body is read for
	// fingerprinting; a larger request is refused with 413 before the
	// handler runs. Default DefaultIdempotencyMaxBody.
	MaxRequestBodyBytes int64

	// Policy decides what a completed duplicate receives. Default Replay.
	Policy DuplicatePolicy

	InFlightStatus int // duplicate while the original executes; default 409
	MismatchStatus int // same key, different request; default 422
	RejectStatus   int // Policy == Reject; default 409

	ErrorWriter httpx.ErrorWriter // nil = RFC 9457 Problem
}

// Idempotency returns a middleware guaranteeing at-most-once handler
// execution per idempotency key within the TTL. The first request
// bearing a key executes and its response is stored; duplicates receive
// the stored response (or RejectStatus, per Policy), a duplicate racing
// the original gets InFlightStatus, and a key reused with a different
// request gets MismatchStatus.
//
// Only observed completions are stored: a 5xx, a panic, or an oversized
// body releases the claim so a retry re-executes. Store errors fail
// closed — the request is refused rather than risking a duplicate
// execution.
func Idempotency(cfg IdempotencyConfig) httpx.Middleware {
	if cfg.Store == nil {
		panic("middleware: Idempotency needs a Store")
	}

	header := cfg.Header
	if header == "" {
		header = DefaultIdempotencyHeader
	}

	methods := slices.Clone(cfg.Methods)
	if methods == nil {
		methods = []string{http.MethodPost}
	}
	for i, m := range methods {
		methods[i] = strings.ToUpper(m)
	}

	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultIdempotencyTTL
	}

	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultIdempotencyMaxBody
	}

	maxReqBody := cfg.MaxRequestBodyBytes
	if maxReqBody <= 0 {
		maxReqBody = DefaultIdempotencyMaxBody
	}

	inFlightStatus := defaultStatus(cfg.InFlightStatus, http.StatusConflict)
	mismatchStatus := defaultStatus(cfg.MismatchStatus, http.StatusUnprocessableEntity)
	rejectStatus := defaultStatus(cfg.RejectStatus, http.StatusConflict)

	errorWriter := cfg.ErrorWriter
	if errorWriter == nil {
		errorWriter = func(w http.ResponseWriter, _ *http.Request, status int, detail string) {
			httpx.Error(w, status, detail)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !slices.Contains(methods, r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get(header)
			if key == "" {
				if cfg.Required {
					errorWriter(w, r, http.StatusBadRequest, "missing "+header+" header")
					return
				}

				next.ServeHTTP(w, r)

				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, maxReqBody)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					errorWriter(w, r, http.StatusRequestEntityTooLarge, "request body exceeds the idempotency fingerprint cap")
					return
				}

				errorWriter(w, r, http.StatusBadRequest, "reading request body failed")

				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			fp := fingerprint(r.Method, r.URL.RequestURI(), body)
			storeKey := cfg.Prefix + key

			claim, _ := json.Marshal(record{Fingerprint: fp, InFlight: true})
			claimed, err := cfg.Store.SetNX(r.Context(), storeKey, claim, ttl)
			if err != nil {
				errorWriter(w, r, http.StatusServiceUnavailable, "idempotency store unavailable")
				return
			}

			if !claimed {
				answerDuplicate(w, r, cfg, storeKey, fp, errorWriter, mismatchStatus, inFlightStatus, rejectStatus)
				return
			}

			// Post-response store bookkeeping must survive the client
			// disconnecting, so it runs on an uncancelable context.
			bg := context.WithoutCancel(r.Context())

			cw := &captureWriter{ResponseWriter: w, max: maxBody}
			defer func() {
				if p := recover(); p != nil {
					_ = cfg.Store.Delete(bg, storeKey)
					panic(p)
				}
			}()

			next.ServeHTTP(cw, r)

			status := cw.status
			if status == 0 {
				status = http.StatusOK
			}

			if status >= http.StatusInternalServerError || cw.truncated {
				_ = cfg.Store.Delete(bg, storeKey)
				return
			}

			stored, _ := json.Marshal(record{
				Fingerprint: fp,
				Status:      status,
				Header:      storableHeader(w.Header()),
				Body:        bytes.Clone(cw.body.Bytes()),
			})
			// A failed Set leaves the claim in place: duplicates get
			// InFlightStatus until the TTL — never a re-execution.
			_ = cfg.Store.Set(bg, storeKey, stored)
		})
	}
}

// answerDuplicate resolves a request whose key already exists in the
// store: mismatch, still in flight, reject, or replay.
func answerDuplicate(w http.ResponseWriter, r *http.Request, cfg IdempotencyConfig, storeKey string, fp []byte,
	errorWriter httpx.ErrorWriter, mismatchStatus, inFlightStatus, rejectStatus int,
) {
	val, ok, err := cfg.Store.Get(r.Context(), storeKey)
	if err != nil {
		errorWriter(w, r, http.StatusServiceUnavailable, "idempotency store unavailable")
		return
	}
	if !ok {
		// The record vanished between SetNX and Get (expired or
		// released); the client retries rather than racing for it.
		errorWriter(w, r, inFlightStatus, "original request still in progress")

		return
	}

	var rec record
	if json.Unmarshal(val, &rec) != nil {
		errorWriter(w, r, http.StatusServiceUnavailable, "idempotency record corrupted")
		return
	}

	switch {
	case !bytes.Equal(rec.Fingerprint, fp):
		errorWriter(w, r, mismatchStatus, "idempotency key reused with a different request")
	case rec.InFlight:
		errorWriter(w, r, inFlightStatus, "original request still in progress")
	case cfg.Policy == Reject:
		errorWriter(w, r, rejectStatus, "request already completed")
	default:
		replayResponse(w, rec)
	}
}

func defaultStatus(status, fallback int) int {
	if status == 0 {
		return fallback
	}

	return status
}

// fingerprint hashes what identifies a request for reuse detection.
// The NUL separators keep (method, uri, body) splits unambiguous.
func fingerprint(method, uri string, body []byte) []byte {
	h := sha256.New()
	io.WriteString(h, method) //nolint:errcheck,gosec // hash.Hash writes never fail
	h.Write([]byte{0})
	io.WriteString(h, uri) //nolint:errcheck,gosec // hash.Hash writes never fail
	h.Write([]byte{0})
	h.Write(body)

	return h.Sum(nil)
}

// perResponseHeaders never replay: hop-by-hop per RFC 9110 §7.6.1, plus
// headers the replaying response computes itself.
var perResponseHeaders = []string{ //nolint:gochecknoglobals // package constant in slice form
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Date", "Content-Length",
}

func storableHeader(h http.Header) http.Header {
	stored := make(http.Header, len(h))
	for k, vv := range h {
		if slices.Contains(perResponseHeaders, http.CanonicalHeaderKey(k)) {
			continue
		}
		stored[k] = slices.Clone(vv)
	}

	return stored
}

func replayResponse(w http.ResponseWriter, rec record) {
	h := w.Header()
	for k, vv := range rec.Header {
		h[k] = slices.Clone(vv)
	}
	h.Set("Idempotency-Replayed", "true")
	w.WriteHeader(rec.Status)
	w.Write(rec.Body) //nolint:errcheck,gosec // nothing to do for a client gone mid-replay
}

// captureWriter tees the response for storage: status, and body up to
// max — one byte past it flips truncated and capture stops.
type captureWriter struct {
	http.ResponseWriter

	status    int
	body      bytes.Buffer
	max       int
	truncated bool
}

func (w *captureWriter) WriteHeader(status int) {
	if w.status == 0 && status >= http.StatusOK {
		w.status = status
	}

	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	if !w.truncated {
		if room := w.max - w.body.Len(); len(b) <= room {
			w.body.Write(b)
		} else {
			w.truncated = true
		}
	}

	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
