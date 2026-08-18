package middleware_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

var (
	errStoreDown   = errors.New("store down")
	errWriteFailed = errors.New("write failed")
)

// spyStore wraps a real MemoryStore, recording keys and letting cases
// fail individual operations.
type spyStore struct {
	inner    *middleware.MemoryStore
	setNXErr error
	setErr   error
	getErr   error
	getMiss  bool
	getRaw   []byte

	mu   sync.Mutex
	keys []string
}

func newSpyStore() *spyStore {
	return &spyStore{inner: middleware.NewMemoryStore(0)}
}

func (s *spyStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	s.keys = append(s.keys, key)
	s.mu.Unlock()

	if s.setNXErr != nil {
		return false, s.setNXErr
	}

	return s.inner.SetNX(ctx, key, value, ttl)
}

func (s *spyStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	switch {
	case s.getErr != nil:
		return nil, false, s.getErr
	case s.getMiss:
		return nil, false, nil
	case s.getRaw != nil:
		return s.getRaw, true, nil
	default:
		return s.inner.Get(ctx, key)
	}
}

func (s *spyStore) Set(ctx context.Context, key string, value []byte) error {
	if s.setErr != nil {
		return s.setErr
	}

	return s.inner.Set(ctx, key, value)
}

func (s *spyStore) Delete(ctx context.Context, key string) error {
	return s.inner.Delete(ctx, key)
}

// idemInput is one table case's request shape; the runner fills the
// Store when the case leaves it nil.
type idemInput struct {
	cfg    middleware.IdempotencyConfig
	method string
	key    string
}

// idemRequest performs one request against handler and returns the recorder.
func idemRequest(handler http.Handler, method, target, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	return rr
}

