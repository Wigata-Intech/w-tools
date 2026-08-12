package circuitbreaker_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/x/circuitbreaker"
)

var errUpstream = errors.New("upstream failed")

// scenario drives a breaker through a scripted sequence with a driven
// clock. Ops: "ok" and "fail" are Allow+Record pairs, "allow" and
// "denied" assert Allow alone (the probe reservation stays in flight).
type scenarioStep struct {
	advance time.Duration
	op      string
}

func runScenario(t *testing.T, b *circuitbreaker.Breaker, clock *time.Time, steps []scenarioStep) {
	t.Helper()

	for i, s := range steps {
		*clock = clock.Add(s.advance)

		switch s.op {
		case "ok", "fail":
			if err := b.Allow(); err != nil {
				t.Fatalf("step %d (%s): Allow() = %v, want nil", i, s.op, err)
			}
			if s.op == "fail" {
				b.Record(errUpstream)
			} else {
				b.Record(nil)
			}
		case "allow":
			if err := b.Allow(); err != nil {
				t.Fatalf("step %d: Allow() = %v, want nil", i, err)
			}
		case "denied":
			if err := b.Allow(); !errors.Is(err, circuitbreaker.ErrOpen) {
				t.Fatalf("step %d: Allow() = %v, want ErrOpen", i, err)
			}
		default:
			t.Fatalf("step %d: unknown op %q", i, s.op)
		}
	}
}

