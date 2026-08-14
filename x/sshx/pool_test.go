package sshx_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Wigata-Intech/w-tools/x/sshx"
)

var (
	errDialRefused = errors.New("dial refused")
	errUnreachable = errors.New("unreachable")
	errConnLost    = errors.New("connection lost")
)

// Fast deterministic Managed timings for tests.
const (
	testBase  = 5 * time.Millisecond
	testCap   = 50 * time.Millisecond
	testProbe = 10 * time.Millisecond
)

// stateLog is a race-safe recorder for OnStateChange transitions.
type stateLog struct {
	mu     sync.Mutex
	events []sshx.State
}

func (l *stateLog) record(s sshx.State, _ error) {
	l.mu.Lock()
	l.events = append(l.events, s)
	l.mu.Unlock()
}

func (l *stateLog) snapshot() []sshx.State {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]sshx.State(nil), l.events...)
}

// hasSequence reports whether want appears in the log as a subsequence.
func (l *stateLog) hasSequence(want ...sshx.State) bool {
	i := 0
	for _, e := range l.snapshot() {
		if i < len(want) && e == want[i] {
			i++
		}
	}
	return i == len(want)
}

// managedDial returns a Managed dial function targeting s.
func managedDial(s *testServer) func(context.Context) (*sshx.Client, error) {
	return func(ctx context.Context) (*sshx.Client, error) {
		return sshx.Dial(ctx, s.addr(), testConfig(s))
	}
}

// newReadyManaged adds a connection to p and waits for its first Ready.
func newReadyManaged(t *testing.T, p *sshx.Pool, s *testServer, log *stateLog) *sshx.Managed {
	t.Helper()
	cfg := sshx.ManagedConfig{Dial: managedDial(s)}
	if log != nil {
		cfg.OnStateChange = log.record
	}
	m := p.AddWithTimings(cfg, testBase, testCap, testProbe)
	waitFor(t, 5*time.Second, func() bool { return m.State() == sshx.StateReady })
	return m
}