// countingHandler responds 201 with a marker header and echoes a fixed
// body, counting executions.
func countingHandler(executions *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		executions.Add(1)
		w.Header().Set("X-Marker", "original")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"ord_1"}`)) //nolint:errcheck,gosec // test writer never fails
	})
}

func TestIdempotency(t *testing.T) {
	tests := []struct {
		name       string
		input      idemInput
		expected   int
		executions int64
	}{
		{
			name:       "uncovered method passes through",
			input:      idemInput{method: http.MethodGet, key: "k-get"},
			expected:   http.StatusCreated,
			executions: 1,
		},
		{
			name:       "missing header passes through",
			input:      idemInput{method: http.MethodPost},
			expected:   http.StatusCreated,
			executions: 1,
		},
		{
			name:       "missing header with Required is rejected",
			input:      idemInput{cfg: middleware.IdempotencyConfig{Required: true}, method: http.MethodPost},
			expected:   http.StatusBadRequest,
			executions: 0,
		},
		{
			name:       "lowercase configured method is normalized",
			input:      idemInput{cfg: middleware.IdempotencyConfig{Methods: []string{"post"}}, method: http.MethodPost, key: "k-lower"},
			expected:   http.StatusCreated,
			executions: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.input.cfg
			if cfg.Store == nil {
				cfg.Store = middleware.NewMemoryStore(0)
			}
			var executions atomic.Int64
			handler := middleware.Idempotency(cfg)(countingHandler(&executions))

			rr := idemRequest(handler, tt.input.method, "/orders", tt.input.key, `{"amount":1}`)
			if rr.Code != tt.expected {
				t.Fatalf("status = %d, want %d", rr.Code, tt.expected)
			}
			if got := executions.Load(); got != tt.executions {
				t.Fatalf("handler executions = %d, want %d", got, tt.executions)
			}
		})
	}

	t.Run("nil store panics at construction", testIdempotencyNilStorePanicsAtConstruction)

	t.Run("duplicate replays the stored response", testIdempotencyDuplicateReplaysTheStoredResponse)

	t.Run("handler writing nothing stores an empty 200", testIdempotencyHandlerWritingNothingStoresAnEmpty200)

	t.Run("per-response headers are not replayed", testIdempotencyPerresponseHeadersAreNotReplayed)

	t.Run("unreadable request body is a 400", testIdempotencyUnreadableRequestBodyIsA400)

	t.Run("flush reaches the underlying writer through Unwrap", testIdempotencyFlushReachesTheUnderlyingWriterThroughUnwrap)

	t.Run("key reuse with a different body is a 422", testIdempotencyKeyReuseWithADifferentBodyIsA422)

	t.Run("key reuse on a different URI is a 422", testIdempotencyKeyReuseOnADifferentURIIsA422)

	t.Run("duplicate while the original is in flight is a 409", testIdempotencyDuplicateWhileTheOriginalIsInFlightIsA409)

	t.Run("reject policy answers duplicates with the reject status", testIdempotencyRejectPolicyAnswersDuplicatesWithTheRejectStatus)

	t.Run("5xx is not stored and a retry re-executes", testIdempotency5xxIsNotStoredAndARetryReexecutes)

	t.Run("4xx is stored and replayed", testIdempotency4xxIsStoredAndReplayed)

	t.Run("panic releases the claim and re-panics", testIdempotencyPanicReleasesTheClaimAndRepanics)

	t.Run("oversized response is served but not stored", testIdempotencyOversizedResponseIsServedButNotStored)

	t.Run("request body reaches the handler intact", testIdempotencyRequestBodyReachesTheHandlerIntact)

	t.Run("store error fails closed with 503", testIdempotencyStoreErrorFailsClosedWith503)
	t.Run("duplicate lookup failure fails closed", testIdempotencyDuplicateLookupFailureFailsClosed)
	t.Run("vanished record answers in-flight", testIdempotencyVanishedRecordAnswersInFlight)
	t.Run("corrupted record fails closed", testIdempotencyCorruptedRecordFailsClosed)

	t.Run("failed Complete fails closed", testIdempotencyFailedCompleteFailsClosed)
	t.Run("oversized request body is a 413", testIdempotencyOversizedRequestBodyIsA413)
	t.Run("custom ErrorWriter shapes refusals", testIdempotencyCustomErrorWriterShapesRefusals)

	t.Run("prefix namespaces the store key", testIdempotencyPrefixNamespacesTheStoreKey)

	t.Run("custom header and statuses are honored", testIdempotencyCustomHeaderAndStatusesAreHonored)

	t.Run("concurrent identical requests execute the handler once", testIdempotencyConcurrentIdenticalRequestsExecuteTheHandlerOnce)
}

func testIdempotencyNilStorePanicsAtConstruction(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Idempotency(Config{}) did not panic")
		}
	}()
	middleware.Idempotency(middleware.IdempotencyConfig{})
}

func testIdempotencyDuplicateReplaysTheStoredResponse(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(countingHandler(&executions))

	first := idemRequest(handler, http.MethodPost, "/orders", "k-1", `{"amount":1}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-1", `{"amount":1}`)

	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want 1", executions.Load())
	}
	if second.Code != first.Code || second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, first = %d, want both 201", second.Code, first.Code)
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf("replay body = %q, want %q", second.Body.String(), first.Body.String())
	}
	if got := second.Header().Get("X-Marker"); got != "original" {
		t.Fatalf("replayed X-Marker = %q, want original", got)
	}
	if got := second.Header().Get("Idempotency-Replayed"); got != "true" {
		t.Fatalf("Idempotency-Replayed = %q, want true", got)
	}
	if first.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("first response must not be marked replayed")
	}
}

