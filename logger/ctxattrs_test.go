package logger_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wigata-Intech/w-tools/logger"
)

type requestIDKey struct{}

// fromRequestID returns a request_id attr when ctx carries one.
func fromRequestID(ctx context.Context) []slog.Attr {
	id, _ := ctx.Value(requestIDKey{}).(string)
	if id == "" {
		return nil
	}
	return []slog.Attr{slog.String("request_id", id)}
}

func TestWrapContextAttrs(t *testing.T) {
	type wrapInput struct {
		requestID string // stored under requestIDKey when non-empty
		fn        func(ctx context.Context) []slog.Attr
	}
	tests := []struct {
		name     string
		input    wrapInput
		expected map[string]any // "-key" asserts absence
	}{
		{
			name:     "context value appended to the record",
			input:    wrapInput{requestID: "req-1", fn: fromRequestID},
			expected: map[string]any{"request_id": "req-1"},
		},
		{
			name:     "value absent from context appends nothing",
			input:    wrapInput{fn: fromRequestID},
			expected: map[string]any{"-request_id": nil},
		},
		{
			name:     "empty attrs leave the record unchanged",
			input:    wrapInput{fn: func(context.Context) []slog.Attr { return []slog.Attr{} }},
			expected: map[string]any{"-request_id": nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.input.requestID != "" {
				ctx = context.WithValue(ctx, requestIDKey{}, tt.input.requestID)
			}
			var buf bytes.Buffer
			h := logger.Wrap(slog.NewJSONHandler(&buf, nil), logger.WrapConfig{ContextAttrs: tt.input.fn})
			slog.New(h).LogAttrs(ctx, slog.LevelInfo, "m")
			got := fields(t, &buf)
			for k, v := range tt.expected {
				assertField(t, got, k, v)
			}
		})
	}

	t.Run("call-site attr wins over the extracted one", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.New(logger.Config{Writer: &buf, ContextAttrs: fromRequestID})
		ctx := context.WithValue(context.Background(), requestIDKey{}, "from-ctx")

		log.Info(ctx, "m", "request_id", "from-call-site")

		line := buf.String()
		if got := strings.Count(line, `"request_id"`); got != 1 {
			t.Fatalf("request_id appears %d times, want exactly once: %s", got, line)
		}
		assertField(t, fields(t, &buf), "request_id", "from-call-site")
	})

	t.Run("enriched attrs pass through redaction under New", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.New(logger.Config{
			Writer: &buf,
			Redact: logger.RedactConfig{Redacted: []string{"session_token"}},
			ContextAttrs: func(context.Context) []slog.Attr {
				return []slog.Attr{slog.String("session_token", "secret-value")}
			},
		})
		log.Info(context.Background(), "m")
		got := fields(t, &buf)
		assertField(t, got, "session_token", "[REDACTED]")
	})

	t.Run("extractor not called for records below the level", func(t *testing.T) {
		var buf bytes.Buffer
		var calls atomic.Int64
		log := logger.New(logger.Config{
			Writer: &buf,
			Level:  slog.LevelInfo,
			ContextAttrs: func(context.Context) []slog.Attr {
				calls.Add(1)
				return nil
			},
		})
		log.Debug(context.Background(), "filtered")
		if got := calls.Load(); got != 0 {
			t.Fatalf("extractor calls for a filtered record = %d, want 0", got)
		}
		log.Info(context.Background(), "emitted")
		if got := calls.Load(); got != 1 {
			t.Fatalf("extractor calls after one emitted record = %d, want 1", got)
		}
	})

	t.Run("derived loggers keep enriching", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.New(logger.Config{Writer: &buf, ContextAttrs: fromRequestID})
		ctx := context.WithValue(context.Background(), requestIDKey{}, "req-2")

		log.With("bound", "yes").Info(ctx, "m")
		got := fields(t, &buf)
		assertField(t, got, "bound", "yes")
		assertField(t, got, "request_id", "req-2")

		// The documented WithGroup contract: enriched attrs render as if
		// passed at the call site, so they land inside the open group.
		buf.Reset()
		log.Slog().WithGroup("http").InfoContext(ctx, "m", "path", "/x")
		grouped := fields(t, &buf)
		g, ok := grouped["http"].(map[string]any)
		if !ok {
			t.Fatalf("http group missing from %v", grouped)
		}
		if g["path"] != "/x" {
			t.Fatalf("http.path = %v, want /x", g["path"])
		}
		if g["request_id"] != "req-2" {
			t.Fatalf("http.request_id = %v, want req-2 nested inside the group", g["request_id"])
		}
		if _, top := grouped["request_id"]; top {
			t.Fatal("request_id must not also appear at top level")
		}
	})

	t.Run("concurrent logging is race-free", func(t *testing.T) {
		var buf bytes.Buffer
		var mu sync.Mutex
		log := logger.New(logger.Config{
			Writer: writerFunc(func(p []byte) (int, error) {
				mu.Lock()
				defer mu.Unlock()
				return buf.Write(p)
			}),
			ContextAttrs: fromRequestID,
		})
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx := context.WithValue(context.Background(), requestIDKey{}, "req-c")
				for range 50 {
					log.Info(ctx, "m")
				}
			}()
		}
		wg.Wait()
		if got := bytes.Count(buf.Bytes(), []byte("\n")); got != 400 {
			t.Fatalf("emitted lines = %d, want 400", got)
		}
	})
}

// writerFunc adapts a func to io.Writer.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// assertField checks one expected entry: "-key" asserts absence.
func assertField(t *testing.T, got map[string]any, key string, want any) {
	t.Helper()
	if len(key) > 0 && key[0] == '-' {
		if _, ok := got[key[1:]]; ok {
			t.Fatalf("field %q present, want absent; line: %v", key[1:], got)
		}
		return
	}
	if got[key] != want {
		t.Fatalf("field %q = %v, want %v", key, got[key], want)
	}
}
