package client_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
	"github.com/Wigata-Intech/w-tools/httpx/client"
	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

var errBreakerOpen = errors.New("upstream unhealthy")

// stubBreaker satisfies client.Breaker.
type stubBreaker struct {
	allowErr error
	recorded []error
}

func (b *stubBreaker) Allow() error { return b.allowErr }

func (b *stubBreaker) Record(err error) { b.recorded = append(b.recorded, err) }

// echoServer records what it receives and answers with a fixed body.
type echoServer struct {
	*httptest.Server

	hits        atomic.Int64
	gotMethod   string
	gotPath     string
	gotBody     string
	gotTrace    string
	gotContent  string
	respBody    string
	respContent string
}

func newEchoServer(t *testing.T) *echoServer {
	t.Helper()

	es := &echoServer{respBody: `{"ok":true}`, respContent: "application/json"}
	es.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		es.gotMethod = r.Method
		es.gotPath = r.URL.Path
		es.gotTrace = r.Header.Get("Traceparent")
		es.gotContent = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		es.gotBody = string(b)
		w.Header().Set("Content-Type", es.respContent)
		_, _ = io.WriteString(w, es.respBody)
		es.hits.Add(1) // last: the release-Add orders the field writes above against the test's Load
	}))
	t.Cleanup(es.Close)

	return es
}

// tracedContext runs a no-op request through middleware.Trace to get a
// context carrying trace values, the way a real handler's ctx would.
func tracedContext(t *testing.T) context.Context {
	t.Helper()

	var ctx context.Context
	h := middleware.Trace()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx = r.Context() //nolint:fatcontext // captures the request context once, no growth
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

	return ctx
}

func logCapture() (*slog.Logger, *bytes.Buffer) {
	buf := new(bytes.Buffer)

	return slog.New(slog.NewJSONHandler(buf, nil)), buf
}

var traceparentRe = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

// loggedGet fires one logged GET, drains the body, and returns what the
// caller read plus the captured log.
func loggedGet(t *testing.T, cfg client.Config, url string) (string, *bytes.Buffer) {
	t.Helper()

	log, buf := logCapture()
	cfg.Log = log

	resp, err := client.New(cfg).Get(context.Background(), url)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	return string(body), buf
}

func assertLogLine(t *testing.T, buf *bytes.Buffer, attrs map[string]any, present, absent []string) {
	t.Helper()

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}

	for key, want := range attrs {
		got, ok := line[key]
		if !ok {
			t.Errorf("attr %q missing from %v", key, line)
			continue
		}
		if m, isMap := want.(map[string]any); isMap {
			assertMapAttr(t, key, got, m)
			continue
		}
		if got != want {
			t.Errorf("attr %q = %v, want %v", key, got, want)
		}
	}
	for _, key := range present {
		if _, ok := line[key]; !ok {
			t.Errorf("attr %q missing from %v", key, line)
		}
	}
	for _, key := range absent {
		if _, ok := line[key]; ok {
			t.Errorf("attr %q unexpectedly present: %v", key, line[key])
		}
	}
}