func testIdempotencyHandlerWritingNothingStoresAnEmpty200(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { executions.Add(1) }))

	first := idemRequest(handler, http.MethodPost, "/orders", "k-silent", `{}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-silent", `{}`)

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d then %d, want 200 and replayed 200", first.Code, second.Code)
	}
	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want 1", executions.Load())
	}
}

func testIdempotencyPerresponseHeadersAreNotReplayed(t *testing.T) {
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Connection", "close")
			w.Header().Set("X-Kept", "yes")
			w.WriteHeader(http.StatusCreated)
		}))

	idemRequest(handler, http.MethodPost, "/orders", "k-hdr", `{}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-hdr", `{}`)

	if got := second.Header().Get("X-Kept"); got != "yes" {
		t.Fatalf("replayed X-Kept = %q, want yes", got)
	}
	if got := second.Header().Get("Connection"); got != "" {
		t.Fatalf("replayed Connection = %q, want absent", got)
	}
}

func testIdempotencyUnreadableRequestBodyIsA400(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(countingHandler(&executions))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/orders", errReader{})
	req.Header.Set("Idempotency-Key", "k-bad-body")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if executions.Load() != 0 {
		t.Fatalf("handler executions = %d, want 0", executions.Load())
	}
}

func testIdempotencyFlushReachesTheUnderlyingWriterThroughUnwrap(t *testing.T) {
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("Flush through the capture writer: %v", err)
			}
		}))

	rr := idemRequest(handler, http.MethodPost, "/orders", "k-flush", `{}`)
	if !rr.Flushed {
		t.Fatal("flush did not reach the recorder")
	}
}

func testIdempotencyKeyReuseWithADifferentBodyIsA422(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(countingHandler(&executions))

	idemRequest(handler, http.MethodPost, "/orders", "k-2", `{"amount":1}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-2", `{"amount":2}`)

	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatch status = %d, want 422", second.Code)
	}
	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want 1", executions.Load())
	}
}

func testIdempotencyKeyReuseOnADifferentURIIsA422(t *testing.T) {
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(countingHandler(new(atomic.Int64)))

	idemRequest(handler, http.MethodPost, "/orders", "k-3", `{"amount":1}`)
	second := idemRequest(handler, http.MethodPost, "/refunds", "k-3", `{"amount":1}`)

	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatch status = %d, want 422", second.Code)
	}
}

func testIdempotencyDuplicateWhileTheOriginalIsInFlightIsA409(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusCreated)
		}))

	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- idemRequest(handler, http.MethodPost, "/orders", "k-4", `{}`) }()
	<-entered

	second := idemRequest(handler, http.MethodPost, "/orders", "k-4", `{}`)
	if second.Code != http.StatusConflict {
		t.Fatalf("in-flight duplicate status = %d, want 409", second.Code)
	}

	close(release)
	if first := <-done; first.Code != http.StatusCreated {
		t.Fatalf("original status = %d, want 201", first.Code)
	}
}

func testIdempotencyRejectPolicyAnswersDuplicatesWithTheRejectStatus(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{
		Store:        middleware.NewMemoryStore(0),
		Policy:       middleware.Reject,
		RejectStatus: http.StatusGone,
	})(countingHandler(&executions))

	idemRequest(handler, http.MethodPost, "/orders", "k-5", `{}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-5", `{}`)

	if second.Code != http.StatusGone {
		t.Fatalf("rejected duplicate status = %d, want 410", second.Code)
	}
	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want 1", executions.Load())
	}
}

func testIdempotency5xxIsNotStoredAndARetryReexecutes(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if executions.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
		}))

	first := idemRequest(handler, http.MethodPost, "/orders", "k-6", `{}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-6", `{}`)

	if first.Code != http.StatusInternalServerError || second.Code != http.StatusCreated {
		t.Fatalf("statuses = %d then %d, want 500 then 201", first.Code, second.Code)
	}
	if executions.Load() != 2 {
		t.Fatalf("handler executions = %d, want 2", executions.Load())
	}
}

func testIdempotency4xxIsStoredAndReplayed(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			executions.Add(1)
			w.WriteHeader(http.StatusBadRequest)
		}))

	idemRequest(handler, http.MethodPost, "/orders", "k-7", `{}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-7", `{}`)

	if second.Code != http.StatusBadRequest {
		t.Fatalf("replayed status = %d, want 400", second.Code)
	}
	if got := second.Header().Get("Idempotency-Replayed"); got != "true" {
		t.Fatalf("Idempotency-Replayed = %q, want true", got)
	}
	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want 1", executions.Load())
	}
}

