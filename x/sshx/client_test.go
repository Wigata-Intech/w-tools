package sshx_test

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Wigata-Intech/w-tools/x/sshx"
)

// waitFor polls cond every millisecond until it holds, failing the test at
// timeout.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// testConfig authenticates against a NoClientAuth test server and trusts its
// host key; the password is never consulted.
func testConfig(s *testServer) sshx.Config {
	return sshx.Config{
		User:    "test",
		Auth:    []ssh.AuthMethod{ssh.Password("unused")},
		HostKey: s.hostKeyCallback(),
	}
}

// dialTestClient dials s and registers the client for cleanup.
func dialTestClient(t *testing.T, s *testServer) *sshx.Client {
	t.Helper()
	c, err := sshx.Dial(context.Background(), s.addr(), testConfig(s))
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestDial(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    sshx.Config
		expected error
	}{
		{
			name:     "nil host key",
			input:    sshx.Config{Auth: []ssh.AuthMethod{ssh.Password("x")}},
			expected: sshx.ErrHostKeyRequired,
		},
		{
			name:     "empty auth",
			input:    sshx.Config{HostKey: sshx.InsecureAcceptAny()},
			expected: sshx.ErrAuthRequired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := sshx.Dial(context.Background(), "127.0.0.1:0", tt.input)
			if !errors.Is(err, tt.expected) {
				t.Errorf("Dial() error = %v, want %v", err, tt.expected)
			}
		})
	}

	t.Run("public key auth", func(t *testing.T) {
		t.Parallel()
		signer := newTestSigner(t)
		s := newTestServer(t, serverOptions{authorizedKey: signer.PublicKey()})
		c, err := sshx.Dial(context.Background(), s.addr(), sshx.Config{
			User:    "test",
			Auth:    []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKey: s.hostKeyCallback(),
		})
		if err != nil {
			t.Fatalf("Dial() error = %v", err)
		}
		defer func() { _ = c.Close() }()
		if err := c.Ping(context.Background()); err != nil {
			t.Errorf("Ping() error = %v", err)
		}
	})

	t.Run("password auth", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{password: "s3cret"})
		c, err := sshx.Dial(context.Background(), s.addr(), sshx.Config{
			User:    "test",
			Auth:    []ssh.AuthMethod{ssh.Password("s3cret")},
			HostKey: s.hostKeyCallback(),
		})
		if err != nil {
			t.Fatalf("Dial() error = %v", err)
		}
		defer func() { _ = c.Close() }()
		if err := c.Ping(context.Background()); err != nil {
			t.Errorf("Ping() error = %v", err)
		}
	})

	t.Run("network stage on unreachable address", func(t *testing.T) {
		t.Parallel()
		ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		_, err = sshx.Dial(context.Background(), addr, sshx.Config{
			User:    "test",
			Auth:    []ssh.AuthMethod{ssh.Password("x")},
			HostKey: sshx.InsecureAcceptAny(),
		})
		var de *sshx.DialError
		if !errors.As(err, &de) {
			t.Fatalf("Dial() error = %v, want *DialError", err)
		}
		if de.Stage != sshx.StageNetwork {
			t.Errorf("Stage = %q, want %q", de.Stage, sshx.StageNetwork)
		}
		if de.Addr != addr {
			t.Errorf("Addr = %q, want %q", de.Addr, addr)
		}
	})

	t.Run("hostkey stage on mismatch", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		path := filepath.Join(t.TempDir(), "known_hosts")
		pinHost(t, path, s.addr(), newTestSigner(t).PublicKey())
		cb, err := sshx.KnownHosts(path)
		if err != nil {
			t.Fatalf("KnownHosts() error = %v", err)
		}
		_, err = sshx.Dial(context.Background(), s.addr(), sshx.Config{
			User:    "test",
			Auth:    []ssh.AuthMethod{ssh.Password("unused")},
			HostKey: cb,
		})
		var de *sshx.DialError
		if !errors.As(err, &de) {
			t.Fatalf("Dial() error = %v, want *DialError", err)
		}
		if de.Stage != sshx.StageHostKey {
			t.Errorf("Stage = %q, want %q", de.Stage, sshx.StageHostKey)
		}
		var mismatch *sshx.HostKeyMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("Dial() error = %v, want wrapped *HostKeyMismatchError", err)
		}
		if mismatch.Host != s.addr() {
			t.Errorf("mismatch.Host = %q, want %q", mismatch.Host, s.addr())
		}
	})

	t.Run("hostkey stage on unknown host", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		cb, err := sshx.TOFU(filepath.Join(t.TempDir(), "known_hosts"), nil)
		if err != nil {
			t.Fatalf("TOFU() error = %v", err)
		}
		_, err = sshx.Dial(context.Background(), s.addr(), sshx.Config{
			User:    "test",
			Auth:    []ssh.AuthMethod{ssh.Password("unused")},
			HostKey: cb,
		})
		var de *sshx.DialError
		if !errors.As(err, &de) {
			t.Fatalf("Dial() error = %v, want *DialError", err)
		}
		if de.Stage != sshx.StageHostKey {
			t.Errorf("Stage = %q, want %q", de.Stage, sshx.StageHostKey)
		}
		var unknown *sshx.UnknownHostKeyError
		if !errors.As(err, &unknown) {
			t.Fatalf("Dial() error = %v, want wrapped *UnknownHostKeyError", err)
		}
	})

	t.Run("handshake stage on auth rejection", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{authorizedKey: newTestSigner(t).PublicKey()})
		_, err := sshx.Dial(context.Background(), s.addr(), sshx.Config{
			User:    "test",
			Auth:    []ssh.AuthMethod{ssh.PublicKeys(newTestSigner(t))},
			HostKey: s.hostKeyCallback(),
		})
		var de *sshx.DialError
		if !errors.As(err, &de) {
			t.Fatalf("Dial() error = %v, want *DialError", err)
		}
		if de.Stage != sshx.StageHandshake {
			t.Errorf("Stage = %q, want %q", de.Stage, sshx.StageHandshake)
		}
	})

	t.Run("context cancellation mid dial", func(t *testing.T) {
		t.Parallel()
		ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer func() { _ = ln.Close() }()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errCh := make(chan error, 1)
		go func() {
			_, err := sshx.Dial(ctx, ln.Addr().String(), sshx.Config{
				User:    "test",
				Auth:    []ssh.AuthMethod{ssh.Password("x")},
				HostKey: sshx.InsecureAcceptAny(),
			})
			errCh <- err
		}()
		conn, err := ln.Accept() // the listener speaks no SSH: the handshake hangs
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		defer func() { _ = conn.Close() }()
		// Read the client's version banner first: it proves Dial is inside
		// the handshake (past DialContext, watchdog armed) before we cancel.
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatalf("read client banner: %v", err)
		}
		cancel()
		err = <-errCh
		var de *sshx.DialError
		if !errors.As(err, &de) {
			t.Fatalf("Dial() error = %v, want *DialError", err)
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Dial() error = %v, want wrapped context.Canceled", err)
		}
	})
}

