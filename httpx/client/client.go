package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

// Breaker is the circuit-breaker hook, consulted per request when set.
// x/circuitbreaker implements it structurally; any breaker with this
// shape wires in — this package never imports one.
type Breaker interface {
	Allow() error
	Record(err error)
}

// ErrCircuitOpen is returned by Do before the network is touched when
// the breaker rejects the attempt; the breaker's own error is wrapped
// alongside it.
var ErrCircuitOpen = errors.New("httpx/client: circuit open")

// Config configures New. Every zero value is a production default.
type Config struct {
	// Timeout is the total per-attempt time. Zero means DefaultTimeout.
	Timeout time.Duration

	MaxIdleConnsPerHost int
	IdleConnTimeout     time.Duration

	// TLS overrides the default TLS settings (TLS 1.2 floor, session
	// resumption cache). Set it for internal CAs (RootCAs) or mTLS
	// client certificates (Certificates); when set it is used as-is —
	// the caller owns it fully. Public APIs need nothing here: servers
	// present their certificates and the OS trust store verifies them.
	TLS *tls.Config

	// Breaker is consulted per request when set. Nil = no breaking.
	Breaker Breaker

	// Log enables outbound logging: nil is silent, set means one line
	// per call. Redaction travels inside the handler, so a logger built
	// by w-tools/logger applies its rules to query params and captured
	// bodies automatically.
	Log *slog.Logger

	// Body capture is off by default. Captured JSON bodies are logged
	// as structured attrs; non-JSON bodies log as size only, never raw.
	// Response capture never gates the caller: only bodies with a
	// declared Content-Length within MaxBody are read; streaming and
	// chunked responses are never touched.
	LogRequestBody  bool
	LogResponseBody bool

	// MaxBody caps each captured body. Default httpx.DefaultMaxBody.
	MaxBody int
}

// Client is an outbound HTTP client with tuned pooling. Use it like
// http.Client: Do for full control, the ctx-first verbs for convenience.
type Client struct {
	hc      *http.Client
	breaker Breaker
	log     *slog.Logger
	logReq  bool
	logResp bool
	maxBody int
}

// New returns a Client ready for production traffic: keep-alive pooling
// sized for real services, TLS session resumption, and a hard timeout.
func New(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxIdleConnsPerHost == 0 {
		cfg.MaxIdleConnsPerHost = DefaultMaxIdleConnsPerHost
	}
	if cfg.IdleConnTimeout == 0 {
		cfg.IdleConnTimeout = DefaultIdleConnTimeout
	}
	maxBody := cfg.MaxBody
	if maxBody <= 0 {
		maxBody = httpx.DefaultMaxBody
	}

	tlsConfig := cfg.TLS
	if tlsConfig == nil {
		tlsConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ClientSessionCache: tls.NewLRUClientSessionCache(64),
		}
	}

	// DefaultTransport's documented shape, with the pooling knobs and
	// TLS session resumption a service client actually needs.
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          0, // no total cap; the per-host limit is the policy
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}

	return &Client{
		hc:      &http.Client{Transport: tr, Timeout: cfg.Timeout},
		breaker: cfg.Breaker,
		log:     cfg.Log,
		logReq:  cfg.LogRequestBody,
		logResp: cfg.LogResponseBody,
		maxBody: maxBody,
	}
}

// Do sends the request. The breaker, when set, is consulted before the
// network and told the transport outcome after — a received response of
// any status records success; status policy stays with the caller.
// A trace carried by the request context (middleware.Trace) is
// propagated as a traceparent header with a freshly minted span id,
// unless the caller already set one.
//
// Like http.Client.Do, this consumes and may replace req.Body; the
// request must not be reused.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c.breaker != nil {
		if err := c.breaker.Allow(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCircuitOpen, err)
		}
	}

	ctx := req.Context()
	if traceID := middleware.TraceIDFrom(ctx); traceID != "" && req.Header.Get("Traceparent") == "" {
		if spanID, ok := randomHex(8); ok {
			// Only middleware.Trace writes these ctx keys, and it always
			// stores flags alongside the trace id.
			req.Header.Set("Traceparent", "00-"+traceID+"-"+spanID+"-"+middleware.TraceFlagsFrom(ctx))
		}
	}

	var reqCap *capture
	if c.log != nil && c.logReq && req.Body != nil {
		reqCap = &capture{max: c.maxBody}
		req.Body = teeBody{Reader: io.TeeReader(req.Body, reqCap), Closer: req.Body}
	}

	start := time.Now()
	resp, err := c.hc.Do(req)
	elapsed := time.Since(start)

	if c.breaker != nil {
		c.breaker.Record(err)
	}

	if c.log != nil {
		c.emit(req, resp, err, elapsed, reqCap)
	}

	return resp, err //nolint:wrapcheck // transparent proxy: http.Client already wraps with *url.Error
}

// Get issues a GET to url.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	return c.verb(ctx, http.MethodGet, url, "", nil)
}