func testIdempotencyPanicReleasesTheClaimAndRepanics(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if executions.Add(1) == 1 {
				panic("boom")
			}
			w.WriteHeader(http.StatusCreated)
		}))

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic did not propagate through the middleware")
			}
		}()
		idemRequest(handler, http.MethodPost, "/orders", "k-8", `{}`)
	}()

	second := idemRequest(handler, http.MethodPost, "/orders", "k-8", `{}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("retry after panic status = %d, want 201", second.Code)
	}
	if executions.Load() != 2 {
		t.Fatalf("handler executions = %d, want 2", executions.Load())
	}
}

func testIdempotencyOversizedResponseIsServedButNotStored(t *testing.T) {
	var executions atomic.Int64
	big := strings.Repeat("x", 64)
	handler := middleware.Idempotency(middleware.IdempotencyConfig{
		Store:        middleware.NewMemoryStore(0),
		MaxBodyBytes: 8,
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		executions.Add(1)
		w.Write([]byte(big)) //nolint:errcheck,gosec // test writer never fails
	}))

	first := idemRequest(handler, http.MethodPost, "/orders", "k-9", `{}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-9", `{}`)

	if first.Body.String() != big || second.Body.String() != big {
		t.Fatal("oversized body must be served in full both times")
	}
	if executions.Load() != 2 {
		t.Fatalf("handler executions = %d, want 2 (nothing stored)", executions.Load())
	}
}

func testIdempotencyRequestBodyReachesTheHandlerIntact(t *testing.T) {
	var seen string
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			seen = string(b)
			w.WriteHeader(http.StatusCreated)
		}))

	idemRequest(handler, http.MethodPost, "/orders", "k-10", `{"amount":42}`)
	if seen != `{"amount":42}` {
		t.Fatalf("handler saw body %q, want the original", seen)
	}
}

func testIdempotencyStoreErrorFailsClosedWith503(t *testing.T) {
	store := newSpyStore()
	store.setNXErr = errStoreDown
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: store})(countingHandler(&executions))

	rr := idemRequest(handler, http.MethodPost, "/orders", "k-11", `{}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if executions.Load() != 0 {
		t.Fatalf("handler executions = %d, want 0", executions.Load())
	}
}

func testIdempotencyFailedCompleteFailsClosed(t *testing.T) {
	store := newSpyStore()
	store.setErr = errWriteFailed
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: store})(countingHandler(&executions))

	first := idemRequest(handler, http.MethodPost, "/orders", "k-12", `{}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-12", `{}`)

	if first.Code != http.StatusCreated {
		t.Fatalf("original status = %d, want 201", first.Code)
	}
	if second.Code != http.StatusConflict {
		t.Fatalf("duplicate after failed Complete status = %d, want 409 (claim held)", second.Code)
	}
	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want exactly 1 — a store blip must never re-execute", executions.Load())
	}
}

func testIdempotencyOversizedRequestBodyIsA413(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{
		Store:               middleware.NewMemoryStore(0),
		MaxRequestBodyBytes: 8,
	})(countingHandler(&executions))

	rr := idemRequest(handler, http.MethodPost, "/orders", "k-413", strings.Repeat("x", 64))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
	if executions.Load() != 0 {
		t.Fatalf("handler executions = %d, want 0", executions.Load())
	}
}

