package sshx_test

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Wigata-Intech/w-tools/x/sshx"
)

var errRefusedSentinel = errors.New("operator said no")

// pinHost appends a known_hosts line binding host to key.
func pinHost(t *testing.T, path, host string, key ssh.PublicKey) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //#nosec G304 -- path is a test-owned temp file
	if err != nil {
		t.Fatalf("open known_hosts: %v", err)
	}
	if _, err := f.WriteString(knownhosts.Line([]string{knownhosts.Normalize(host)}, key) + "\n"); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close known_hosts: %v", err)
	}
}

// knownHostsLines returns the non-empty lines of the file at path.
func knownHostsLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) //#nosec G304 -- path is a test-owned temp file
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	var lines []string
	for l := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// wantLines asserts the known_hosts file has exactly n non-empty lines.
func wantLines(t *testing.T, path string, n int) []string {
	t.Helper()
	lines := knownHostsLines(t, path)
	if len(lines) != n {
		t.Fatalf("known_hosts lines = %d, want %d", len(lines), n)
	}
	return lines
}

// mustKnownHosts builds a strict verifier, failing the test on error.
func mustKnownHosts(t *testing.T, path string) ssh.HostKeyCallback {
	t.Helper()
	cb, err := sshx.KnownHosts(path)
	if err != nil {
		t.Fatalf("KnownHosts() error = %v", err)
	}
	return cb
}

// mustTOFU builds a trust-on-first-use verifier, failing the test on error.
func mustTOFU(t *testing.T, path string, confirm sshx.ConfirmHostFunc) ssh.HostKeyCallback {
	t.Helper()
	cb, err := sshx.TOFU(path, confirm)
	if err != nil {
		t.Fatalf("TOFU() error = %v", err)
	}
	return cb
}

// wantUnknown asserts err classifies as an unknown-host-key error.
func wantUnknown(t *testing.T, err error) *sshx.UnknownHostKeyError {
	t.Helper()
	var unknown *sshx.UnknownHostKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("error = %v, want *UnknownHostKeyError", err)
	}
	return unknown
}

// wantMismatch asserts err classifies as a host-key-mismatch error.
func wantMismatch(t *testing.T, err error) *sshx.HostKeyMismatchError {
	t.Helper()
	var mismatch *sshx.HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want *HostKeyMismatchError", err)
	}
	return mismatch
}

// wantConfirms asserts the confirm callback ran exactly n times.
func wantConfirms(t *testing.T, confirms *atomic.Int32, n int32) {
	t.Helper()
	if got := confirms.Load(); got != n {
		t.Errorf("confirm calls = %d, want %d", got, n)
	}
}

func TestKnownHosts(t *testing.T) {
	t.Parallel()
	const host = "127.0.0.1:2222"
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}

	t.Run("pinned key verifies", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		key := newTestSigner(t).PublicKey()
		pinHost(t, path, host, key)
		cb := mustKnownHosts(t, path)
		if err := cb(host, remote, key); err != nil {
			t.Errorf("callback error = %v, want nil", err)
		}
	})

	t.Run("creates file and directory with safe modes", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "sub")
		path := filepath.Join(dir, "known_hosts")
		mustKnownHosts(t, path)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat file: %v", err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
		}
		di, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if !di.IsDir() || di.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %v (dir=%t), want 0700 directory", di.Mode().Perm(), di.IsDir())
		}
	})

	t.Run("unknown host", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		key := newTestSigner(t).PublicKey()
		cb := mustKnownHosts(t, path)
		unknown := wantUnknown(t, cb(host, remote, key))
		if unknown.Host != host {
			t.Errorf("Host = %q, want %q", unknown.Host, host)
		}
		if unknown.KeyType != key.Type() {
			t.Errorf("KeyType = %q, want %q", unknown.KeyType, key.Type())
		}
		if unknown.Fingerprint != ssh.FingerprintSHA256(key) {
			t.Errorf("Fingerprint = %q, want %q", unknown.Fingerprint, ssh.FingerprintSHA256(key))
		}
	})

	t.Run("changed key", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		pinHost(t, path, host, newTestSigner(t).PublicKey())
		key := newTestSigner(t).PublicKey()
		cb := mustKnownHosts(t, path)
		mismatch := wantMismatch(t, cb(host, remote, key))
		if mismatch.Host != host {
			t.Errorf("Host = %q, want %q", mismatch.Host, host)
		}
		if mismatch.Fingerprint != ssh.FingerprintSHA256(key) {
			t.Errorf("Fingerprint = %q, want %q", mismatch.Fingerprint, ssh.FingerprintSHA256(key))
		}
	})

	t.Run("unrecognized verifier error passes through", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		cb := mustKnownHosts(t, path)
		// A hostname without a port is rejected by x/crypto's verifier with an
		// untyped error, which must pass through unclassified.
		err := cb("127.0.0.1", remote, newTestSigner(t).PublicKey())
		if err == nil {
			t.Fatal("callback error = nil, want non-nil")
		}
		var unknown *sshx.UnknownHostKeyError
		var mismatch *sshx.HostKeyMismatchError
		if errors.As(err, &unknown) || errors.As(err, &mismatch) {
			t.Errorf("callback error = %v, want it unclassified", err)
		}
	})

	t.Run("unwritable directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o500); err != nil { //#nosec G302 -- the test needs an unwritable directory
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //#nosec G302 -- restore so TempDir cleanup can remove it
		cb, err := sshx.KnownHosts(filepath.Join(dir, "known_hosts"))
		if cb != nil {
			t.Error("callback = non-nil, want nil")
		}
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("KnownHosts() error = %v, want fs.ErrPermission", err)
		}
	})

	t.Run("parent path is a regular file", func(t *testing.T) {
		t.Parallel()
		base := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(base, nil, 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		cb, err := sshx.KnownHosts(filepath.Join(base, "known_hosts"))
		if cb != nil {
			t.Error("callback = non-nil, want nil")
		}
		var pe *fs.PathError
		if !errors.As(err, &pe) {
			t.Fatalf("KnownHosts() error = %v, want *fs.PathError", err)
		}
	})

	t.Run("path is a directory", func(t *testing.T) {
		t.Parallel()
		cb, err := sshx.KnownHosts(t.TempDir())
		if cb != nil {
			t.Error("callback = non-nil, want nil")
		}
		var pe *fs.PathError
		if !errors.As(err, &pe) {
			t.Fatalf("KnownHosts() error = %v, want *fs.PathError", err)
		}
	})

	t.Run("malformed file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		if err := os.WriteFile(path, []byte("malformed\n"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		cb, err := sshx.KnownHosts(path)
		if cb != nil {
			t.Error("callback = non-nil, want nil")
		}
		if err == nil {
			t.Fatal("KnownHosts() error = nil, want parse error")
		}
	})
}