// Post issues a POST with the given body.
func (c *Client) Post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	return c.verb(ctx, http.MethodPost, url, contentType, body)
}

// Query issues a QUERY (RFC 10008): a safe, idempotent query carried in
// the request body.
func (c *Client) Query(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	return c.verb(ctx, httpx.MethodQuery, url, contentType, body)
}

func (c *Client) verb(ctx context.Context, method, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("client: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	return c.Do(req)
}

// emit writes the outbound access line. The URL's query string is
// logged as a parsed map — not a raw string — so the logging handler's
// redaction rules apply to sensitive parameters; the error line
// likewise never embeds the URL, which would smuggle the query past
// those rules.
func (c *Client) emit(req *http.Request, resp *http.Response, err error, elapsed time.Duration, reqCap *capture) {
	// After redirects the response carries the final URL; log that one.
	u := req.URL
	if resp != nil && resp.Request != nil {
		u = resp.Request.URL
	}

	attrs := make([]any, 0, 16)
	attrs = append(attrs,
		"method", req.Method,
		"url", u.Scheme+"://"+u.Host+u.Path,
	)
	if q := u.Query(); len(q) > 0 {
		attrs = append(attrs, "query", map[string][]string(q))
	}
	attrs = append(attrs, "duration_ms", float64(elapsed.Microseconds())/1000.0)
	if traceID := middleware.TraceIDFrom(req.Context()); traceID != "" {
		attrs = append(attrs, "trace_id", traceID)
	}
	if reqCap != nil {
		attrs = append(attrs, bodyAttrs("request_body", reqCap.buf.Bytes(), req.Header.Get("Content-Type"), reqCap.truncated)...)
	}

	if err != nil {
		msg := err.Error()
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			msg = urlErr.Err.Error() // the URL (and its query) already ride in dedicated attrs
		}
		attrs = append(attrs, "error", msg)
		c.log.ErrorContext(req.Context(), "outbound request failed", attrs...)

		return
	}

	attrs = append(attrs, "status", resp.StatusCode)
	if c.logResp {
		attrs = append(attrs, captureResponse(resp, c.maxBody)...)
	}

	c.log.InfoContext(req.Context(), "outbound request", attrs...)
}

// capture is an io.Writer that keeps at most max bytes and marks the
// overflow, for use as a TeeReader sink.
type capture struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (cp *capture) Write(b []byte) (int, error) {
	if !cp.truncated {
		if room := cp.max - cp.buf.Len(); len(b) <= room {
			cp.buf.Write(b)
		} else {
			cp.buf.Write(b[:room])
			cp.truncated = true
		}
	}

	return len(b), nil
}

// teeBody keeps the original Close while the transport reads through
// the tee.
type teeBody struct {
	io.Reader
	io.Closer
}

// captureResponse renders response-body attrs without ever gating the
// caller: only bodies with a declared length within the cap are read
// (bounded, then spliced back whole); larger declared bodies log size
// only, and unknown-length bodies — streaming, chunked — are never
// touched.
func captureResponse(resp *http.Response, maxBody int) []any {
	contentLength := resp.ContentLength

	switch {
	case contentLength < 0:
		return nil
	case contentLength > int64(maxBody):
		return []any{"response_body_size", contentLength, "response_body_truncated", true}
	}

	var buf bytes.Buffer
	_, err := buf.ReadFrom(io.LimitReader(resp.Body, contentLength))

	resp.Body = teeBody{
		Reader: io.MultiReader(bytes.NewReader(buf.Bytes()), resp.Body),
		Closer: resp.Body,
	}

	if err != nil {
		// Partial capture is not a body; the caller sees the same error.
		return []any{"response_body_size", buf.Len(), "response_body_truncated", true}
	}

	return bodyAttrs("response_body", buf.Bytes(), resp.Header.Get("Content-Type"), false)
}

// bodyAttrs mirrors the server middleware's rule: JSON parses into a
// structured attr the logging handler can walk; anything else stays
// size-only — bytes that cannot be walked cannot be redacted.
func bodyAttrs(key string, body []byte, contentType string, truncated bool) []any {
	attrs := []any{key + "_size", len(body)}
	if truncated {
		return append(attrs, key+"_truncated", true)
	}
	if len(body) == 0 {
		return attrs
	}

	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || (mt != "application/json" && !strings.HasSuffix(mt, "+json")) {
		return attrs
	}

	var v any
	if json.Unmarshal(body, &v) != nil {
		return attrs
	}

	return append(attrs, key, v)
}

// randSource feeds span-id generation; swapped only by tests. Its type
// is io.Reader by inference: crypto/rand.Reader is declared as one.
var randSource = rand.Reader //nolint:gochecknoglobals // test seam

func randomHex(n int) (string, bool) {
	b := make([]byte, n)
	if _, err := io.ReadFull(randSource, b); err != nil {
		return "", false
	}

	return hex.EncodeToString(b), true
}
