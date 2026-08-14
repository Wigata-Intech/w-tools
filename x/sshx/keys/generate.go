package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ErrRSATooWeak reports an RSA bit size below the 2048 floor.
var ErrRSATooWeak = errors.New("sshx/keys: rsa keys below 2048 bits are refused")

// ErrUnsupportedAlgorithm reports an Algorithm Generate doesn't know.
var ErrUnsupportedAlgorithm = errors.New("sshx/keys: unsupported algorithm")

// ErrInvalidComment reports a comment the authorized_keys format cannot carry
// faithfully: a newline would forge extra entries, and leading or trailing
// whitespace does not survive a parse round-trip.
var ErrInvalidComment = errors.New("sshx/keys: comment must not contain newlines or edge whitespace")

// Algorithm selects a key type for Generate.
type Algorithm string

const (
	// Ed25519 is the recommended default (RFC 8709).
	Ed25519 Algorithm = "ed25519"
	// RSA is for peers that can't do Ed25519. Generate refuses fewer than
	// 2048 bits (NIST SP 800-57) and defaults to 3072.
	RSA Algorithm = "rsa"
)

// Pair is a freshly generated key pair, encoded and ready to store: the
// private key as OpenSSH PEM, the public key as an authorized_keys line.
type Pair struct {
	PrivatePEM       []byte
	PublicAuthorized []byte
	Fingerprint      string // SHA-256, OpenSSH format
}

// Generate creates a new key pair. bits applies to RSA only (0 means
// [DefaultRSABits], below [MinRSABits] is refused) and is ignored for Ed25519. comment is embedded in
// the private key and appended to the authorized_keys line; it may be empty.
func Generate(alg Algorithm, bits int, comment string) (*Pair, error) {
	return generate(alg, bits, comment, rand.Reader)
}

// generate is Generate with an injectable entropy source, the seam tests use
// to exercise generation failure.
func generate(alg Algorithm, bits int, comment string, rng io.Reader) (*Pair, error) {
	if strings.ContainsAny(comment, "\r\n") || comment != strings.TrimSpace(comment) {
		return nil, ErrInvalidComment
	}
	switch alg {
	case Ed25519:
		_, priv, err := ed25519.GenerateKey(rng)
		if err != nil {
			return nil, err
		}
		return encodePair(priv, comment)
	case RSA:
		if bits == 0 {
			bits = DefaultRSABits
		}
		if bits < MinRSABits {
			return nil, fmt.Errorf("%w: got %d", ErrRSATooWeak, bits)
		}
		priv, err := rsa.GenerateKey(rng, bits)
		if err != nil {
			return nil, err
		}
		return encodePair(priv, comment)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
}

// encodePair encodes a private key into the formats Pair promises.
func encodePair(priv any, comment string) (*Pair, error) {
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, err
	}
	pub := signer.PublicKey()
	authorized := ssh.MarshalAuthorizedKey(pub)
	if comment != "" {
		// MarshalAuthorizedKey ends with a newline; splice the comment in.
		authorized = append(authorized[:len(authorized)-1], []byte(" "+comment+"\n")...)
	}
	return &Pair{
		PrivatePEM:       pem.EncodeToMemory(block),
		PublicAuthorized: authorized,
		Fingerprint:      ssh.FingerprintSHA256(pub),
	}, nil
}
