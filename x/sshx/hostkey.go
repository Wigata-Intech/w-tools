package sshx

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// UnknownHostKeyError reports a host absent from known_hosts that was not
// (or could not be) confirmed.
type UnknownHostKeyError struct {
	Host        string
	KeyType     string
	Fingerprint string
}

// Error implements error.
func (e *UnknownHostKeyError) Error() string {
	return fmt.Sprintf("sshx: unknown host key for %s (%s %s)", e.Host, e.KeyType, e.Fingerprint)
}

// HostKeyMismatchError reports a host whose presented key differs from the
// pinned one — a possible man-in-the-middle. It is never confirmable.
type HostKeyMismatchError struct {
	Host        string
	KeyType     string
	Fingerprint string
}

// Error implements error.
func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("sshx: host key mismatch for %s (%s %s)", e.Host, e.KeyType, e.Fingerprint)
}

// HostInfo describes a host presenting a key, as handed to a ConfirmHostFunc.
type HostInfo struct {
	Host        string // address as dialed, host:port
	Remote      net.Addr
	KeyType     string // e.g. "ssh-ed25519"
	Fingerprint string // SHA-256, OpenSSH format
	Key         ssh.PublicKey
}

// ConfirmHostFunc decides whether a previously unseen host is trusted.
// Returning true pins the key; false or an error refuses the connection.
type ConfirmHostFunc func(h HostInfo) (bool, error)

// KnownHosts returns a strict host-key verifier pinned to the OpenSSH-format
// file at path: unknown hosts fail with UnknownHostKeyError, changed keys with
// HostKeyMismatchError. The file is created empty (0600, directory 0700) if
// absent.
func KnownHosts(path string) (ssh.HostKeyCallback, error) {
	verify, err := openKnownHosts(path)
	if err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		return classifyKnownHosts(verify(hostname, remote, key), hostname, key)
	}, nil
}

// TOFU returns a trust-on-first-use verifier over the file at path: known
// hosts are checked strictly, and an unknown host is handed to confirm, whose
// consent pins the key. A nil confirm degrades to strict. A changed key is
// never confirmable.
//
// Concurrent first-contact dials through one TOFU value collapse to a single
// confirmation and a single pinned line. That guarantee is per returned
// callback: use one TOFU value per known_hosts file, not one per dial.
func TOFU(path string, confirm ConfirmHostFunc) (ssh.HostKeyCallback, error) {
	verify, err := openKnownHosts(path)
	if err != nil {
		return nil, err
	}
	// mu serializes the confirm-and-append path so concurrent first-contact
	// dials to one host produce a single confirmation and a single pin.
	var mu sync.Mutex
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := classifyKnownHosts(verify(hostname, remote, key), hostname, key)
		var unknown *UnknownHostKeyError
		if !errors.As(err, &unknown) {
			return err
		}
		if confirm == nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		// Re-verify against the file as it is now: a parallel dial may have
		// pinned this host while we waited on the lock.
		reverify, rerr := knownhosts.New(path)
		if rerr != nil {
			return rerr
		}
		rerr = classifyKnownHosts(reverify(hostname, remote, key), hostname, key)
		if !errors.As(rerr, &unknown) {
			return rerr
		}
		ok, cerr := confirm(HostInfo{
			Host:        hostname,
			Remote:      remote,
			KeyType:     key.Type(),
			Fingerprint: ssh.FingerprintSHA256(key),
			Key:         key,
		})
		if cerr != nil {
			return cerr
		}
		if !ok {
			return err
		}
		return appendKnownHost(path, hostname, remote, key)
	}, nil
}

// InsecureAcceptAny returns a verifier that accepts every host key without
// checking anything. It exists for lab use; production traffic has no
// business anywhere near it.
func InsecureAcceptAny() ssh.HostKeyCallback {
	return ssh.InsecureIgnoreHostKey() //#nosec G106 -- the entire point of this constructor; callers opt in by name
}

// openKnownHosts ensures the file exists with safe permissions and returns
// x/crypto's verifier over it.
func openKnownHosts(path string) (ssh.HostKeyCallback, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE, 0o600) //#nosec G304 -- path is chosen by the consumer, not remote input
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return knownhosts.New(path)
}

// classifyKnownHosts maps x/crypto's knownhosts errors onto this package's
// typed errors; nil and unrecognized errors pass through.
func classifyKnownHosts(err error, hostname string, key ssh.PublicKey) error {
	if err == nil {
		return nil
	}
	var ke *knownhosts.KeyError
	if !errors.As(err, &ke) {
		return err
	}
	if len(ke.Want) == 0 {
		return &UnknownHostKeyError{Host: hostname, KeyType: key.Type(), Fingerprint: ssh.FingerprintSHA256(key)}
	}
	return &HostKeyMismatchError{Host: hostname, KeyType: key.Type(), Fingerprint: ssh.FingerprintSHA256(key)}
}

// appendKnownHost pins a confirmed key, recording the remote address too when
// it differs from the dialed name.
func appendKnownHost(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	addrs := []string{knownhosts.Normalize(hostname)}
	if remote != nil {
		if ra := knownhosts.Normalize(remote.String()); ra != addrs[0] {
			addrs = append(addrs, ra)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600) //#nosec G304 -- path is chosen by the consumer, not remote input
	if err != nil {
		return err
	}
	_, werr := f.WriteString(knownhosts.Line(addrs, key) + "\n")
	return errors.Join(werr, f.Close())
}
