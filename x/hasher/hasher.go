package hasher

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// randSource feeds Hash's salt entropy; swapped only by tests.
var randSource = rand.Reader //nolint:gochecknoglobals // test seam: rand failure is unreachable otherwise

var (
	// ErrMismatch reports a well-formed hash that does not match the
	// password. Callers must not tell clients which of user or password
	// was wrong — map it to the same response as an unknown user.
	ErrMismatch = errors.New("hasher: password mismatch")

	// ErrUnsupportedScheme reports an encoded hash in a scheme Verify
	// does not accept — not argon2id, and not enabled via Config.Legacy.
	ErrUnsupportedScheme = errors.New("hasher: unsupported scheme")

	// ErrMalformed reports an encoded hash that could not be parsed. A
	// corrupt stored hash and a wrong password are different incidents;
	// Verify never conflates them.
	ErrMalformed = errors.New("hasher: malformed encoded hash")
)

// Scheme names a password-hashing scheme Verify can accept.
type Scheme int

const (
	// Argon2id is the only scheme Hash produces.
	Argon2id Scheme = iota

	// Bcrypt is accepted by Verify only when listed in Config.Legacy,
	// for migrating existing credential stores. Every bcrypt hash
	// reports NeedsRehash.
	Bcrypt
)

// Config configures New. The zero value is a production-safe hasher at
// the RFC 9106 / OWASP recommended argon2id parameters.
type Config struct {
	Memory      uint32 // KiB per hash. Default DefaultMemory (19 MiB)
	Time        uint32 // passes over memory. Default DefaultTime
	Parallelism uint8  // lanes. Default DefaultParallelism
	SaltLength  uint32 // random salt bytes per hash. Default DefaultSaltLength
	KeyLength   uint32 // derived key bytes. Default DefaultKeyLength

	// Legacy lists schemes Verify additionally accepts. Hash never
	// produces them and NeedsRehash reports true for every legacy
	// hash, so enabling one migrates the store login by login.
	// Listing Argon2id has no effect — it is always accepted.
	Legacy []Scheme
}

// Hasher hashes and verifies passwords. Create one with New; it is
// safe for concurrent use.
type Hasher struct {
	memory      uint32
	time        uint32
	parallelism uint8
	saltLen     uint32
	keyLen      uint32
	legacy      []Scheme
}

// New builds a Hasher from cfg, filling zero fields with the defaults.
func New(cfg Config) *Hasher {
	h := &Hasher{
		memory:      cfg.Memory,
		time:        cfg.Time,
		parallelism: cfg.Parallelism,
		saltLen:     cfg.SaltLength,
		keyLen:      cfg.KeyLength,
		legacy:      append([]Scheme(nil), cfg.Legacy...),
	}
	if h.memory == 0 {
		h.memory = DefaultMemory
	}
	if h.time == 0 {
		h.time = DefaultTime
	}
	if h.parallelism == 0 {
		h.parallelism = DefaultParallelism
	}
	if h.saltLen == 0 {
		h.saltLen = DefaultSaltLength
	}
	if h.keyLen == 0 {
		h.keyLen = DefaultKeyLength
	}

	return h
}

