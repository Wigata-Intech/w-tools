package sshx

import (
	"context"
	"time"
)

// AddWithTimings is Pool.Add with fast deterministic backoff and probe
// timings for tests.
func (p *Pool) AddWithTimings(cfg ManagedConfig, base, maxDelay, probe time.Duration) *Managed {
	return p.add(cfg, base, maxDelay, probe)
}

// Backoff exposes the jittered backoff for bounds assertions.
func (m *Managed) Backoff(attempt int) time.Duration { return m.backoff(attempt) }

// DialWithKeepalive is Dial with a shrunken keepalive interval for tests.
func DialWithKeepalive(ctx context.Context, addr string, cfg Config, tick time.Duration) (*Client, error) {
	return dial(ctx, addr, cfg, tick)
}

// FatalConnErr exposes the transport-death classifier.
func FatalConnErr(err error) bool { return fatalConnErr(err) }

// SignalReconnect exposes the straggler-guarded reconnect signal so the
// guard's rejection paths are deterministically testable.
func (m *Managed) SignalReconnect(c *Client, err error) { m.signalReconnect(c, err) }
