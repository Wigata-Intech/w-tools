// Package keys parses, loads, and generates SSH private keys for use with
// sshx. Interactive concerns stay out: a passphrase is fetched through a
// caller-supplied callback, only when a key actually needs one. Writing keys
// to disk is the consumer's job.
package keys

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
)

// ErrPassphraseRequired reports an encrypted key parsed without a passphrase.
var ErrPassphraseRequired = errors.New("sshx/keys: passphrase required")

// PassphraseFunc supplies the passphrase for the encrypted key at path.
type PassphraseFunc func(path string) ([]byte, error)

// ParsePrivate parses a PEM-encoded private key. An encrypted key fails with
// ErrPassphraseRequired — use ParsePrivateWithPassphrase or Load.
//
//nolint:ireturn // ssh.Signer is x/crypto's contract type for private keys
func ParsePrivate(pemBytes []byte) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("%w: %w", ErrPassphraseRequired, err)
		}
		return nil, err
	}
	return signer, nil
}

// ParsePrivateWithPassphrase parses a passphrase-protected PEM private key.
//
//nolint:ireturn // ssh.Signer is x/crypto's contract type for private keys
func ParsePrivateWithPassphrase(pemBytes, passphrase []byte) (ssh.Signer, error) {
	return ssh.ParsePrivateKeyWithPassphrase(pemBytes, passphrase)
}

// Load reads and parses the private key at path. prompt is invoked only when
// the key is encrypted; a nil prompt fails such keys with
// ErrPassphraseRequired.
//
//nolint:ireturn // ssh.Signer is x/crypto's contract type for private keys
func Load(path string, prompt PassphraseFunc) (ssh.Signer, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- path is chosen by the consumer, not remote input
	if err != nil {
		return nil, err
	}
	signer, err := ParsePrivate(data)
	if err == nil || !errors.Is(err, ErrPassphraseRequired) || prompt == nil {
		return signer, err
	}
	passphrase, err := prompt(path)
	if err != nil {
		return nil, err
	}
	return ParsePrivateWithPassphrase(data, passphrase)
}