func testIdempotencyCustomErrorWriterShapesRefusals(t *testing.T) {
	handler := middleware.Idempotency(middleware.IdempotencyConfig{
		Store: middleware.NewMemoryStore(0),
		ErrorWriter: func(w http.ResponseWriter, _ *http.Request, status int, detail string) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("custom: " + detail))
		},
	})(countingHandler(new(atomic.Int64)))

	idemRequest(handler, http.MethodPost, "/orders", "k-ew", `{"a":1}`)
	second := idemRequest(handler, http.MethodPost, "/orders", "k-ew", `{"a":2}`)

	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", second.Code)
	}
	if !strings.HasPrefix(second.Body.String(), "custom: ") {
		t.Fatalf("body = %q, want the custom error writer's shape", second.Body.String())
	}
}

func testIdempotencyPrefixNamespacesTheStoreKey(t *testing.T) {
	store := newSpyStore()
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: store, Prefix: "orders:"})(countingHandler(new(atomic.Int64)))

	idemRequest(handler, http.MethodPost, "/orders", "k-13", `{}`)

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.keys) != 1 || store.keys[0] != "orders:k-13" {
		t.Fatalf("store keys = %v, want [orders:k-13]", store.keys)
	}
}

func testIdempotencyCustomHeaderAndStatusesAreHonored(t *testing.T) {
	handler := middleware.Idempotency(middleware.IdempotencyConfig{
		Store:          middleware.NewMemoryStore(0),
		Header:         "X-Request-Key",
		MismatchStatus: http.StatusConflict,
	})(countingHandler(new(atomic.Int64)))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/orders", strings.NewReader(`{"a":1}`))
	req.Header.Set("X-Request-Key", "k-14")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	req2 := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/orders", strings.NewReader(`{"a":2}`))
	req2.Header.Set("X-Request-Key", "k-14")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr.Code != http.StatusCreated || rr2.Code != http.StatusConflict {
		t.Fatalf("statuses = %d then %d, want 201 then custom 409", rr.Code, rr2.Code)
	}
}

func testIdempotencyConcurrentIdenticalRequestsExecuteTheHandlerOnce(t *testing.T) {
	var executions atomic.Int64
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: middleware.NewMemoryStore(0)})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			executions.Add(1)
			time.Sleep(5 * time.Millisecond)
			w.WriteHeader(http.StatusCreated)
		}))

	const n = 50
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = idemRequest(handler, http.MethodPost, "/orders", "k-15", `{}`).Code
		}()
	}
	wg.Wait()

	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want exactly 1", executions.Load())
	}
	for i, code := range codes {
		if code != http.StatusCreated && code != http.StatusConflict {
			t.Fatalf("response %d status = %d, want 201 (winner/replay) or 409 (in-flight)", i, code)
		}
	}
}

// duplicateThrough seeds one completed request through handler-with-store
// and returns the store for the duplicate's failure injection.
func duplicateThrough(t *testing.T) (*spyStore, http.Handler) {
	t.Helper()
	store := newSpyStore()
	handler := middleware.Idempotency(middleware.IdempotencyConfig{Store: store})(countingHandler(new(atomic.Int64)))
	if rr := idemRequest(handler, http.MethodPost, "/orders", "k-dup-path", `{}`); rr.Code != http.StatusCreated {
		t.Fatalf("seed status = %d, want 201", rr.Code)
	}

	return store, handler
}

func testIdempotencyDuplicateLookupFailureFailsClosed(t *testing.T) {
	store, handler := duplicateThrough(t)
	store.getErr = errStoreDown

	if rr := idemRequest(handler, http.MethodPost, "/orders", "k-dup-path", `{}`); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func testIdempotencyVanishedRecordAnswersInFlight(t *testing.T) {
	store, handler := duplicateThrough(t)
	store.getMiss = true

	if rr := idemRequest(handler, http.MethodPost, "/orders", "k-dup-path", `{}`); rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 — the client retries rather than racing for the vanished key", rr.Code)
	}
}

func testIdempotencyCorruptedRecordFailsClosed(t *testing.T) {
	store, handler := duplicateThrough(t)
	store.getRaw = []byte("not json")

	if rr := idemRequest(handler, http.MethodPost, "/orders", "k-dup-path", `{}`); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}
