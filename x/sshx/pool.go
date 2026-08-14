package sshx

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ErrNotReady is returned by Managed when no live connection exists right now
// (connecting, backing off, or reconnecting). It is returned immediately,
// never after blocking.
var ErrNotReady = errors.New("sshx: connection not ready")

// State is the lifecycle state of a Managed connection.
type State int

const (
	// StateConnecting is the initial state, before the first dial resolves.
	StateConnecting State = iota
	// StateReady means a live connection is established and usable.
	StateReady
	// StateBroken means the last attempt failed; a reconnect is scheduled.
	StateBroken
	// StateClosed is terminal: the Managed was closed and will not reconnect.
	StateClosed
)

// String renders the state for status lines.
func (s State) String() string {
	switch s {
	case StateReady:
		return "ready"
	case StateBroken:
		return "broken"
	case StateClosed:
		return "closed"
	default:
		return "connecting"
	}
}

// Pool owns a set of Managed connections and the dial-concurrency limit
// shared across them. Capping concurrent dials keeps a cold start of many
// hosts from tripping a server's sshd MaxStartups throttle or exhausting
// local sockets.
type Pool struct {
	sem chan struct{}

	mu    sync.Mutex
	conns []*Managed
	dead  bool
}

// NewPool returns a pool permitting at most maxConcurrentDials dials in
// flight across all its connections. Non-positive means [DefaultMaxDials].
func NewPool(maxConcurrentDials int) *Pool {
	if maxConcurrentDials <= 0 {
		maxConcurrentDials = DefaultMaxDials
	}
	return &Pool{sem: make(chan struct{}, maxConcurrentDials)}
}

// ManagedConfig configures one self-healing connection.
type ManagedConfig struct {
	// Dial establishes the connection; it is called for the first connect and
	// every reconnect. Its ctx is canceled when the Managed closes.
	Dial func(ctx context.Context) (*Client, error)
	// OnStateChange, when non-nil, is invoked on every lifecycle transition
	// with the new state and the error that drove it (nil on recovery and on
	// close). Transitions from the maintenance loop arrive in order; the
	// StateClosed notification comes from the closing goroutine and is not
	// ordered relative to a loop notification already in flight — State()
	// itself is always accurate after Close. Keep the callback fast and
	// non-blocking.
	OnStateChange func(s State, err error)
}

// Add registers a connection and starts maintaining it in the background,
// returning immediately. The returned Managed is usable at once — its
// execution methods report ErrNotReady until the first dial succeeds.
func (p *Pool) Add(cfg ManagedConfig) *Managed {
	return p.add(cfg, backoffBase, backoffCap, probeEvery)
}

// Close tears down every Managed connection. Idempotent; Add after Close
// returns an already-closed Managed.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.dead {
		p.mu.Unlock()
		return
	}
	p.dead = true
	conns := p.conns
	p.conns = nil
	p.mu.Unlock()
	for _, m := range conns {
		m.Close()
	}
}

// add is Add with injectable timings, the seam tests dial through.
func (p *Pool) add(cfg ManagedConfig, base, maxDelay, probe time.Duration) *Managed {
	ctx, cancel := context.WithCancel(context.Background()) //#nosec G118 -- cancel is stored and invoked by Managed.Close
	m := &Managed{
		pool:      p,
		dialFn:    cfg.Dial,
		onChange:  cfg.OnStateChange,
		cancel:    cancel,
		reconnect: make(chan struct{}, 1),
		baseDelay: base,
		maxDelay:  maxDelay,
		probe:     probe,
	}
	p.mu.Lock()
	closed := p.dead
	if !closed {
		p.conns = append(p.conns, m)
	}
	p.mu.Unlock()
	if closed {
		m.Close()
		return m
	}
	go m.manage(ctx)
	return m
}

// Managed is a self-healing connection to one host: it keeps a single live
// Client, redialing with jittered exponential backoff after failures, and
// multiplexes all execution over it — only dials pay the handshake.
type Managed struct {
	pool     *Pool
	dialFn   func(ctx context.Context) (*Client, error)
	onChange func(s State, err error)

	cancel    context.CancelFunc
	closeOnce sync.Once

	reconnect chan struct{} // buffered(1): a pending redial request

	// Backoff/probe timings, defaulted from the package constants in Add;
	// overridable in tests for fast deterministic runs.
	baseDelay time.Duration
	maxDelay  time.Duration
	probe     time.Duration

	mu      sync.Mutex
	client  *Client
	state   State
	lastErr error
	closed  bool
}

// State returns the current lifecycle state.
func (m *Managed) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Err returns the error from the last failed attempt, or nil.
func (m *Managed) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

// Client returns the live connection for direct reuse — an interactive
// session on an already-pooled host without a second handshake — or nil when
// not currently ready. The Managed still owns it: don't Close it.
func (m *Managed) Client() *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateReady {
		return nil
	}
	return m.client
}