func TestPing(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{})
	c := dialTestClient(t, s)

	t.Run("answered", func(t *testing.T) {
		t.Parallel()
		if err := c.Ping(context.Background()); err != nil {
			t.Errorf("Ping() error = %v", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := c.Ping(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("Ping() error = %v, want context.Canceled", err)
		}
	})
}

func TestKeepalive(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{})
	c, err := sshx.DialWithKeepalive(context.Background(), s.addr(), testConfig(s), 10*time.Millisecond)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() before kill error = %v", err)
	}

	s.killConns()
	waitFor(t, 5*time.Second, func() bool {
		return c.Ping(context.Background()) != nil
	})
	// The keepalive goroutine's exit is not directly observable; give its
	// 10ms ticker a few periods to see the dead transport and return.
	time.Sleep(50 * time.Millisecond)
}

func TestClientClose(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, serverOptions{})
	c, err := sshx.Dial(context.Background(), s.addr(), testConfig(s))
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	err1 := c.Close()
	err2 := c.Close()
	if err2 != err1 { //nolint:err113,errorlint // the Close contract is same-value, which errors.Is would weaken
		t.Errorf("second Close() = %v, want same value as first (%v)", err2, err1)
	}
}

var (
	errBoom          = errors.New("boom")
	errInnerSentinel = errors.New("inner")
)

func TestDialError_Error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    *sshx.DialError
		expected string
	}{
		{
			name: "stage and wrapped error rendered",
			input: &sshx.DialError{
				Stage: sshx.StageHostKey,
				Addr:  "203.0.113.7:22",
				Err:   errBoom,
			},
			expected: "sshx: dial 203.0.113.7:22: hostkey: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.input.Error(); got != tt.expected {
				t.Errorf("Error() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDialError_Unwrap(t *testing.T) {
	t.Parallel()
	errInner := errInnerSentinel
	tests := []struct {
		name     string
		input    *sshx.DialError
		expected error
	}{
		{
			name:     "exposes wrapped error",
			input:    &sshx.DialError{Stage: sshx.StageNetwork, Addr: "a:22", Err: errInner},
			expected: errInner,
		},
		{
			name:     "nil inner error",
			input:    &sshx.DialError{Stage: sshx.StageNetwork, Addr: "a:22"},
			expected: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.input.Unwrap(); got != tt.expected { //nolint:err113,errorlint // Unwrap's contract is identity, not equivalence
				t.Errorf("Unwrap() = %v, want %v", got, tt.expected)
			}
			if tt.expected != nil && !errors.Is(tt.input, tt.expected) {
				t.Errorf("errors.Is(%v, %v) = false, want true", tt.input, tt.expected)
			}
		})
	}

	t.Run("errors.As reaches the typed host-key error", func(t *testing.T) {
		t.Parallel()
		inner := &sshx.HostKeyMismatchError{Host: "a:22", KeyType: "ssh-ed25519", Fingerprint: "SHA256:x"}
		de := &sshx.DialError{Stage: sshx.StageHostKey, Addr: "a:22", Err: inner}
		var mismatch *sshx.HostKeyMismatchError
		if !errors.As(de, &mismatch) {
			t.Fatalf("errors.As(%v, *HostKeyMismatchError) = false, want true", de)
		}
		if mismatch != inner {
			t.Errorf("errors.As target = %v, want %v", mismatch, inner)
		}
	})
}