func TestState_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    sshx.State
		expected string
	}{
		{name: "ready", input: sshx.StateReady, expected: "ready"},
		{name: "broken", input: sshx.StateBroken, expected: "broken"},
		{name: "closed", input: sshx.StateClosed, expected: "closed"},
		{name: "connecting", input: sshx.StateConnecting, expected: "connecting"},
		{name: "unknown value defaults to connecting", input: sshx.State(42), expected: "connecting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.input.String(); got != tt.expected {
				t.Errorf("String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestNewPool(t *testing.T) {
	t.Parallel()

	t.Run("non-positive limit still dials", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		// Production Add is safe here: the dial succeeds first try, so its
		// slow backoff and probe timings never engage.
		m := p.Add(sshx.ManagedConfig{Dial: managedDial(s)})
		waitFor(t, 5*time.Second, func() bool { return m.State() == sshx.StateReady })
		out, err := m.CombinedOutput(context.Background(), "ok")
		if err != nil {
			t.Fatalf("CombinedOutput() error = %v", err)
		}
		if out == "" {
			t.Error("CombinedOutput() = empty, want scripted output")
		}
	})
}

func TestAdd(t *testing.T) {
	t.Parallel()

	t.Run("connecting until dial completes", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		release := make(chan struct{})
		dial := func(ctx context.Context) (*sshx.Client, error) {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return managedDial(s)(ctx)
		}
		m := p.AddWithTimings(sshx.ManagedConfig{Dial: dial}, testBase, testCap, testProbe)
		if got := m.State(); got != sshx.StateConnecting {
			t.Errorf("State() = %v, want %v", got, sshx.StateConnecting)
		}

		start := time.Now()
		_, err := m.CombinedOutput(context.Background(), "ok")
		if !errors.Is(err, sshx.ErrNotReady) {
			t.Fatalf("CombinedOutput() error = %v, want ErrNotReady", err)
		}
		if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
			t.Errorf("CombinedOutput() blocked %v, want an immediate return", elapsed)
		}

		close(release)
		waitFor(t, 5*time.Second, func() bool { return m.State() == sshx.StateReady })
		if _, err := m.CombinedOutput(context.Background(), "ok"); err != nil {
			t.Errorf("CombinedOutput() after ready error = %v", err)
		}
	})

	t.Run("state transitions on transport loss", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		log := &stateLog{}
		m := newReadyManaged(t, p, s, log)

		events := log.snapshot()
		if len(events) == 0 || events[0] != sshx.StateReady {
			t.Fatalf("first transition = %v, want StateReady", events)
		}

		s.killConns()
		waitFor(t, 5*time.Second, func() bool {
			return log.hasSequence(sshx.StateReady, sshx.StateBroken, sshx.StateReady)
		})
		if _, err := m.CombinedOutput(context.Background(), "ok"); err != nil {
			t.Errorf("CombinedOutput() after self-heal error = %v", err)
		}
		if err := m.Err(); err != nil {
			t.Errorf("Err() after recovery = %v, want nil", err)
		}
	})

	t.Run("dial failure then recovery", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		errDial := errDialRefused
		var healthy atomic.Bool
		log := &stateLog{}
		dial := func(ctx context.Context) (*sshx.Client, error) {
			if !healthy.Load() {
				return nil, errDial
			}
			return managedDial(s)(ctx)
		}
		m := p.AddWithTimings(sshx.ManagedConfig{Dial: dial, OnStateChange: log.record}, testBase, testCap, testProbe)

		waitFor(t, 5*time.Second, func() bool { return m.State() == sshx.StateBroken })
		if err := m.Err(); !errors.Is(err, errDial) {
			t.Errorf("Err() = %v, want %v", err, errDial)
		}

		healthy.Store(true)
		waitFor(t, 5*time.Second, func() bool { return m.State() == sshx.StateReady })
		if err := m.Err(); err != nil {
			t.Errorf("Err() after recovery = %v, want nil", err)
		}
		if !log.hasSequence(sshx.StateBroken, sshx.StateReady) {
			t.Errorf("transitions = %v, want Broken then Ready", log.snapshot())
		}
	})

	t.Run("close during backoff", func(t *testing.T) {
		t.Parallel()
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		var calls atomic.Int32
		dial := func(context.Context) (*sshx.Client, error) {
			calls.Add(1)
			return nil, errDialRefused
		}
		// Hour-long backoff pins the manage loop inside its wait so Close
		// deterministically interrupts it.
		m := p.AddWithTimings(sshx.ManagedConfig{Dial: dial}, time.Hour, time.Hour, time.Hour)
		waitFor(t, 5*time.Second, func() bool { return m.State() == sshx.StateBroken })
		m.Close()
		if got := calls.Load(); got != 1 {
			t.Errorf("dial calls = %d, want 1", got)
		}
	})

	t.Run("close while awaiting dial slot", func(t *testing.T) {
		t.Parallel()
		p := sshx.NewPool(1)
		t.Cleanup(p.Close)
		entered := make(chan struct{})
		var once sync.Once
		holdDial := func(ctx context.Context) (*sshx.Client, error) {
			once.Do(func() { close(entered) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		a := p.AddWithTimings(sshx.ManagedConfig{Dial: holdDial}, testBase, testCap, testProbe)
		<-entered // a now holds the pool's only dial slot

		var bCalls atomic.Int32
		b := p.AddWithTimings(sshx.ManagedConfig{Dial: func(context.Context) (*sshx.Client, error) {
			bCalls.Add(1)
			return nil, errUnreachable
		}}, testBase, testCap, testProbe)
		b.Close()
		if got := bCalls.Load(); got != 0 {
			t.Errorf("dial calls for blocked Managed = %d, want 0", got)
		}
		a.Close()
	})
}

func TestPoolClose(t *testing.T) {
	t.Parallel()

	t.Run("closes managed connections", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		log := &stateLog{}
		m := newReadyManaged(t, p, s, log)

		p.Close()
		waitFor(t, 5*time.Second, func() bool {
			_, err := m.CombinedOutput(context.Background(), "ok")
			return errors.Is(err, sshx.ErrClosed)
		})
		if got := m.State(); got != sshx.StateClosed {
			t.Errorf("State() after pool close = %v, want StateClosed", got)
		}
		if c := m.Client(); c != nil {
			t.Errorf("Client() after pool close = %v, want nil", c)
		}
		// Absence check: no transitions may arrive after Close.
		before := len(log.snapshot())
		time.Sleep(50 * time.Millisecond)
		if after := len(log.snapshot()); after != before {
			t.Errorf("transitions kept arriving after Close: %d -> %d", before, after)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		t.Parallel()
		p := sshx.NewPool(0)
		p.Close()
		p.Close()
	})

	t.Run("add after close", func(t *testing.T) {
		t.Parallel()
		p := sshx.NewPool(0)
		p.Close()
		var calls atomic.Int32
		m := p.AddWithTimings(sshx.ManagedConfig{Dial: func(context.Context) (*sshx.Client, error) {
			calls.Add(1)
			return nil, errUnreachable
		}}, testBase, testCap, testProbe)

		if _, err := m.CombinedOutput(context.Background(), "ok"); !errors.Is(err, sshx.ErrClosed) {
			t.Errorf("CombinedOutput() error = %v, want ErrClosed", err)
		}
		res, err := m.Output(context.Background(), "ok")
		if !errors.Is(err, sshx.ErrClosed) {
			t.Errorf("Output() error = %v, want ErrClosed", err)
		}
		if res.ExitCode != -1 {
			t.Errorf("Output() ExitCode = %d, want -1", res.ExitCode)
		}
		if got := calls.Load(); got != 0 {
			t.Errorf("dial calls = %d, want 0", got)
		}
	})
}

func TestManagedClient(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{})
	p := sshx.NewPool(0)
	t.Cleanup(p.Close)
	release := make(chan struct{})
	dial := func(ctx context.Context) (*sshx.Client, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return managedDial(s)(ctx)
	}
	m := p.AddWithTimings(sshx.ManagedConfig{Dial: dial}, testBase, testCap, testProbe)

	if c := m.Client(); c != nil {
		t.Errorf("Client() before ready = %v, want nil", c)
	}
	close(release)
	waitFor(t, 5*time.Second, func() bool { return m.State() == sshx.StateReady })
	c := m.Client()
	if c == nil {
		t.Fatal("Client() when ready = nil, want live client")
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("Ping() on shared client error = %v", err)
	}
}

func TestManagedCombinedOutput(t *testing.T) {
	t.Parallel()

	t.Run("runs on the live connection", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		m := newReadyManaged(t, p, s, nil)
		out, err := m.CombinedOutput(context.Background(), "ok")
		if err != nil {
			t.Fatalf("CombinedOutput() error = %v", err)
		}
		if out == "" {
			t.Error("CombinedOutput() = empty, want scripted output")
		}
	})

	t.Run("not ready", func(t *testing.T) {
		t.Parallel()
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		m := p.AddWithTimings(sshx.ManagedConfig{Dial: func(ctx context.Context) (*sshx.Client, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}, testBase, testCap, testProbe)
		if _, err := m.CombinedOutput(context.Background(), "ok"); !errors.Is(err, sshx.ErrNotReady) {
			t.Errorf("CombinedOutput() error = %v, want ErrNotReady", err)
		}
	})

	t.Run("transport failure schedules reconnect", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		log := &stateLog{}
		// Hour-long probe: only the failed exec can trigger the reconnect.
		m := p.AddWithTimings(sshx.ManagedConfig{Dial: managedDial(s), OnStateChange: log.record},
			testBase, testCap, time.Hour)
		waitFor(t, 5*time.Second, func() bool { return m.State() == sshx.StateReady })

		s.killConns()
		_, err := m.CombinedOutput(context.Background(), "ok")
		if err == nil {
			t.Fatal("CombinedOutput() error = nil, want transport failure")
		}
		if errors.Is(err, sshx.ErrNotReady) {
			t.Fatalf("CombinedOutput() error = %v, want a transport error from the live attempt", err)
		}
		waitFor(t, 5*time.Second, func() bool {
			return log.hasSequence(sshx.StateReady, sshx.StateBroken, sshx.StateReady)
		})
		if _, err := m.CombinedOutput(context.Background(), "ok"); err != nil {
			t.Errorf("CombinedOutput() after reconnect error = %v", err)
		}
	})
}