func assertMapAttr(t *testing.T, key string, got any, want map[string]any) {
	t.Helper()

	gm, _ := got.(map[string]any)
	for mk, mv := range want {
		if list, isList := mv.([]any); isList {
			gl, _ := gm[mk].([]any)
			if len(gl) != len(list) || gl[0] != list[0] {
				t.Errorf("attr %q[%q] = %v, want %v", key, mk, gm[mk], mv)
			}
			continue
		}
		if gm[mk] != mv {
			t.Errorf("attr %q[%q] = %v, want %v", key, mk, gm[mk], mv)
		}
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		input    client.Config
		expected bool // request must succeed
	}{
		{
			name:     "zero config is a working client",
			input:    client.Config{},
			expected: true,
		},
		{
			name:     "custom timeout is enforced",
			input:    client.Config{Timeout: 20 * time.Millisecond},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if !tt.expected {
					time.Sleep(100 * time.Millisecond)
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(slow.Close)

			resp, err := client.New(tt.input).Get(context.Background(), slow.URL)
			if resp != nil {
				t.Cleanup(func() { _ = resp.Body.Close() })
			}

			if (err == nil) != tt.expected {
				t.Errorf("Get() error = %v, want success: %t", err, tt.expected)
			}
		})
	}

	t.Run("custom TLS config trusts a private CA the default rejects", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(ts.Close)

		if resp, err := client.New(client.Config{}).Get(context.Background(), ts.URL); err == nil {
			_ = resp.Body.Close()
			t.Fatal("default TLS trusted a self-signed server, want verification failure")
		}

		pool := x509.NewCertPool()
		pool.AddCert(ts.Certificate())

		c := client.New(client.Config{TLS: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}})
		resp, err := c.Get(context.Background(), ts.URL)
		if err != nil {
			t.Fatalf("Get() with the private CA = %v, want success", err)
		}
		_ = resp.Body.Close()
	})
}

func TestClientDo(t *testing.T) {
	type doInput struct {
		breaker *stubBreaker
		traced  bool
	}

	type doExpected struct {
		errIs     error
		hits      int64
		traced    bool
		recordNil bool // breaker recorded exactly one nil
	}

	tests := []struct {
		name     string
		input    doInput
		expected doExpected
	}{
		{
			name:     "plain request round-trips",
			input:    doInput{},
			expected: doExpected{hits: 1},
		},
		{
			name:     "trace in ctx is propagated with a fresh span",
			input:    doInput{traced: true},
			expected: doExpected{hits: 1, traced: true},
		},
		{
			name:     "breaker allows and records the success",
			input:    doInput{breaker: &stubBreaker{}},
			expected: doExpected{hits: 1, recordNil: true},
		},
		{
			name:     "open breaker fails fast before the network",
			input:    doInput{breaker: &stubBreaker{allowErr: errBreakerOpen}},
			expected: doExpected{errIs: client.ErrCircuitOpen, hits: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := newEchoServer(t)

			cfg := client.Config{}
			if tt.input.breaker != nil {
				cfg.Breaker = tt.input.breaker
			}
			c := client.New(cfg)

			ctx := context.Background()
			if tt.input.traced {
				ctx = tracedContext(t)
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, es.URL, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}

			resp, err := c.Do(req)
			if resp != nil {
				t.Cleanup(func() { _ = resp.Body.Close() })
			}

			if tt.expected.errIs != nil {
				if !errors.Is(err, tt.expected.errIs) {
					t.Fatalf("Do() error = %v, want errors.Is %v", err, tt.expected.errIs)
				}
				if !errors.Is(err, errBreakerOpen) {
					t.Errorf("Do() error = %v, want the breaker's own error wrapped", err)
				}
			} else if err != nil {
				t.Fatalf("Do() error = %v, want nil", err)
			}

			if got := es.hits.Load(); got != tt.expected.hits {
				t.Errorf("server hits = %d, want %d", got, tt.expected.hits)
			}
			if tt.expected.traced {
				if !traceparentRe.MatchString(es.gotTrace) {
					t.Errorf("Traceparent = %q, want W3C format", es.gotTrace)
				}
				if !strings.Contains(es.gotTrace, middleware.TraceIDFrom(ctx)) {
					t.Errorf("Traceparent %q does not carry the ctx trace id", es.gotTrace)
				}
			} else if es.gotTrace != "" {
				t.Errorf("Traceparent = %q, want none", es.gotTrace)
			}
			if tt.expected.recordNil && (len(tt.input.breaker.recorded) != 1 || tt.input.breaker.recorded[0] != nil) {
				t.Errorf("breaker recorded %v, want exactly one nil", tt.input.breaker.recorded)
			}
		})
	}

	t.Run("breaker records the transport error", func(t *testing.T) {
		es := newEchoServer(t)
		es.Close() // dead upstream: dialing fails

		br := &stubBreaker{}
		c := client.New(client.Config{Breaker: br})

		resp, err := c.Get(context.Background(), es.URL)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatal("Get() to a closed server succeeded, want transport error")
		}
		if len(br.recorded) != 1 || br.recorded[0] == nil {
			t.Errorf("breaker recorded %v, want exactly one non-nil", br.recorded)
		}
	})
}

