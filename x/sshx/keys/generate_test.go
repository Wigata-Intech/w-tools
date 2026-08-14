package keys_test

import (
	"bytes"
	"crypto/dsa" //nolint:staticcheck // deprecated on purpose: the one signer-accepted key type the OpenSSH format refuses
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"golang.org/x/crypto/ssh"

	"github.com/Wigata-Intech/w-tools/x/sshx/keys"
)

var errEntropy = errors.New("entropy source failed")

func TestGenerate(t *testing.T) {
	type genInput struct {
		alg     keys.Algorithm
		bits    int
		comment string
	}
	type genExpect struct {
		err     error // sentinel via errors.Is; nil means success
		comment string
		rsaBits int // 0 means not an RSA case
	}
	tests := []struct {
		name     string
		input    genInput
		expected genExpect
	}{
		{
			name:     "ed25519 with comment",
			input:    genInput{alg: keys.Ed25519, comment: "comment"},
			expected: genExpect{comment: "comment"},
		},
		{
			name:     "ed25519 ignores bits and empty comment",
			input:    genInput{alg: keys.Ed25519, bits: 4096},
			expected: genExpect{},
		},
		{
			name:     "rsa defaults to 3072 bits",
			input:    genInput{alg: keys.RSA, comment: "c"},
			expected: genExpect{comment: "c", rsaBits: 3072},
		},
		{
			name:     "rsa 2048 bits",
			input:    genInput{alg: keys.RSA, bits: 2048},
			expected: genExpect{rsaBits: 2048},
		},
		{
			name:     "rsa below minimum refused",
			input:    genInput{alg: keys.RSA, bits: 1024},
			expected: genExpect{err: keys.ErrRSATooWeak},
		},
		{
			name:     "unsupported algorithm",
			input:    genInput{alg: "dsa"},
			expected: genExpect{err: keys.ErrUnsupportedAlgorithm},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pair, err := keys.Generate(tt.input.alg, tt.input.bits, tt.input.comment)
			if tt.expected.err != nil {
				if !errors.Is(err, tt.expected.err) {
					t.Fatalf("err = %v, want errors.Is %v", err, tt.expected.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			block, rest := pem.Decode(pair.PrivatePEM)
			if block == nil {
				t.Fatal("PrivatePEM is not PEM")
			}
			if len(rest) != 0 {
				t.Fatalf("PrivatePEM has %d trailing bytes", len(rest))
			}
			if block.Type != "OPENSSH PRIVATE KEY" {
				t.Fatalf("PEM block type = %q, want %q", block.Type, "OPENSSH PRIVATE KEY")
			}

			signer, err := keys.ParsePrivate(pair.PrivatePEM)
			if err != nil {
				t.Fatalf("ParsePrivate(PrivatePEM): %v", err)
			}
			msg := []byte("sshx keys test message")
			sig, err := signer.Sign(rand.Reader, msg)
			if err != nil {
				t.Fatalf("signer.Sign: %v", err)
			}

			pub, comment, _, rest, err := ssh.ParseAuthorizedKey(pair.PublicAuthorized)
			if err != nil {
				t.Fatalf("ssh.ParseAuthorizedKey(PublicAuthorized): %v", err)
			}
			if len(rest) != 0 {
				t.Fatalf("PublicAuthorized has %d trailing bytes", len(rest))
			}
			if comment != tt.expected.comment {
				t.Fatalf("authorized_keys comment = %q, want %q", comment, tt.expected.comment)
			}
			if err := pub.Verify(msg, sig); err != nil {
				t.Fatalf("public key does not verify private key signature: %v", err)
			}

			if got := ssh.FingerprintSHA256(pub); got != pair.Fingerprint {
				t.Fatalf("Fingerprint = %q, want %q", pair.Fingerprint, got)
			}

			if tt.expected.rsaBits != 0 {
				raw, err := ssh.ParseRawPrivateKey(pair.PrivatePEM)
				if err != nil {
					t.Fatalf("ssh.ParseRawPrivateKey: %v", err)
				}
				rsaKey, ok := raw.(*rsa.PrivateKey)
				if !ok {
					t.Fatalf("raw private key is %T, want *rsa.PrivateKey", raw)
				}
				if got := rsaKey.N.BitLen(); got != tt.expected.rsaBits {
					t.Fatalf("rsa key size = %d bits, want %d", got, tt.expected.rsaBits)
				}
			}
		})
	}
}

func TestGenerateEntropyFailure(t *testing.T) {
	t.Run("ed25519", func(t *testing.T) {
		if _, err := keys.GenerateWithRand(iotest.ErrReader(errEntropy), keys.Ed25519, 0, ""); !errors.Is(err, errEntropy) {
			t.Errorf("GenerateWithRand() error = %v, want errEntropy", err)
		}
	})

	t.Run("rsa", func(t *testing.T) {
		if _, err := keys.GenerateWithRand(iotest.ErrReader(errEntropy), keys.RSA, 2048, ""); err == nil {
			t.Error("GenerateWithRand() error = nil, want entropy failure")
		}
	})
}

func TestEncodePair(t *testing.T) {
	t.Run("signer refuses a non-key", func(t *testing.T) {
		if _, err := keys.EncodePair("not a key", ""); err == nil {
			t.Error("EncodePair() error = nil, want signer failure")
		}
	})

	t.Run("marshal refuses what the signer accepts", func(t *testing.T) {
		if _, err := keys.EncodePair(dsaKey(t), ""); err == nil {
			t.Error("EncodePair() error = nil, want marshal failure")
		}
	})
}

// dsaKey generates the one stdlib key type ssh signers accept but the OpenSSH
// private-key format cannot encode, reaching encodePair's marshal branch.
func dsaKey(t *testing.T) *dsa.PrivateKey {
	t.Helper()
	var params dsa.Parameters
	if err := dsa.GenerateParameters(&params, rand.Reader, dsa.L1024N160); err != nil {
		t.Fatalf("dsa parameters: %v", err)
	}
	var key dsa.PrivateKey
	key.Parameters = params
	if err := dsa.GenerateKey(&key, rand.Reader); err != nil {
		t.Fatalf("dsa key: %v", err)
	}
	return &key
}

// FuzzGenerateComment holds Generate's comment invariant: any accepted comment
// yields exactly one parseable authorized_keys line carrying it back; anything
// with a newline is refused before key material exists.
func FuzzGenerateComment(f *testing.F) {
	f.Add("web-1")
	f.Add("")
	f.Add("spaces and unicode \u00e9")
	f.Add("evil\nssh-ed25519 AAAA forged")
	f.Fuzz(func(t *testing.T, comment string) {
		pair, err := keys.Generate(keys.Ed25519, 0, comment)
		if strings.ContainsAny(comment, "\r\n") || comment != strings.TrimSpace(comment) {
			if !errors.Is(err, keys.ErrInvalidComment) {
				t.Fatalf("Generate(%q) error = %v, want ErrInvalidComment", comment, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("Generate(%q) error = %v", comment, err)
		}
		if n := bytes.Count(pair.PublicAuthorized, []byte("\n")); n != 1 {
			t.Fatalf("PublicAuthorized has %d newlines, want 1: %q", n, pair.PublicAuthorized)
		}
		_, got, _, rest, err := ssh.ParseAuthorizedKey(pair.PublicAuthorized)
		if err != nil {
			t.Fatalf("ParseAuthorizedKey(%q): %v", pair.PublicAuthorized, err)
		}
		if len(rest) != 0 {
			t.Fatalf("authorized_keys line has %d trailing bytes", len(rest))
		}
		if got != comment {
			t.Fatalf("comment round-trip = %q, want %q", got, comment)
		}
	})
}