func TestManagedOutput(t *testing.T) {
	t.Parallel()

	t.Run("runs on the live connection", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		m := newReadyManaged(t, p, s, nil)
		res, err := m.Output(context.Background(), "ok")
		if err != nil {
			t.Fatalf("Output() error = %v", err)
		}
		if res.ExitCode != 0 || len(res.Stdout) == 0 {
			t.Errorf("Output() = %+v, want exit 0 with stdout", res)
		}
	})

	t.Run("non-zero exit keeps the connection", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		log := &stateLog{}
		m := newReadyManaged(t, p, s, log)

		res, err := m.Output(context.Background(), "fail")
		var exitErr *ssh.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("Output() error = %v, want *ssh.ExitError", err)
		}
		if res.ExitCode != 3 {
			t.Errorf("ExitCode = %d, want 3", res.ExitCode)
		}
		if got := m.State(); got != sshx.StateReady {
			t.Errorf("State() after non-zero exit = %v, want StateReady", got)
		}
		if log.hasSequence(sshx.StateReady, sshx.StateBroken) {
			t.Errorf("transitions = %v, non-zero exit must not break the connection", log.snapshot())
		}
		if _, err := m.Output(context.Background(), "ok"); err != nil {
			t.Errorf("Output() on same connection error = %v", err)
		}
	})

	t.Run("not ready", func(t *testing.T) {
		t.Parallel()
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		m := p.AddWithTimings(sshx.ManagedConfig{Dial: func(ctx context.Context) (*sshx.Client, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}, testBase, testCap, testProbe)
		res, err := m.Output(context.Background(), "ok")
		if !errors.Is(err, sshx.ErrNotReady) {
			t.Errorf("Output() error = %v, want ErrNotReady", err)
		}
		if res.ExitCode != -1 {
			t.Errorf("ExitCode = %d, want -1", res.ExitCode)
		}
	})

	t.Run("transport failure schedules reconnect", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		log := &stateLog{}
		// Hour-long probe: only the failed exec can trigger the reconnect.
		m := p.AddWithTimings(sshx.ManagedConfig{Dial: managedDial(s), OnStateChange: log.record},
			testBase, testCap, time.Hour)
		waitFor(t, 5*time.Second, func() bool { return m.State() == sshx.StateReady })

		s.killConns()
		if _, err := m.Output(context.Background(), "ok"); err == nil {
			t.Fatal("Output() error = nil, want transport failure")
		}
		waitFor(t, 5*time.Second, func() bool {
			return log.hasSequence(sshx.StateReady, sshx.StateBroken, sshx.StateReady)
		})
	})
}