func TestClientGet(t *testing.T) {
	tests := []struct {
		name     string
		input    string // path
		expected string // method seen by the server
	}{
		{name: "issues GET", input: "/orders", expected: http.MethodGet},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := newEchoServer(t)

			resp, err := client.New(client.Config{}).Get(context.Background(), es.URL+tt.input)
			if err != nil {
				t.Fatalf("Get() = %v", err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })

			if es.gotMethod != tt.expected || es.gotPath != tt.input {
				t.Errorf("server saw %s %s, want %s %s", es.gotMethod, es.gotPath, tt.expected, tt.input)
			}
		})
	}

	t.Run("invalid URL surfaces the build error", func(t *testing.T) {
		resp, err := client.New(client.Config{}).Get(context.Background(), "://bad")
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Error("Get(\"://bad\") = nil error, want error")
		}
	})
}

func TestClientPost(t *testing.T) {
	tests := []struct {
		name     string
		input    string // body
		expected string // content type seen by the server
	}{
		{name: "issues POST with body and content type", input: `{"n":1}`, expected: "application/json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := newEchoServer(t)

			resp, err := client.New(client.Config{}).Post(context.Background(), es.URL, tt.expected, strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("Post() = %v", err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })

			if es.gotMethod != http.MethodPost || es.gotBody != tt.input || es.gotContent != tt.expected {
				t.Errorf("server saw %s body=%q ct=%q", es.gotMethod, es.gotBody, es.gotContent)
			}
		})
	}
}

func TestClientQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string // body
		expected string // method seen by the server
	}{
		{name: "issues QUERY per RFC 10008", input: `{"min":40}`, expected: httpx.MethodQuery},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := newEchoServer(t)

			resp, err := client.New(client.Config{}).Query(context.Background(), es.URL, "application/json", strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("Query() = %v", err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })

			if es.gotMethod != tt.expected || es.gotBody != tt.input {
				t.Errorf("server saw %s body=%q, want %s body=%q", es.gotMethod, es.gotBody, tt.expected, tt.input)
			}
		})
	}
}

