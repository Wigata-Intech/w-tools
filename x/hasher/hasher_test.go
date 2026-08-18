package hasher_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Wigata-Intech/w-tools/x/hasher"
)

const password = "correct horse battery staple"

// Pinned fixtures: hashes minted by this module (and, for bcrypt, by
// x/crypto) that must keep verifying forever — stored credentials
// outlive every parameter change.
const (
	fixtureFast    = "$argon2id$v=19$m=64,t=1,p=1$fkG4MaN0WVYQn5bfUhth3g$PcyP1G2CIT24V0AN2IXt4L4uixrumP/9yosw6jcgF8k"
	fixtureDefault = "$argon2id$v=19$m=19456,t=2,p=1$TlpMNe3KKePtbVZmF0N1Bg$8vxUIjenUtd9EJZ1aLxROez9cXE5o0Gprm+pxCLDn28"
	fixtureBcrypt  = "$2a$04$MNh1npW4Iaqa1qcLgewgweE6Xqu6gGBvhak75G0PLZPd6eUN.Rjou"
)

// fast returns a hasher at the cheapest legal parameters, so the suite
// stays quick; parameter strength is pinned by the defaults cases.
func fast() *hasher.Hasher {
	return hasher.New(hasher.Config{Memory: 64, Time: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
}

func TestNew(t *testing.T) {
	t.Run("zero config fills every default", func(t *testing.T) {
		encoded, err := hasher.New(hasher.Config{}).Hash(password)
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}

		parts := strings.Split(encoded, "$")
		if len(parts) != 6 {
			t.Fatalf("PHC sections = %d, want 6: %q", len(parts), encoded)
		}
		salt, err := base64.RawStdEncoding.DecodeString(parts[4])
		if err != nil {
			t.Fatalf("decode salt: %v", err)
		}
		key, err := base64.RawStdEncoding.DecodeString(parts[5])
		if err != nil {
			t.Fatalf("decode key: %v", err)
		}
		if len(salt) != hasher.DefaultSaltLength {
			t.Fatalf("salt length = %d, want DefaultSaltLength %d", len(salt), hasher.DefaultSaltLength)
		}
		if len(key) != hasher.DefaultKeyLength {
			t.Fatalf("key length = %d, want DefaultKeyLength %d", len(key), hasher.DefaultKeyLength)
		}
	})
}

func TestHash(t *testing.T) {
	t.Run("round-trips through Verify", func(t *testing.T) {
		h := fast()
		encoded, err := h.Hash(password)
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		if err := h.Verify(password, encoded); err != nil {
			t.Fatalf("Verify(own hash): %v", err)
		}
	})

	t.Run("defaults emit the RFC 9106 profile", func(t *testing.T) {
		encoded, err := hasher.New(hasher.Config{}).Hash(password)
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		if !strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$") {
			t.Fatalf("Hash() = %q, want the default-parameter PHC prefix", encoded)
		}
	})

	t.Run("entropy failure surfaces as an error", func(t *testing.T) {
		restore := hasher.SetRandSource(strings.NewReader("short"))
		defer restore()

		if _, err := fast().Hash(password); err == nil {
			t.Fatal("Hash with exhausted entropy returned nil error")
		}
	})

	t.Run("salts are fresh per hash", func(t *testing.T) {
		h := fast()
		a, err := h.Hash(password)
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		b, err := h.Hash(password)
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		if a == b {
			t.Fatal("two hashes of one password are identical — salt is not fresh")
		}
	})
}