func TestSignalReconnect(t *testing.T) {
	t.Parallel()

	t.Run("straggler from a replaced client is ignored", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		log := &stateLog{}
		m := newReadyManaged(t, p, s, log)

		old := m.Client()
		s.killConns()
		_, _ = m.CombinedOutput(context.Background(), "ok") // fatal: schedules the redial
		waitFor(t, 5*time.Second, func() bool {
			return m.State() == sshx.StateReady && m.Client() != old
		})

		before := len(log.snapshot())
		m.SignalReconnect(old, errConnLost)
		if got := m.State(); got != sshx.StateReady {
			t.Errorf("State() after straggler signal = %v, want StateReady", got)
		}
		time.Sleep(50 * time.Millisecond) // absence check: no teardown may follow
		if got := m.State(); got != sshx.StateReady {
			t.Errorf("State() after straggler grace = %v, want StateReady", got)
		}
		if after := len(log.snapshot()); after != before {
			t.Errorf("straggler signal caused transitions: %d -> %d", before, after)
		}
	})

	t.Run("ignored after close", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{execs: testExecs()})
		p := sshx.NewPool(0)
		t.Cleanup(p.Close)
		m := newReadyManaged(t, p, s, nil)
		c := m.Client()
		m.Close()
		m.SignalReconnect(c, errConnLost)
		if got := m.State(); got != sshx.StateClosed {
			t.Errorf("State() = %v, want StateClosed", got)
		}
	})
}