// CombinedOutput runs cmd on the live connection. It returns ErrNotReady
// immediately — without blocking — when no connection is established, so a
// caller polling many hosts never stalls on a dead one, and ErrClosed after
// Close. A transport-level failure schedules a reconnect; a non-zero exit
// does not.
func (m *Managed) CombinedOutput(ctx context.Context, cmd string) (string, error) {
	c, err := m.ready()
	if err != nil {
		return "", err
	}
	out, err := c.CombinedOutput(ctx, cmd)
	if fatalConnErr(err) {
		m.signalReconnect(c, err)
	}
	return out, err
}

// Output runs cmd on the live connection with the same not-ready and
// reconnect semantics as CombinedOutput.
func (m *Managed) Output(ctx context.Context, cmd string) (Result, error) {
	c, err := m.ready()
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	res, err := c.Output(ctx, cmd)
	if fatalConnErr(err) {
		m.signalReconnect(c, err)
	}
	return res, err
}

// Close stops maintaining the connection and tears down the live transport.
// StateClosed is terminal; subsequent operations return ErrClosed.
func (m *Managed) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.state = StateClosed
		cb := m.onChange
		m.mu.Unlock()
		m.cancel()
		if cb != nil {
			cb(StateClosed, nil)
		}
	})
}

// ready hands out the live client or fails fast.
func (m *Managed) ready() (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if m.state != StateReady || m.client == nil {
		return nil, ErrNotReady
	}
	return m.client, nil
}

// manage is the per-connection maintenance loop: dial (with backoff on
// failure), serve the live connection until it dies or a redial is requested,
// then repeat — until Close. ctx is the Managed's lifetime, canceled by Close.
func (m *Managed) manage(ctx context.Context) {
	attempt := 0
	for {
		c, err := m.dial(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return
			}
			attempt++
			m.set(StateBroken, nil, err)
			if !m.wait(ctx, m.backoff(attempt)) {
				return
			}
			continue
		}
		attempt = 0
		// Drain any stale redial token before going Ready: a failure signaled
		// against the connection just torn down must not kill its successor.
		// Nothing legitimate can queue here — the state is Broken until set.
		select {
		case <-m.reconnect:
		default:
		}
		m.set(StateReady, c, nil)
		redial := m.serve(ctx, c)
		m.teardown(c)
		if !redial {
			return
		}
	}
}

// dial acquires a slot from the pool's dial semaphore (respecting Close) and
// runs the dial function.
func (m *Managed) dial(ctx context.Context) (*Client, error) {
	select {
	case m.pool.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, context.Canceled
	}
	defer func() { <-m.pool.sem }()
	return m.dialFn(ctx)
}

// serve blocks while c is the live connection, probing its health. It returns
// true to request a redial (connection died or a reconnect was signalled) or
// false when the Managed is closing for good.
func (m *Managed) serve(ctx context.Context, c *Client) bool {
	t := time.NewTicker(m.probe)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-m.reconnect:
			// signalReconnect already recorded Broken and its cause; this
			// set replays it so the callback fires from the ordered loop.
			m.set(StateBroken, nil, m.Err())
			return true
		case <-t.C:
			if err := c.Ping(ctx); err != nil {
				m.set(StateBroken, nil, err)
				return true
			}
		}
	}
}

// teardown closes the dead connection and clears it from the model.
func (m *Managed) teardown(c *Client) {
	_ = c.Close()
	m.mu.Lock()
	if m.client == c {
		m.client = nil
	}
	m.mu.Unlock()
}

// signalReconnect marks the connection broken and requests a redial, but only
// when c is still the live client — a straggler failure from a connection the
// loop already replaced must not tear down its healthy successor. Non-blocking;
// the callback fires later, from the maintenance loop, so transitions stay
// ordered.
func (m *Managed) signalReconnect(c *Client, err error) {
	m.mu.Lock()
	changed := !m.closed && m.state == StateReady && m.client == c
	if changed {
		m.state = StateBroken
		m.lastErr = err
	}
	m.mu.Unlock()
	if !changed {
		return
	}
	select {
	case m.reconnect <- struct{}{}:
	default:
	}
}

// set updates state, live client, and last error atomically, then notifies.
// After Close it is a no-op: StateClosed is terminal.
func (m *Managed) set(state State, c *Client, err error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.state = state
	if c != nil || state == StateReady {
		m.client = c
	}
	m.lastErr = err
	cb := m.onChange
	m.mu.Unlock()
	if cb != nil {
		cb(state, err)
	}
}

// wait sleeps for d, returning false if Close happened first.
func (m *Managed) wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// backoff returns capped exponential backoff with full jitter, so many hosts
// recovering from one shared blip don't reconnect in lockstep.
func (m *Managed) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 6) // base << 6 already dwarfs any sane cap
	d := m.baseDelay << shift
	if d > m.maxDelay || d <= 0 {
		d = m.maxDelay
	}
	//#nosec G404 -- jitter spacing, not a security decision
	return time.Duration(rand.Int64N(int64(d))) + 1
}

// fatalConnErr reports whether err means the transport is gone — as opposed
// to the remote command merely exiting non-zero, which leaves the connection
// perfectly usable.
func fatalConnErr(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *ssh.ExitError
	var missingErr *ssh.ExitMissingError
	if errors.As(err, &exitErr) || errors.As(err, &missingErr) {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
