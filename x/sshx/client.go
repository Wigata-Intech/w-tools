package sshx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Dial stages reported by [DialError.Stage]. The stage states with certainty
// where a dial died; it never guesses at causes inside a stage.
const (
	// StageNetwork: the TCP connection could not be established.
	StageNetwork = "network"
	// StageHostKey: the transport came up but the host-key policy refused the
	// server's identity. The policy's error is retrievable with errors.As.
	StageHostKey = "hostkey"
	// StageHandshake: the SSH handshake failed after host-key verification
	// passed — authentication exhaustion, protocol failure, or peer close.
	StageHandshake = "handshake"
)

// ErrHostKeyRequired is returned by Dial when Config.HostKey is nil. There is
// no insecure default; opting out takes an explicit InsecureAcceptAny.
var ErrHostKeyRequired = errors.New("sshx: host key policy required")

// ErrAuthRequired is returned by Dial when Config.Auth is empty.
var ErrAuthRequired = errors.New("sshx: at least one auth method required")

// ErrClosed is returned for operations on a closed client, session, or pool.
var ErrClosed = errors.New("sshx: closed")

// DialError reports a failed Dial with the stage it died in.
type DialError struct {
	Stage string // StageNetwork, StageHostKey, or StageHandshake
	Addr  string
	Err   error
}

// Error implements error.
func (e *DialError) Error() string {
	return fmt.Sprintf("sshx: dial %s: %s: %v", e.Addr, e.Stage, e.Err)
}

// Unwrap exposes the underlying error to errors.Is and errors.As.
func (e *DialError) Unwrap() error { return e.Err }

// Config configures a Dial. HostKey and at least one Auth method are
// required; there are no insecure defaults.
type Config struct {
	User    string
	Auth    []ssh.AuthMethod
	HostKey ssh.HostKeyCallback
}

// Client is one authenticated SSH connection. Command execution and sessions
// multiplex over it; only Dial pays the handshake.
type Client struct {
	c    *ssh.Client
	done chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// hostKeyRecorder keeps the host-key policy's own error so Dial can surface
// it typed, however x/crypto wraps what the callback returned.
type hostKeyRecorder struct {
	cb ssh.HostKeyCallback

	mu  sync.Mutex
	err error
}

func (r *hostKeyRecorder) check(hostname string, remote net.Addr, key ssh.PublicKey) error {
	err := r.cb(hostname, remote, key)
	if err != nil {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
	}
	return err
}

func (r *hostKeyRecorder) recorded() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Dial connects to addr (host:port) and authenticates. ctx bounds the whole
// dial — TCP connect and SSH handshake; without a deadline [DefaultDialTimeout]
// applies. Failures are reported as *DialError with the stage that
// died, and host-key policy errors are retrievable from it with errors.As.
func Dial(ctx context.Context, addr string, cfg Config) (*Client, error) {
	return dial(ctx, addr, cfg, keepaliveInterval)
}

// dial is Dial with an injectable keepalive interval, the seam tests use.
func dial(ctx context.Context, addr string, cfg Config, keepaliveEvery time.Duration) (*Client, error) {
	if cfg.HostKey == nil {
		return nil, ErrHostKeyRequired
	}
	if len(cfg.Auth) == 0 {
		return nil, ErrAuthRequired
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultDialTimeout)
		defer cancel()
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, &DialError{Stage: StageNetwork, Addr: addr, Err: err}
	}
	// The handshake below doesn't take ctx; a conn deadline enforces it, and
	// the AfterFunc covers cancellation ahead of the deadline. SetDeadline
	// only fails on a closed conn, which the handshake then reports itself.
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	rec := &hostKeyRecorder{cb: cfg.HostKey}
	sconn, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            cfg.Auth,
		HostKeyCallback: rec.check,
	})
	if err != nil {
		_ = conn.Close()
		if hkErr := rec.recorded(); hkErr != nil {
			return nil, &DialError{Stage: StageHostKey, Addr: addr, Err: hkErr}
		}
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		return nil, &DialError{Stage: StageHandshake, Addr: addr, Err: err}
	}
	_ = conn.SetDeadline(time.Time{})
	c := &Client{c: ssh.NewClient(sconn, chans, reqs), done: make(chan struct{})}
	//nolint:contextcheck // keepalive is connection-lifetime, not dial-scoped
	go c.keepalive(keepaliveEvery) //#nosec G118 -- keepalive outlives the dial ctx by design; it stops on Close
	return c, nil
}

// Ping sends an OpenSSH keepalive request and reports whether the peer
// answered. It is deadline-guarded — [DefaultPingTimeout] unless ctx sets less — so a
// black-hole peer that never replies cannot wedge the caller (golang/go#21478).
func (c *Client) Ping(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultPingTimeout)
		defer cancel()
	}
	ch := make(chan error, 1) // buffered: the goroutine never leaks on timeout
	go func() {
		_, _, err := c.c.SendRequest("keepalive@openssh.com", true, nil)
		ch <- err
	}()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close terminates the connection and stops the keepalive goroutine. It is
// idempotent; every call returns the first close's error.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.closeErr = c.c.Close()
	})
	return c.closeErr
}

// keepalive pings the peer so idle connections aren't dropped by the server's
// ClientAliveInterval. A failed ping means the transport is unusable, so the
// client is closed outright — that teardown is what unblocks any Wait parked
// on a black-holed connection. A Managed wrapper then detects the death and
// redials.
func (c *Client) keepalive(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			if c.Ping(context.Background()) != nil {
				_ = c.Close()
				return
			}
		}
	}
}

// closedErr reports ErrClosed once Close has run, nil before.
func (c *Client) closedErr() error {
	select {
	case <-c.done:
		return ErrClosed
	default:
		return nil
	}
}