// Hash derives an argon2id hash of password with a fresh random salt
// and returns it in the PHC string format:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>
func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.saltLen)
	if _, err := io.ReadFull(randSource, salt); err != nil {
		return "", fmt.Errorf("hasher: reading salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, h.time, h.memory, h.parallelism, h.keyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.memory, h.time, h.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether password matches encoded. Parameters come
// from the encoded string itself, so hashes minted under older
// parameters keep verifying. Returns nil on a match, ErrMismatch on a
// well-formed non-match, ErrUnsupportedScheme for a scheme not enabled,
// and an error wrapping ErrMalformed for anything unparseable.
func (h *Hasher) Verify(password, encoded string) error {
	switch {
	case strings.HasPrefix(encoded, "$argon2id$"):
		return verifyArgon2id(password, encoded)
	case strings.HasPrefix(encoded, "$2"):
		if !h.legacyEnabled(Bcrypt) {
			return ErrUnsupportedScheme
		}

		return verifyBcrypt(password, encoded)
	default:
		return ErrUnsupportedScheme
	}
}

// NeedsRehash reports whether encoded is below the Hasher's current
// policy: a legacy or unparseable hash, or argon2id with any parameter
// under the configured one. Call it after a successful Verify — the
// only moment the plaintext is in hand to re-hash.
func (h *Hasher) NeedsRehash(encoded string) bool {
	p, salt, key, err := parseArgon2id(encoded)
	if err != nil {
		return true
	}

	return p.memory < h.memory || p.time < h.time || p.parallelism < h.parallelism ||
		uint32(len(salt)) < h.saltLen || uint32(len(key)) < h.keyLen //nolint:gosec // parser caps salt/key lengths
}

func (h *Hasher) legacyEnabled(s Scheme) bool {
	return slices.Contains(h.legacy, s)
}

func verifyArgon2id(password, encoded string) error {
	p, salt, key, err := parseArgon2id(encoded)
	if err != nil {
		return err
	}

	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.parallelism, uint32(len(key))) //nolint:gosec // len(key) capped at maxKeyLength by the parser

	if subtle.ConstantTimeCompare(got, key) == 1 {
		return nil
	}

	return ErrMismatch
}

func verifyBcrypt(password, encoded string) error {
	err := bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return ErrMismatch
	default:
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}
}

type argon2Params struct {
	memory      uint32
	time        uint32
	parallelism uint8
}

// parseArgon2id splits a PHC argon2id string into parameters, salt, and
// key. Parameters are validated against the verification caps and the
// argon2 minimums, so a stored hash can neither panic the KDF nor
// demand unbounded resources.
func parseArgon2id(encoded string) (argon2Params, []byte, []byte, error) {
	var p argon2Params

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("%w: want $argon2id$v=..$m=..,t=..,p=..$salt$key", ErrMalformed)
	}

	if v, ok := parseParam(parts[2], "v="); !ok || v != argon2.Version {
		return p, nil, nil, fmt.Errorf("%w: unsupported version %q", ErrMalformed, parts[2])
	}

	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return p, nil, nil, fmt.Errorf("%w: parameters %q", ErrMalformed, parts[3])
	}
	m, ok1 := parseParam(fields[0], "m=")
	t, ok2 := parseParam(fields[1], "t=")
	par, ok3 := parseParam(fields[2], "p=")
	if !ok1 || !ok2 || !ok3 {
		return p, nil, nil, fmt.Errorf("%w: parameters %q", ErrMalformed, parts[3])
	}

	if par < 1 || par > maxParallelism || t < 1 || t > maxTime ||
		m < 8*par || m > maxMemory {
		return p, nil, nil, fmt.Errorf("%w: parameters %q out of bounds", ErrMalformed, parts[3])
	}
	p.memory, p.time = m, t
	p.parallelism = uint8(par)

	if len(parts[4]) > base64.RawStdEncoding.EncodedLen(maxSaltLength) ||
		len(parts[5]) > base64.RawStdEncoding.EncodedLen(maxKeyLength) {
		return p, nil, nil, fmt.Errorf("%w: salt or key length out of bounds", ErrMalformed)
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, fmt.Errorf("%w: salt: %w", ErrMalformed, err)
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, fmt.Errorf("%w: key: %w", ErrMalformed, err)
	}
	if len(salt) == 0 || len(salt) > maxSaltLength || len(key) == 0 || len(key) > maxKeyLength {
		return p, nil, nil, fmt.Errorf("%w: salt or key length out of bounds", ErrMalformed)
	}

	return p, salt, key, nil
}

// parseParam reads one strict "<prefix><decimal>" field. Canonical
// decimal only, per the PHC spec: no sign, no leading zeros.
func parseParam(s, prefix string) (uint32, bool) {
	v, ok := strings.CutPrefix(s, prefix)
	if !ok || len(v) > 1 && v[0] == '0' {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0, false
	}

	return uint32(n), true
}
