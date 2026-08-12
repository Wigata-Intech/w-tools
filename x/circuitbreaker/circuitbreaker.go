package circuitbreaker

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

// State is the breaker's position in its cycle.
type State int

// Requests flow when Closed, fail fast when Open, probe when HalfOpen.
const (
	Closed State = iota
	Open
	HalfOpen
)

// String returns the state's name.
func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrOpen is returned by Allow while the circuit rejects attempts.
var ErrOpen = errors.New("circuitbreaker: open")

// Config configures New. Zero values mean the Default constants.
type Config struct {
	// FailureRatio trips the circuit at failures/total in the window.
	FailureRatio float64

	// MinRequests is the sample size below which the ratio is never judged.
	MinRequests int

	// Window is the sliding window the ratio is computed over.
	Window time.Duration

	// WindowBuckets subdivides Window.
	WindowBuckets int

	// OpenFor is how long the circuit stays open before probing.
	OpenFor time.Duration

	// HalfOpenProbes caps concurrent probes in half-open.
	HalfOpenProbes int

	// OnStateChange fires after each transition — outside the lock, on
	// the causing goroutine; it may call back into the breaker. Order is
	// not guaranteed under concurrent transitions.
	OnStateChange func(from, to State)
}

// Breaker is a three-state circuit breaker. Create one per upstream.
type Breaker struct {
	cfg        Config
	bucketSpan time.Duration

	mu           sync.Mutex
	state        State
	buckets      []bucket
	idx          int
	current      time.Time // start of the bucket at idx; zero until first Record
	openedAt     time.Time
	probes       int
	probeSuccess int

	now func() time.Time
}

type bucket struct {
	success int
	failure int
}

// New returns a closed Breaker.
func New(cfg Config) *Breaker {
	if cfg.FailureRatio <= 0 {
		cfg.FailureRatio = DefaultFailureRatio
	}
	if cfg.MinRequests <= 0 {
		cfg.MinRequests = DefaultMinRequests
	}
	if cfg.Window <= 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.WindowBuckets <= 0 {
		cfg.WindowBuckets = DefaultWindowBuckets
	}
	if cfg.OpenFor <= 0 {
		cfg.OpenFor = DefaultOpenFor
	}
	if cfg.HalfOpenProbes <= 0 {
		cfg.HalfOpenProbes = DefaultHalfOpenProbes
	}

	bucketSpan := cfg.Window / time.Duration(cfg.WindowBuckets)
	if bucketSpan <= 0 {
		bucketSpan = 1
	}

	return &Breaker{
		cfg:        cfg,
		bucketSpan: bucketSpan,
		buckets:    make([]bucket, cfg.WindowBuckets),
		now:        time.Now,
	}
}

// Allow reports whether an attempt may proceed: nil to go ahead, ErrOpen
// to fail fast. Every nil must be followed by exactly one Record.
func (b *Breaker) Allow() error {
	b.mu.Lock()

	var fired func()

	switch b.state {
	case Open:
		if b.now().Sub(b.openedAt) < b.cfg.OpenFor {
			b.mu.Unlock()
			return ErrOpen
		}

		fired = b.transition(HalfOpen)
		b.probes++
	case HalfOpen:
		if b.probes >= b.cfg.HalfOpenProbes {
			b.mu.Unlock()
			return ErrOpen
		}

		b.probes++
	case Closed:
	}

	b.mu.Unlock()
	if fired != nil {
		fired()
	}

	return nil
}

// Record reports an allowed attempt's outcome: nil is success, anything
// else is failure — which errors count is the caller's judgment. A
// result from before the breaker's last transition cannot be told apart
// from a current one; keep operation timeouts below OpenFor and the
// ambiguity never arises.
func (b *Breaker) Record(err error) {
	b.mu.Lock()

	var fired func()

	switch b.state {
	case Closed:
		b.advance()
		if err == nil {
			b.buckets[b.idx].success++
		} else {
			b.buckets[b.idx].failure++
		}

		if success, failure := b.totals(); success+failure >= b.cfg.MinRequests &&
			float64(failure)/float64(success+failure) >= b.cfg.FailureRatio {
			fired = b.open()
		}
	case HalfOpen:
		// No probe in flight: the result predates the transition.
		if b.probes == 0 {
			b.mu.Unlock()
			return
		}
		b.probes--

		if err != nil {
			fired = b.open()
		} else if b.probeSuccess++; b.probeSuccess >= b.cfg.HalfOpenProbes {
			fired = b.transition(Closed)
			clear(b.buckets)
			b.current = time.Time{}
		}
	case Open:
		// Late result from before the trip.
	}

	b.mu.Unlock()
	if fired != nil {
		fired()
	}
}

// State returns the current state. The open → half-open move happens on
// traffic, so a quiet breaker reads Open until the next Allow.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.state
}

// RoundTripper guards a native *http.Client: an open circuit fails the
// request before dialing. Transport errors — caller cancellations
// included — record as failures; any received response records success,
// whatever its status.
func (b *Breaker) RoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}

	return roundTripper{b: b, base: base}
}

type roundTripper struct {
	b    *Breaker
	base http.RoundTripper
}

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := rt.b.Allow(); err != nil {
		return nil, err
	}

	resp, err := rt.base.RoundTrip(req)
	rt.b.Record(err)

	return resp, err
}

// open (re)starts the recovery timer. Callers hold mu.
func (b *Breaker) open() func() {
	b.openedAt = b.now()

	return b.transition(Open)
}

// transition changes state and returns the OnStateChange invocation to
// run after unlock, or nil. Callers hold mu.
func (b *Breaker) transition(to State) func() {
	from := b.state
	b.state = to
	b.probes = 0
	b.probeSuccess = 0

	if b.cfg.OnStateChange == nil || from == to {
		return nil
	}

	return func() { b.cfg.OnStateChange(from, to) }
}

// advance rotates the ring to the bucket covering now, zeroing passed
// buckets. Callers hold mu.
func (b *Breaker) advance() {
	now := b.now()
	if b.current.IsZero() {
		b.current = now
		return
	}

	steps := int(now.Sub(b.current) / b.bucketSpan)
	if steps <= 0 {
		return
	}

	if steps >= len(b.buckets) {
		clear(b.buckets)
		b.idx = 0
		b.current = now

		return
	}

	for range steps {
		b.idx = (b.idx + 1) % len(b.buckets)
		b.buckets[b.idx] = bucket{}
		b.current = b.current.Add(b.bucketSpan)
	}
}

// totals sums the live window. Callers hold mu.
func (b *Breaker) totals() (int, int) {
	var success, failure int
	for _, bk := range b.buckets {
		success += bk.success
		failure += bk.failure
	}

	return success, failure
}