func TestClientLogging(t *testing.T) {
	type logInput struct {
		cfg         client.Config // Log filled by the runner
		query       string
		body        string
		contentType string
		respBody    string
		respContent string
	}

	type logExpected struct {
		attrs   map[string]any
		present []string
		absent  []string
	}

	tests := []struct {
		name     string
		input    logInput
		expected logExpected
	}{
		{
			name:  "access line carries method, url, status, duration",
			input: logInput{},
			expected: logExpected{
				attrs:   map[string]any{"msg": "outbound request", "method": "GET", "status": float64(200)},
				present: []string{"url", "duration_ms"},
				absent:  []string{"query", "request_body", "response_body", "error"},
			},
		},
		{
			name:  "query string is logged as a parsed map, not a raw url",
			input: logInput{query: "?api_key=hunter2&page=1"},
			expected: logExpected{
				attrs:   map[string]any{"query": map[string]any{"api_key": []any{"hunter2"}, "page": []any{"1"}}},
				present: []string{"url"},
			},
		},
		{
			name: "JSON request body captured as a structured attr",
			input: logInput{
				cfg:         client.Config{LogRequestBody: true},
				body:        `{"password":"hunter2"}`,
				contentType: "application/json",
			},
			expected: logExpected{
				attrs: map[string]any{
					"request_body_size": float64(22),
					"request_body":      map[string]any{"password": "hunter2"},
				},
			},
		},
		{
			name: "truncated request body marks and never parses",
			input: logInput{
				cfg:         client.Config{LogRequestBody: true, MaxBody: 4},
				body:        `{"password":"hunter2"}`,
				contentType: "application/json",
			},
			expected: logExpected{
				attrs:  map[string]any{"request_body_size": float64(4), "request_body_truncated": true},
				absent: []string{"request_body"},
			},
		},
		{
			name: "JSON response body captured as a structured attr",
			input: logInput{
				cfg:         client.Config{LogResponseBody: true},
				respBody:    `{"token":"abc"}`,
				respContent: "application/json",
			},
			expected: logExpected{
				attrs: map[string]any{
					"response_body_size": float64(15),
					"response_body":      map[string]any{"token": "abc"},
				},
			},
		},
		{
			name: "empty response body logs size zero",
			input: logInput{
				cfg:         client.Config{LogResponseBody: true},
				respBody:    "-", // sentinel replaced by "" in the runner
				respContent: "application/json",
			},
			expected: logExpected{
				attrs:  map[string]any{"response_body_size": float64(0)},
				absent: []string{"response_body"},
			},
		},
		{
			name: "malformed JSON response logs size only",
			input: logInput{
				cfg:         client.Config{LogResponseBody: true},
				respBody:    `{broken`,
				respContent: "application/json",
			},
			expected: logExpected{
				attrs:  map[string]any{"response_body_size": float64(7)},
				absent: []string{"response_body"},
			},
		},
		{
			name: "non-JSON response logs size only",
			input: logInput{
				cfg:         client.Config{LogResponseBody: true},
				respBody:    "<html>secret</html>",
				respContent: "text/html",
			},
			expected: logExpected{
				attrs:  map[string]any{"response_body_size": float64(19)},
				absent: []string{"response_body"},
			},
		},
		{
			name: "over-cap declared length logs size only, unread",
			input: logInput{
				cfg:         client.Config{LogResponseBody: true, MaxBody: 4},
				respBody:    `{"ok":true}`,
				respContent: "application/json",
			},
			expected: logExpected{
				attrs:  map[string]any{"response_body_size": float64(11), "response_body_truncated": true},
				absent: []string{"response_body"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := newEchoServer(t)
			if tt.input.respBody != "" {
				es.respBody, es.respContent = tt.input.respBody, tt.input.respContent
				if tt.input.respBody == "-" {
					es.respBody = ""
				}
			}

			log, buf := logCapture()
			tt.input.cfg.Log = log
			c := client.New(tt.input.cfg)

			var (
				resp *http.Response
				err  error
			)
			if tt.input.body != "" {
				resp, err = c.Post(context.Background(), es.URL+tt.input.query, tt.input.contentType, strings.NewReader(tt.input.body))
			} else {
				resp, err = c.Get(context.Background(), es.URL+tt.input.query)
			}
			if err != nil {
				t.Fatalf("request: %v", err)
			}

			// The caller must always read the complete body, capture or not.
			full, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(full) != es.respBody {
				t.Errorf("caller read %q, want the full body %q", full, es.respBody)
			}

			assertLogLine(t, buf, tt.expected.attrs, tt.expected.present, tt.expected.absent)
		})
	}

	t.Run("streaming responses are never captured or gated", func(t *testing.T) {
		streamed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"part":1}`)
			_ = http.NewResponseController(w).Flush() // forces chunked encoding; failure just means a plain body
			_, _ = io.WriteString(w, `{"part":2}`)
		}))
		t.Cleanup(streamed.Close)

		full, buf := loggedGet(t, client.Config{LogResponseBody: true}, streamed.URL)

		if full != `{"part":1}{"part":2}` {
			t.Errorf("caller read %q, want the full stream", full)
		}
		if strings.Contains(buf.String(), "response_body") {
			t.Errorf("log %q captured a streaming body", buf.String())
		}
	})

	t.Run("a body cut short logs partial size, never a body", func(t *testing.T) {
		cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, `{"par`) // 5 of the declared 100 bytes, then the conn dies
		}))
		t.Cleanup(cut.Close)

		_, buf := loggedGet(t, client.Config{LogResponseBody: true, MaxBody: 4096}, cut.URL)

		if !strings.Contains(buf.String(), `"response_body_truncated":true`) {
			t.Errorf("log %q missing the truncation mark for a cut body", buf.String())
		}
		if strings.Contains(buf.String(), `"response_body":{`) {
			t.Errorf("log %q parsed a partial body", buf.String())
		}
	})

	t.Run("error line never embeds the URL's query", func(t *testing.T) {
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(slow.Close)

		log, buf := logCapture()
		c := client.New(client.Config{Log: log, Timeout: 20 * time.Millisecond})

		resp, err := c.Get(context.Background(), slow.URL+"/p?api_key=hunter2")
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatal("Get() beat the timeout, want error")
		}

		line := buf.String()
		if strings.Contains(line, "api_key=hunter2") {
			t.Errorf("error line leaks the raw query: %q", line)
		}
		if !strings.Contains(line, `"api_key":["hunter2"]`) {
			t.Errorf("query map missing from the error line: %q", line)
		}
	})

	t.Run("redirects log the final URL", func(t *testing.T) {
		es := newEchoServer(t)
		hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, es.URL+"/final", http.StatusTemporaryRedirect)
		}))
		t.Cleanup(hop.Close)

		_, buf := loggedGet(t, client.Config{}, hop.URL+"/start")

		if !strings.Contains(buf.String(), "/final") || strings.Contains(buf.String(), "/start") {
			t.Errorf("log %q should carry the final URL, not the redirect origin", buf.String())
		}
	})

	t.Run("a caller-set Traceparent is never overwritten", func(t *testing.T) {
		es := newEchoServer(t)

		own := "00-11111111111111111111111111111111-2222222222222222-01"
		req, err := http.NewRequestWithContext(tracedContext(t), http.MethodGet, es.URL, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Traceparent", own)

		resp, err := client.New(client.Config{}).Do(req)
		if err != nil {
			t.Fatalf("Do() = %v", err)
		}
		_ = resp.Body.Close()

		if es.gotTrace != own {
			t.Errorf("Traceparent = %q, want the caller's own %q", es.gotTrace, own)
		}
	})

	t.Run("traced context puts the trace id on the line", func(t *testing.T) {
		es := newEchoServer(t)
		log, buf := logCapture()

		ctx := tracedContext(t)
		resp, err := client.New(client.Config{Log: log}).Get(ctx, es.URL)
		if err != nil {
			t.Fatalf("Get() = %v", err)
		}
		_ = resp.Body.Close()

		want := `"trace_id":"` + middleware.TraceIDFrom(ctx) + `"`
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log %q missing %s", buf.String(), want)
		}
	})

	t.Run("span mint failure sends the request untraced", func(t *testing.T) {
		es := newEchoServer(t)
		restore := client.SetRandSource(strings.NewReader("")) // empty: ReadFull fails
		t.Cleanup(restore)

		resp, err := client.New(client.Config{}).Get(tracedContext(t), es.URL)
		if err != nil {
			t.Fatalf("Get() = %v", err)
		}
		_ = resp.Body.Close()

		if es.gotTrace != "" {
			t.Errorf("Traceparent = %q, want none when the span mint fails", es.gotTrace)
		}
	})

	t.Run("nil Log stays silent", func(t *testing.T) {
		es := newEchoServer(t)

		resp, err := client.New(client.Config{}).Get(context.Background(), es.URL)
		if err != nil {
			t.Fatalf("Get() = %v", err)
		}
		_ = resp.Body.Close()
	})

	t.Run("transport failure logs an error line", func(t *testing.T) {
		es := newEchoServer(t)
		es.Close()

		log, buf := logCapture()
		resp, err := client.New(client.Config{Log: log}).Get(context.Background(), es.URL)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatal("Get() to a closed server succeeded")
		}

		if !strings.Contains(buf.String(), "outbound request failed") {
			t.Errorf("log %q missing the failure line", buf.String())
		}
	})
}