func TestTOFU(t *testing.T) {
	t.Parallel()
	const host = "127.0.0.1:2222"
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}

	t.Run("confirm true pins across dials", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		path := filepath.Join(t.TempDir(), "known_hosts")
		var confirms atomic.Int32
		cb := mustTOFU(t, path, func(h sshx.HostInfo) (bool, error) {
			confirms.Add(1)
			if h.Host != s.addr() {
				t.Errorf("HostInfo.Host = %q, want %q", h.Host, s.addr())
			}
			if h.Key == nil || h.KeyType != h.Key.Type() || h.Fingerprint != ssh.FingerprintSHA256(h.Key) {
				t.Errorf("HostInfo inconsistent: %+v", h)
			}
			return true, nil
		})
		cfg := sshx.Config{User: "test", Auth: []ssh.AuthMethod{ssh.Password("unused")}, HostKey: cb}

		c, err := sshx.Dial(context.Background(), s.addr(), cfg)
		if err != nil {
			t.Fatalf("first Dial() error = %v", err)
		}
		_ = c.Close()
		wantConfirms(t, &confirms, 1)
		wantLines(t, path, 1)

		c, err = sshx.Dial(context.Background(), s.addr(), cfg)
		if err != nil {
			t.Fatalf("second Dial() error = %v", err)
		}
		_ = c.Close()
		wantConfirms(t, &confirms, 1)
		wantLines(t, path, 1)
	})

	t.Run("concurrent first contact confirms once", func(t *testing.T) {
		t.Parallel()
		s := newTestServer(t, serverOptions{})
		path := filepath.Join(t.TempDir(), "known_hosts")
		var confirms atomic.Int32
		cb := mustTOFU(t, path, func(sshx.HostInfo) (bool, error) {
			confirms.Add(1)
			return true, nil
		})
		cfg := sshx.Config{User: "test", Auth: []ssh.AuthMethod{ssh.Password("unused")}, HostKey: cb}

		const n = 8
		errCh := make(chan error, n)
		for range n {
			go func() {
				c, err := sshx.Dial(context.Background(), s.addr(), cfg)
				if err == nil {
					_ = c.Close()
				}
				errCh <- err
			}()
		}
		for range n {
			if err := <-errCh; err != nil {
				t.Errorf("parallel Dial() error = %v", err)
			}
		}
		wantConfirms(t, &confirms, 1)
		wantLines(t, path, 1)
	})

	t.Run("confirm false refuses", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		cb := mustTOFU(t, path, func(sshx.HostInfo) (bool, error) { return false, nil })
		_ = wantUnknown(t, cb(host, remote, newTestSigner(t).PublicKey()))
		wantLines(t, path, 0)
	})

	t.Run("confirm error propagates", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		errRefused := errRefusedSentinel
		cb := mustTOFU(t, path, func(sshx.HostInfo) (bool, error) { return false, errRefused })
		if err := cb(host, remote, newTestSigner(t).PublicKey()); !errors.Is(err, errRefused) {
			t.Errorf("callback error = %v, want %v", err, errRefused)
		}
	})

	t.Run("nil confirm is strict", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		cb := mustTOFU(t, path, nil)
		_ = wantUnknown(t, cb(host, remote, newTestSigner(t).PublicKey()))
	})

	t.Run("changed key never confirms", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		pinHost(t, path, host, newTestSigner(t).PublicKey())
		var confirms atomic.Int32
		cb := mustTOFU(t, path, func(sshx.HostInfo) (bool, error) {
			confirms.Add(1)
			return true, nil
		})
		_ = wantMismatch(t, cb(host, remote, newTestSigner(t).PublicKey()))
		wantConfirms(t, &confirms, 0)
	})

	t.Run("records remote when it differs", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		key := newTestSigner(t).PublicKey()
		var confirms atomic.Int32
		cb := mustTOFU(t, path, func(sshx.HostInfo) (bool, error) {
			confirms.Add(1)
			return true, nil
		})
		if err := cb("example.test:22", remote, key); err != nil {
			t.Fatalf("callback error = %v, want nil", err)
		}
		lines := wantLines(t, path, 1)
		if !strings.Contains(lines[0], "example.test") || !strings.Contains(lines[0], "[127.0.0.1]:2222") {
			t.Errorf("pinned line = %q, want both dialed name and remote address", lines[0])
		}
		// The pinned host is now trusted without a second confirmation.
		if err := cb("example.test:22", remote, key); err != nil {
			t.Errorf("re-verify error = %v, want nil", err)
		}
		wantConfirms(t, &confirms, 1)
	})

	t.Run("file corrupted after open", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		var confirms atomic.Int32
		cb := mustTOFU(t, path, func(sshx.HostInfo) (bool, error) {
			confirms.Add(1)
			return true, nil
		})
		if err := os.WriteFile(path, []byte("malformed\n"), 0o600); err != nil {
			t.Fatalf("corrupt file: %v", err)
		}
		if err := cb(host, remote, newTestSigner(t).PublicKey()); err == nil {
			t.Error("callback error = nil, want re-open failure")
		}
		wantConfirms(t, &confirms, 0)
	})

	t.Run("key pinned differently while waiting", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		var confirms atomic.Int32
		cb := mustTOFU(t, path, func(sshx.HostInfo) (bool, error) {
			confirms.Add(1)
			return true, nil
		})
		// The file gains a different key for this host after the verifier
		// opened it: the re-verify must classify a mismatch, not confirm.
		pinHost(t, path, host, newTestSigner(t).PublicKey())
		_ = wantMismatch(t, cb(host, remote, newTestSigner(t).PublicKey()))
		wantConfirms(t, &confirms, 0)
	})

	t.Run("append failure surfaces", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "known_hosts")
		cb := mustTOFU(t, path, func(sshx.HostInfo) (bool, error) { return true, nil })
		if err := os.Chmod(path, 0o400); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		if err := cb(host, remote, newTestSigner(t).PublicKey()); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("callback error = %v, want fs.ErrPermission", err)
		}
	})

	t.Run("parent path is a regular file", func(t *testing.T) {
		t.Parallel()
		base := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(base, nil, 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		cb, err := sshx.TOFU(filepath.Join(base, "known_hosts"), nil)
		if cb != nil {
			t.Error("callback = non-nil, want nil")
		}
		var pe *fs.PathError
		if !errors.As(err, &pe) {
			t.Fatalf("TOFU() error = %v, want *fs.PathError", err)
		}
	})
}

func TestInsecureAcceptAny(t *testing.T) {
	t.Parallel()
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
	cb := sshx.InsecureAcceptAny()
	if err := cb("anything:22", remote, newTestSigner(t).PublicKey()); err != nil {
		t.Errorf("callback error = %v, want nil", err)
	}
}

func TestUnknownHostKeyError_Error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    *sshx.UnknownHostKeyError
		expected string
	}{
		{
			name: "all fields rendered",
			input: &sshx.UnknownHostKeyError{
				Host:        "203.0.113.7:22",
				KeyType:     "ssh-ed25519",
				Fingerprint: "SHA256:abcdef",
			},
			expected: "sshx: unknown host key for 203.0.113.7:22 (ssh-ed25519 SHA256:abcdef)",
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

func TestHostKeyMismatchError_Error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    *sshx.HostKeyMismatchError
		expected string
	}{
		{
			name: "all fields rendered",
			input: &sshx.HostKeyMismatchError{
				Host:        "203.0.113.7:22",
				KeyType:     "ssh-ed25519",
				Fingerprint: "SHA256:abcdef",
			},
			expected: "sshx: host key mismatch for 203.0.113.7:22 (ssh-ed25519 SHA256:abcdef)",
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
