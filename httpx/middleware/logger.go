package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx"
)

// LoggerConfig configures Logger.
type LoggerConfig struct {
	// Log writes the access line. Nil means slog.Default(). Redaction
	// rules travel inside the handler, so a logger built by
	// w-tools/logger applies them to captured bodies automatically.
	Log *slog.Logger

	// Body capture is off by default. Captured JSON bodies are logged as
	// structured attrs; non-JSON bodies log as size only, never raw.
	LogRequestBody  bool
	LogResponseBody bool

	// MaxBody caps each captured body. Default httpx.DefaultMaxBody.
	MaxBody int
}

// Logger emits one access line per request: method, path, matched
// pattern, status, bytes written, duration, remote IP, and the request
// and trace IDs when present.
func Logger(cfg LoggerConfig) httpx.Middleware {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}

	maxBody := cfg.MaxBody
	if maxBody <= 0 {
		maxBody = httpx.DefaultMaxBody
	}

	pool := &sync.Pool{New: func() any { return new(bytes.Buffer) }}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			var reqBuf *bytes.Buffer
			var reqCaptured []byte
			var reqTruncated bool
			if cfg.LogRequestBody && r.Body != nil {
				reqBuf, reqCaptured, reqTruncated = captureRequest(r, pool, maxBody)
			}

			sw := &statusWriter{ResponseWriter: w}
			if cfg.LogResponseBody {
				sw.tee = getBuffer(pool)
				sw.teeMax = maxBody
			}

			next.ServeHTTP(sw, r)
			elapsed := time.Since(start)

			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}

			attrs := make([]any, 0, 24)
			attrs = append(attrs,
				"method", r.Method,
				"path", r.URL.Path,
			)
			if r.Pattern != "" {
				attrs = append(attrs, "pattern", r.Pattern)
			}
			attrs = append(attrs,
				"status", status,
				"bytes", sw.bytes,
				"duration_ms", float64(elapsed.Microseconds())/1000.0,
				"remote_ip", remoteIP(r.RemoteAddr),
			)
			if id := RequestIDFrom(r.Context()); id != "" {
				attrs = append(attrs, "request_id", id)
			}
			if id := TraceIDFrom(r.Context()); id != "" {
				attrs = append(attrs, "trace_id", id)
			}
			if reqBuf != nil {
				attrs = append(attrs, bodyAttrs("request_body", reqCaptured, r.Header.Get("Content-Type"), reqTruncated)...)
			}
			if sw.tee != nil {
				attrs = append(attrs, bodyAttrs("response_body", sw.tee.Bytes(), sw.Header().Get("Content-Type"), sw.teeTruncated)...)
			}

			log.InfoContext(r.Context(), "request", attrs...)

			putBuffer(pool, reqBuf, maxBody)
			putBuffer(pool, sw.tee, maxBody)
		})
	}
}

// captureRequest buffers up to maxBody+1 bytes of the request body and
// splices everything it consumed back in front of the unread remainder,
// so the handler always reads the complete body. The returned capture
// view is capped at maxBody; the buffer itself keeps every consumed byte
// because the replay depends on it.
func captureRequest(r *http.Request, pool *sync.Pool, maxBody int) (*bytes.Buffer, []byte, bool) {
	buf := getBuffer(pool)
	_, _ = buf.ReadFrom(io.LimitReader(r.Body, int64(maxBody)+1))

	captured := buf.Bytes()
	truncated := len(captured) > maxBody
	if truncated {
		captured = captured[:maxBody]
	}

	r.Body = replayBody{
		Reader: io.MultiReader(bytes.NewReader(buf.Bytes()), r.Body),
		Closer: r.Body,
	}

	return buf, captured, truncated
}

// replayBody rejoins the captured prefix with the live remainder while
// keeping the original Close.
type replayBody struct {
	io.Reader
	io.Closer
}

// bodyAttrs renders a captured body: JSON parses into a structured attr
// the logging handler can walk; anything else stays size-only — bytes
// that cannot be walked cannot be redacted, so they never ship raw.
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

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}

	return host
}

func getBuffer(pool *sync.Pool) *bytes.Buffer {
	buf, _ := pool.Get().(*bytes.Buffer)
	buf.Reset()

	return buf
}

// putBuffer returns a buffer to the pool unless it outgrew the capture
// cap — oversized buffers are dropped so pool memory stays flat.
func putBuffer(pool *sync.Pool, buf *bytes.Buffer, maxBody int) {
	if buf == nil || buf.Cap() > 2*maxBody {
		return
	}

	pool.Put(buf)
}

// statusWriter records status and size, and optionally tees the body.
type statusWriter struct {
	http.ResponseWriter

	status       int
	bytes        int
	tee          *bytes.Buffer
	teeMax       int
	teeTruncated bool
}

func (w *statusWriter) WriteHeader(status int) {
	// 1xx headers are informational; the access line reports the final status.
	if w.status == 0 && status >= http.StatusOK {
		w.status = status
	}

	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	if w.tee != nil && !w.teeTruncated {
		if room := w.teeMax - w.tee.Len(); len(b) <= room {
			w.tee.Write(b)
		} else {
			w.tee.Write(b[:room])
			w.teeTruncated = true
		}
	}

	n, err := w.ResponseWriter.Write(b)
	w.bytes += n

	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