func newDriven(t *testing.T, cfg circuitbreaker.Config) (*circuitbreaker.Breaker, *time.Time) {
	t.Helper()

	clock := time.Unix(1700000000, 0)
	b := circuitbreaker.New(cfg)
	restore := circuitbreaker.SetNow(b, func() time.Time { return clock })
	t.Cleanup(restore)

	return b, &clock
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		input    circuitbreaker.Config
		expected circuitbreaker.State
	}{
		{
			name:     "zero config yields a working closed breaker",
			input:    circuitbreaker.Config{},
			expected: circuitbreaker.Closed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := circuitbreaker.New(tt.input)

			if got := b.State(); got != tt.expected {
				t.Errorf("State() = %v, want %v", got, tt.expected)
			}
			if err := b.Allow(); err != nil {
				t.Errorf("Allow() = %v, want nil", err)
			}
		})
	}

	t.Run("the documented defaults hold behaviorally", func(t *testing.T) {
		b, clock := newDriven(t, circuitbreaker.Config{})

		// MinRequests 10 with FailureRatio 0.5: nine samples never judge...
		runScenario(t, b, clock, []scenarioStep{
			{op: "ok"}, {op: "ok"}, {op: "ok"}, {op: "ok"}, {op: "ok"},
			{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"},
		})
		if got := b.State(); got != circuitbreaker.Closed {
			t.Fatalf("at 9 samples: State() = %v, want Closed", got)
		}

		// ...and the tenth at exactly 5/10 trips.
		runScenario(t, b, clock, []scenarioStep{{op: "fail"}})
		if got := b.State(); got != circuitbreaker.Open {
			t.Fatalf("at 5/10: State() = %v, want Open", got)
		}

		// OpenFor 30s: denied just before, probing just after;
		// HalfOpenProbes 1: the second concurrent probe is denied.
		runScenario(t, b, clock, []scenarioStep{
			{advance: 29 * time.Second, op: "denied"},
			{advance: 2 * time.Second, op: "allow"},
			{op: "denied"},
		})
	})

	t.Run("the default window ages samples out near ten seconds", func(t *testing.T) {
		fresh, clock := newDriven(t, circuitbreaker.Config{})
		runScenario(t, fresh, clock, []scenarioStep{
			{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"},
			{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"},
			{advance: 11 * time.Second, op: "fail"}, // old nine aged out: 1 sample, no trip
		})
		if got := fresh.State(); got != circuitbreaker.Closed {
			t.Errorf("after aging: State() = %v, want Closed", got)
		}

		hot, clock2 := newDriven(t, circuitbreaker.Config{})
		runScenario(t, hot, clock2, []scenarioStep{
			{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"},
			{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"},
			{advance: 5 * time.Second, op: "fail"}, // still inside the window: 10/10 trips
		})
		if got := hot.State(); got != circuitbreaker.Open {
			t.Errorf("inside the window: State() = %v, want Open", got)
		}
	})

	t.Run("a sub-bucket window is clamped, not a divide-by-zero", func(t *testing.T) {
		b := circuitbreaker.New(circuitbreaker.Config{Window: 5, MinRequests: 2}) // 5ns raw

		for range 3 {
			if err := b.Allow(); err != nil {
				t.Fatalf("Allow() = %v, want nil", err)
			}
			b.Record(nil)
		}
	})
}

func TestStateString(t *testing.T) {
	tests := []struct {
		name     string
		input    circuitbreaker.State
		expected string
	}{
		{name: "closed", input: circuitbreaker.Closed, expected: "closed"},
		{name: "open", input: circuitbreaker.Open, expected: "open"},
		{name: "half-open", input: circuitbreaker.HalfOpen, expected: "half-open"},
		{name: "unknown", input: circuitbreaker.State(99), expected: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.String(); got != tt.expected {
				t.Errorf("String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBreakerAllow(t *testing.T) {
	cfg := circuitbreaker.Config{FailureRatio: 0.5, MinRequests: 4, Window: 10 * time.Second, OpenFor: 30 * time.Second}

	tests := []struct {
		name     string
		input    []scenarioStep
		expected circuitbreaker.State
	}{
		{
			name:     "closed allows freely",
			input:    []scenarioStep{{op: "ok"}, {op: "ok"}, {op: "ok"}},
			expected: circuitbreaker.Closed,
		},
		{
			name: "open rejects until OpenFor elapses, then probes half-open",
			input: []scenarioStep{
				{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"}, // trip at 4/4
				{op: "denied"},
				{advance: 29 * time.Second, op: "denied"},
				{advance: 2 * time.Second, op: "allow"}, // past OpenFor: first probe
			},
			expected: circuitbreaker.HalfOpen,
		},
		{
			name: "half-open caps concurrent probes",
			input: []scenarioStep{
				{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"},
				{advance: 31 * time.Second, op: "allow"}, // probe slot taken, result pending
				{op: "denied"},                           // no second probe
			},
			expected: circuitbreaker.HalfOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, clock := newDriven(t, cfg)

			runScenario(t, b, clock, tt.input)

			if got := b.State(); got != tt.expected {
				t.Errorf("State() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestBreakerRecord(t *testing.T) {
	cfg := circuitbreaker.Config{FailureRatio: 0.5, MinRequests: 4, Window: 10 * time.Second, OpenFor: 30 * time.Second, HalfOpenProbes: 1}

	tests := []struct {
		name     string
		input    []scenarioStep
		expected circuitbreaker.State
	}{
		{
			name: "trips exactly at the ratio and sample floor",
			input: []scenarioStep{
				{op: "ok"}, {op: "ok"}, {op: "fail"}, {op: "fail"}, // 2/4 = 0.5
			},
			expected: circuitbreaker.Open,
		},
		{
			name: "below MinRequests never trips",
			input: []scenarioStep{
				{op: "fail"}, {op: "fail"}, {op: "fail"}, // 3/3 but samples < 4
			},
			expected: circuitbreaker.Closed,
		},
		{
			name: "old failures age out of the window",
			input: []scenarioStep{
				{op: "fail"}, {op: "fail"}, {op: "fail"},
				{advance: 11 * time.Second, op: "ok"}, // window rotated fully; failures gone
				{op: "ok"}, {op: "ok"}, {op: "fail"},  // 1/4 = 0.25 < 0.5
			},
			expected: circuitbreaker.Closed,
		},
		{
			name: "partial window rotation keeps recent history counting",
			input: []scenarioStep{
				{op: "fail"}, {op: "fail"},
				{advance: 3 * time.Second, op: "fail"}, // ring rotated 3 of 10 buckets
				{op: "fail"},                           // 4/4 across buckets = trip
			},
			expected: circuitbreaker.Open,
		},
		{
			name: "a failed probe reopens and restarts the timer",
			input: []scenarioStep{
				{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"},
				{advance: 31 * time.Second, op: "fail"},   // probe fails -> open again
				{advance: 29 * time.Second, op: "denied"}, // timer restarted: still open
			},
			expected: circuitbreaker.Open,
		},
		{
			name: "a successful probe closes with a fresh window",
			input: []scenarioStep{
				{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"},
				{advance: 31 * time.Second, op: "ok"},    // probe succeeds -> closed
				{op: "fail"}, {op: "fail"}, {op: "fail"}, // 3 samples: sick-period history must not count
			},
			expected: circuitbreaker.Closed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, clock := newDriven(t, cfg)

			runScenario(t, b, clock, tt.input)

			if got := b.State(); got != tt.expected {
				t.Errorf("State() = %v, want %v", got, tt.expected)
			}
		})
	}

	t.Run("all probes must succeed when HalfOpenProbes is larger", func(t *testing.T) {
		b, clock := newDriven(t, circuitbreaker.Config{
			FailureRatio: 0.5, MinRequests: 2, Window: 10 * time.Second,
			OpenFor: 30 * time.Second, HalfOpenProbes: 2,
		})

		runScenario(t, b, clock, []scenarioStep{
			{op: "fail"}, {op: "fail"},
			{advance: 31 * time.Second, op: "allow"}, {op: "allow"},
		})
		b.Record(nil)
		if got := b.State(); got != circuitbreaker.HalfOpen {
			t.Fatalf("after one of two probes: State() = %v, want HalfOpen", got)
		}
		b.Record(nil)
		if got := b.State(); got != circuitbreaker.Closed {
			t.Errorf("after both probes: State() = %v, want Closed", got)
		}
	})

	t.Run("a stale result with no probe in flight is ignored", func(t *testing.T) {
		b, clock := newDriven(t, circuitbreaker.Config{
			FailureRatio: 0.5, MinRequests: 2, Window: 10 * time.Second,
			OpenFor: 30 * time.Second, HalfOpenProbes: 2,
		})

		runScenario(t, b, clock, []scenarioStep{
			{op: "fail"}, {op: "fail"},
			{advance: 31 * time.Second, op: "ok"}, // first probe succeeds: 1 of 2
		})

		// A result from before the trip arrives with no probe in flight.
		// Counting it would fake the second probe success and close early.
		b.Record(nil)
		if got := b.State(); got != circuitbreaker.HalfOpen {
			t.Fatalf("after stale record: State() = %v, want HalfOpen", got)
		}

		runScenario(t, b, clock, []scenarioStep{{op: "ok"}}) // the real second probe
		if got := b.State(); got != circuitbreaker.Closed {
			t.Errorf("after the real probe: State() = %v, want Closed", got)
		}
	})

	t.Run("transitions fire OnStateChange outside the lock, in order", func(t *testing.T) {
		var transitions []string

		cb := circuitbreaker.Config{
			FailureRatio: 0.5, MinRequests: 2, Window: 10 * time.Second, OpenFor: 30 * time.Second,
		}
		var b *circuitbreaker.Breaker
		cb.OnStateChange = func(from, to circuitbreaker.State) {
			_ = b.State() // must not deadlock
			transitions = append(transitions, from.String()+"->"+to.String())
		}
		b = circuitbreaker.New(cb)
		clock := time.Unix(1700000000, 0)
		restore := circuitbreaker.SetNow(b, func() time.Time { return clock })
		t.Cleanup(restore)

		runScenario(t, b, &clock, []scenarioStep{
			{op: "fail"}, {op: "fail"},
			{advance: 31 * time.Second, op: "ok"},
		})

		want := []string{"closed->open", "open->half-open", "half-open->closed"}
		if len(transitions) != len(want) {
			t.Fatalf("transitions = %v, want %v", transitions, want)
		}
		for i := range want {
			if transitions[i] != want[i] {
				t.Errorf("transition %d = %q, want %q", i, transitions[i], want[i])
			}
		}
	})

	t.Run("exactly one goroutine wins the single probe slot", func(t *testing.T) {
		b, clock := newDriven(t, circuitbreaker.Config{
			FailureRatio: 0.5, MinRequests: 2, Window: 10 * time.Second,
			OpenFor: 30 * time.Second, HalfOpenProbes: 1,
		})
		runScenario(t, b, clock, []scenarioStep{{op: "fail"}, {op: "fail"}})
		*clock = clock.Add(31 * time.Second)

		var wg sync.WaitGroup
		var allowed sync.Map
		for i := range 8 {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				if b.Allow() == nil {
					allowed.Store(n, true)
				}
			}(i)
		}
		wg.Wait()

		var count int
		allowed.Range(func(_, _ any) bool { count++; return true })
		if count != 1 {
			t.Errorf("probe slots won = %d, want exactly 1", count)
		}
	})
}

func TestBreakerState(t *testing.T) {
	tests := []struct {
		name     string
		input    []scenarioStep
		expected circuitbreaker.State
	}{
		{name: "fresh breaker is closed", input: nil, expected: circuitbreaker.Closed},
		{
			name:     "a quiet open breaker reads open until traffic probes it",
			input:    []scenarioStep{{op: "fail"}, {op: "fail"}, {op: "fail"}, {op: "fail"}},
			expected: circuitbreaker.Open,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, clock := newDriven(t, circuitbreaker.Config{FailureRatio: 0.5, MinRequests: 4, Window: 10 * time.Second})

			runScenario(t, b, clock, tt.input)

			if got := b.State(); got != tt.expected {
				t.Errorf("State() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestBreakerRoundTripper(t *testing.T) {
	t.Run("any received response records success — a 500 does not trip", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(ts.Close)

		b := circuitbreaker.New(circuitbreaker.Config{MinRequests: 2, FailureRatio: 0.5})
		hc := &http.Client{Transport: b.RoundTripper(nil)}

		for range 5 {
			resp, err := hc.Get(ts.URL) //nolint:noctx // driving the RoundTripper is the point of this test
			if err != nil {
				t.Fatalf("Get() = %v", err)
			}
			_ = resp.Body.Close()
		}

		if got := b.State(); got != circuitbreaker.Closed {
			t.Errorf("State() = %v, want Closed — status policy belongs to the caller", got)
		}
	})

	t.Run("transport errors trip the breaker and then fail fast before dialing", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		ts.Close() // dead upstream

		b := circuitbreaker.New(circuitbreaker.Config{MinRequests: 2, FailureRatio: 0.5})
		hc := &http.Client{Transport: b.RoundTripper(nil)}

		for range 2 {
			if _, err := hc.Get(ts.URL); err == nil { //nolint:noctx,bodyclose // errors have no body; driving the RoundTripper is the point
				t.Fatal("Get() to a dead upstream succeeded")
			}
		}

		if got := b.State(); got != circuitbreaker.Open {
			t.Fatalf("State() = %v, want Open", got)
		}

		// ErrOpen — not "connection refused" — proves the base transport
		// was never consulted.
		if _, err := hc.Get(ts.URL); !errors.Is(err, circuitbreaker.ErrOpen) { //nolint:noctx,bodyclose // errors have no body
			t.Errorf("Get() = %v, want ErrOpen without dialing", err)
		}
	})
}