func TestManagedDialAfterClose(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{})
	p := sshx.NewPool(0)
	t.Cleanup(p.Close)

	entered := make(chan struct{})
	unblock := make(chan struct{})
	dialed := make(chan *sshx.Client, 1)
	m := p.AddWithTimings(sshx.ManagedConfig{Dial: func(context.Context) (*sshx.Client, error) {
		close(entered)
		<-unblock
		// Deliberately ignores the managed ctx: this models a dial that was
		// already past cancellation and completed anyway.
		c, err := sshx.Dial(context.Background(), s.addr(), testConfig(s)) //nolint:contextcheck // ignoring the inherited ctx is the scenario under test
		if err == nil {
			dialed <- c
		}
		return c, err
	}}, testBase, testCap, testProbe)

	<-entered // the dial must be in flight before Close, or it never starts
	m.Close()
	close(unblock) // the dial now completes into a closed Managed
	select {
	case c := <-dialed:
		// The late connection must be discarded, not adopted.
		waitFor(t, 5*time.Second, func() bool {
			return c.Ping(context.Background()) != nil
		})
	case <-time.After(5 * time.Second):
		t.Fatal("dial never completed")
	}
	if got := m.State(); got != sshx.StateClosed {
		t.Errorf("State() = %v, want StateClosed", got)
	}
	if got := m.Client(); got != nil {
		t.Errorf("Client() = %v, want nil", got)
	}
}

func TestManagedClose(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{execs: testExecs()})
	p := sshx.NewPool(0)
	t.Cleanup(p.Close)
	m := newReadyManaged(t, p, s, nil)

	m.Close()
	m.Close()
	if _, err := m.CombinedOutput(context.Background(), "ok"); !errors.Is(err, sshx.ErrClosed) {
		t.Errorf("CombinedOutput() after Close error = %v, want ErrClosed", err)
	}
	if got := m.State(); got != sshx.StateClosed {
		t.Errorf("State() after Close = %v, want StateClosed", got)
	}
}

func TestBackoff(t *testing.T) {
	t.Parallel()
	p := sshx.NewPool(0)
	t.Cleanup(p.Close)
	m := p.AddWithTimings(sshx.ManagedConfig{Dial: func(ctx context.Context) (*sshx.Client, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}, testBase, testCap, testProbe)

	for attempt := range 11 {
		d := m.Backoff(attempt)
		if d <= 0 {
			t.Errorf("Backoff(%d) = %v, want > 0", attempt, d)
		}
		if d > testCap {
			t.Errorf("Backoff(%d) = %v, want <= cap %v", attempt, d, testCap)
		}
		// Attempts below 1 are clamped to 1: first-attempt jitter never
		// exceeds the base delay.
		if attempt <= 1 && d > testBase {
			t.Errorf("Backoff(%d) = %v, want <= base %v", attempt, d, testBase)
		}
	}
}

func TestFatalConnErr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    error
		expected bool
	}{
		{name: "nil", input: nil, expected: false},
		{name: "exit error", input: &ssh.ExitError{}, expected: false},
		{name: "exit missing", input: &ssh.ExitMissingError{}, expected: false},
		{name: "context canceled", input: context.Canceled, expected: false},
		{name: "context deadline", input: context.DeadlineExceeded, expected: false},
		{name: "generic transport error", input: errConnLost, expected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sshx.FatalConnErr(tt.input); got != tt.expected {
				t.Errorf("FatalConnErr(%v) = %t, want %t", tt.input, got, tt.expected)
			}
		})
	}
}