func TestVerify(t *testing.T) {
	tests := []struct {
		name     string
		cfg      hasher.Config
		password string
		encoded  string
		expected error // nil means match
	}{
		{
			name:     "pinned argon2id fixture verifies",
			password: password,
			encoded:  fixtureFast,
			expected: nil,
		},
		{
			name:     "pinned default-parameter fixture verifies",
			password: password,
			encoded:  fixtureDefault,
			expected: nil,
		},
		{
			name:     "wrong password is a mismatch",
			password: "wrong horse",
			encoded:  fixtureFast,
			expected: hasher.ErrMismatch,
		},
		{
			name:     "tampered key is a mismatch",
			password: password,
			encoded:  strings.Replace(fixtureFast, "IT24", "IT34", 1),
			expected: hasher.ErrMismatch,
		},
		{
			name:     "bcrypt without Legacy is unsupported",
			password: password,
			encoded:  fixtureBcrypt,
			expected: hasher.ErrUnsupportedScheme,
		},
		{
			name:     "bcrypt with Legacy verifies",
			cfg:      hasher.Config{Legacy: []hasher.Scheme{hasher.Bcrypt}},
			password: password,
			encoded:  fixtureBcrypt,
			expected: nil,
		},
		{
			name:     "bcrypt with Legacy and wrong password is a mismatch",
			cfg:      hasher.Config{Legacy: []hasher.Scheme{hasher.Bcrypt}},
			password: "wrong horse",
			encoded:  fixtureBcrypt,
			expected: hasher.ErrMismatch,
		},
		{
			name:     "malformed bcrypt with Legacy is malformed not mismatch",
			cfg:      hasher.Config{Legacy: []hasher.Scheme{hasher.Bcrypt}},
			password: password,
			encoded:  "$2a$xx$not-a-real-bcrypt-hash",
			expected: hasher.ErrMalformed,
		},
		{
			name:     "unknown scheme is unsupported",
			password: password,
			encoded:  "$scrypt$N=16384$whatever",
			expected: hasher.ErrUnsupportedScheme,
		},
		{
			name:     "plaintext column is unsupported",
			password: password,
			encoded:  password,
			expected: hasher.ErrUnsupportedScheme,
		},
		{
			name:     "missing sections are malformed",
			password: password,
			encoded:  "$argon2id$v=19$m=64,t=1,p=1$saltonly",
			expected: hasher.ErrMalformed,
		},
		{
			name:     "unsupported version is malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "v=19", "v=18", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "trailing garbage in version is malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "v=19", "v=19x", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "signed version is malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "v=19", "v=+19", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "leading zeros are non-canonical and malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "m=64", "m=064", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "zero time is malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "t=1", "t=0", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "memory below the argon2 minimum is malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "m=64", "m=4", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "memory beyond the verification cap is malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "m=64", "m=4194304", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "parallelism beyond the cap is malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "p=1", "p=65", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "parameter fields out of order are malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "m=64,t=1,p=1", "t=1,m=64,p=1", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "missing parameter field is malformed",
			password: password,
			encoded:  strings.Replace(fixtureFast, "m=64,t=1,p=1", "m=64,t=1", 1),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "oversized encoded key is refused before decoding",
			password: password,
			encoded:  "$argon2id$v=19$m=64,t=1,p=1$fkG4MaN0WVYQn5bfUhth3g$" + strings.Repeat("A", 4096),
			expected: hasher.ErrMalformed,
		},
		{
			name:     "invalid salt base64 is malformed",
			password: password,
			encoded:  "$argon2id$v=19$m=64,t=1,p=1$!!!$PcyP1G2CIT24V0AN2IXt4L4uixrumP/9yosw6jcgF8k",
			expected: hasher.ErrMalformed,
		},
		{
			name:     "invalid key base64 is malformed",
			password: password,
			encoded:  "$argon2id$v=19$m=64,t=1,p=1$fkG4MaN0WVYQn5bfUhth3g$!!!",
			expected: hasher.ErrMalformed,
		},
		{
			name:     "empty key is malformed",
			password: password,
			encoded:  "$argon2id$v=19$m=64,t=1,p=1$fkG4MaN0WVYQn5bfUhth3g$",
			expected: hasher.ErrMalformed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := hasher.New(tt.cfg).Verify(tt.password, tt.encoded)
			if tt.expected == nil {
				if err != nil {
					t.Fatalf("Verify() = %v, want nil", err)
				}

				return
			}
			if !errors.Is(err, tt.expected) {
				t.Fatalf("Verify() = %v, want %v", err, tt.expected)
			}
		})
	}

	t.Run("old-parameter hashes verify under a stronger config", func(t *testing.T) {
		if err := hasher.New(hasher.Config{}).Verify(password, fixtureFast); err != nil {
			t.Fatalf("Verify(old params, strong config) = %v, want nil", err)
		}
	})

	t.Run("concurrent hash and verify are race-free", func(t *testing.T) {
		h := fast()
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				encoded, err := h.Hash(password)
				if err != nil {
					t.Errorf("Hash: %v", err)

					return
				}
				if err := h.Verify(password, encoded); err != nil {
					t.Errorf("Verify: %v", err)
				}
			})
		}
		wg.Wait()
	})
}

func TestNeedsRehash(t *testing.T) {
	strong, err := hasher.New(hasher.Config{Memory: 32768, Time: 3, Parallelism: 2}).Hash(password)
	if err != nil {
		t.Fatalf("Hash(strong): %v", err)
	}

	tests := []struct {
		name     string
		encoded  string
		expected bool
	}{
		{
			name:     "hash at current parameters stands",
			encoded:  fixtureDefault,
			expected: false,
		},
		{
			name:     "hash above current parameters stands",
			encoded:  strong,
			expected: false,
		},
		{
			name:     "hash below current parameters needs rehash",
			encoded:  fixtureFast,
			expected: true,
		},
		{
			name:     "bcrypt always needs rehash",
			encoded:  fixtureBcrypt,
			expected: true,
		},
		{
			name:     "unparseable value needs rehash",
			encoded:  "not-a-hash",
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasher.New(hasher.Config{}).NeedsRehash(tt.encoded); got != tt.expected {
				t.Fatalf("NeedsRehash() = %v, want %v", got, tt.expected)
			}
		})
	}
}
