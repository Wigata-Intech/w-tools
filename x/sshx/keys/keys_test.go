package keys_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Wigata-Intech/w-tools/x/sshx/keys"
)

var errPromptSentinel = errors.New("prompt refused")

// parseExpect describes the outcome of a parse or load call: all fields zero
// means success; errIs is an errors.Is target; errMsg is an exact message for
// errors without a sentinel; passphraseMissing additionally demands errors.As
// to *ssh.PassphraseMissingError.
type parseExpect struct {
	errIs             error
	errMsg            string
	passphraseMissing bool
}

// check asserts a (signer, err) pair against the expectation.
func (e parseExpect) check(t *testing.T, signer ssh.Signer, err error) {
	t.Helper()
	if e.errIs == nil && e.errMsg == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if signer == nil {
			t.Fatal("signer is nil")
		}
		return
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if e.errIs != nil && !errors.Is(err, e.errIs) {
		t.Fatalf("errors.Is(err, %v) is false, err = %v", e.errIs, err)
	}
	if e.errMsg != "" && err.Error() != e.errMsg {
		t.Fatalf("err = %q, want %q", err.Error(), e.errMsg)
	}
	if e.passphraseMissing {
		var missing *ssh.PassphraseMissingError
		if !errors.As(err, &missing) {
			t.Fatalf("errors.As(err, **ssh.PassphraseMissingError) is false, err = %v", err)
		}
	}
}

// encryptedKeyPEM returns a fresh Ed25519 private key encrypted with
// passphrase, as OpenSSH PEM.
func encryptedKeyPEM(t *testing.T, passphrase []byte) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", passphrase)
	if err != nil {
		t.Fatalf("ssh.MarshalPrivateKeyWithPassphrase: %v", err)
	}
	return pem.EncodeToMemory(block)
}

// unencryptedKeyPEM returns a fresh unencrypted Ed25519 private key as
// OpenSSH PEM.
func unencryptedKeyPEM(t *testing.T) []byte {
	t.Helper()
	pair, err := keys.Generate(keys.Ed25519, 0, "")
	if err != nil {
		t.Fatalf("keys.Generate: %v", err)
	}
	return pair.PrivatePEM
}

func TestParsePrivate(t *testing.T) {
	plain := unencryptedKeyPEM(t)
	encrypted := encryptedKeyPEM(t, []byte("open sesame"))

	tests := []struct {
		name     string
		input    []byte
		expected parseExpect
	}{
		{
			name:     "unencrypted key parses",
			input:    plain,
			expected: parseExpect{},
		},
		{
			name:     "encrypted key requires passphrase",
			input:    encrypted,
			expected: parseExpect{errIs: keys.ErrPassphraseRequired, passphraseMissing: true},
		},
		{
			name:     "garbage bytes",
			input:    []byte("not a key"),
			expected: parseExpect{errMsg: "ssh: no key found"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer, err := keys.ParsePrivate(tt.input)
			tt.expected.check(t, signer, err)
		})
	}
}

func TestParsePrivateWithPassphrase(t *testing.T) {
	passphrase := []byte("open sesame")
	encrypted := encryptedKeyPEM(t, passphrase)
	plain := unencryptedKeyPEM(t)

	type parseInput struct {
		pemBytes   []byte
		passphrase []byte
	}
	tests := []struct {
		name     string
		input    parseInput
		expected parseExpect
	}{
		{
			name:     "correct passphrase parses",
			input:    parseInput{pemBytes: encrypted, passphrase: passphrase},
			expected: parseExpect{},
		},
		{
			name:     "wrong passphrase",
			input:    parseInput{pemBytes: encrypted, passphrase: []byte("wrong")},
			expected: parseExpect{errIs: x509.IncorrectPasswordError},
		},
		{
			name:     "unencrypted key",
			input:    parseInput{pemBytes: plain, passphrase: passphrase},
			expected: parseExpect{errMsg: "ssh: key is not password protected"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer, err := keys.ParsePrivateWithPassphrase(tt.input.pemBytes, tt.input.passphrase)
			tt.expected.check(t, signer, err)
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	passphrase := []byte("open sesame")
	plainPath := filepath.Join(dir, "id_plain")
	if err := os.WriteFile(plainPath, unencryptedKeyPEM(t), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	encPath := filepath.Join(dir, "id_encrypted")
	if err := os.WriteFile(encPath, encryptedKeyPEM(t, passphrase), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	errPrompt := errPromptSentinel

	type loadInput struct {
		path   string
		prompt keys.PassphraseFunc
	}
	tests := []struct {
		name     string
		input    loadInput
		expected parseExpect
	}{
		{
			name:     "unencrypted file with nil prompt",
			input:    loadInput{path: plainPath},
			expected: parseExpect{},
		},
		{
			name: "encrypted file with prompt",
			input: loadInput{
				path:   encPath,
				prompt: func(string) ([]byte, error) { return passphrase, nil },
			},
			expected: parseExpect{},
		},
		{
			name:     "missing file",
			input:    loadInput{path: filepath.Join(dir, "absent")},
			expected: parseExpect{errIs: os.ErrNotExist},
		},
		{
			name:     "encrypted file with nil prompt",
			input:    loadInput{path: encPath},
			expected: parseExpect{errIs: keys.ErrPassphraseRequired},
		},
		{
			name: "encrypted file with failing prompt",
			input: loadInput{
				path:   encPath,
				prompt: func(string) ([]byte, error) { return nil, errPrompt },
			},
			expected: parseExpect{errIs: errPrompt},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer, err := keys.Load(tt.input.path, tt.input.prompt)
			tt.expected.check(t, signer, err)
		})
	}

	t.Run("prompt receives the key path", func(t *testing.T) {
		var got string
		calls := 0
		signer, err := keys.Load(encPath, func(path string) ([]byte, error) {
			got = path
			calls++
			return passphrase, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if signer == nil {
			t.Fatal("signer is nil")
		}
		if calls != 1 {
			t.Fatalf("prompt called %d times, want 1", calls)
		}
		if got != encPath {
			t.Fatalf("prompt received path %q, want %q", got, encPath)
		}
	})

	t.Run("prompt not called for unencrypted key", func(t *testing.T) {
		calls := 0
		signer, err := keys.Load(plainPath, func(string) ([]byte, error) {
			calls++
			return nil, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if signer == nil {
			t.Fatal("signer is nil")
		}
		if calls != 0 {
			t.Fatalf("prompt called %d times, want 0", calls)
		}
	})
}

// FuzzParsePrivate holds ParsePrivate's robustness invariant: arbitrary input
// never panics, and a nil error always carries a usable signer.
func FuzzParsePrivate(f *testing.F) {
	seed, err := keys.Generate(keys.Ed25519, 0, "seed")
	if err != nil {
		f.Fatalf("seed key: %v", err)
	}
	f.Add(seed.PrivatePEM)
	f.Add([]byte("not a key"))
	f.Add([]byte("-----BEGIN OPENSSH PRIVATE KEY-----\ngarbage\n-----END OPENSSH PRIVATE KEY-----\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		signer, err := keys.ParsePrivate(data)
		if err == nil && signer == nil {
			t.Fatal("ParsePrivate returned nil error and nil signer")
		}
	})
}
