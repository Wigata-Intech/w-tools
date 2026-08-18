package hasher

// Defaults follow RFC 9106's second recommended option (the
// memory-constrained profile) as adopted by the OWASP password storage
// guidance: argon2id, 19 MiB, 2 passes, 1 lane.
const (
	DefaultMemory      = 19456 // KiB
	DefaultTime        = 2
	DefaultParallelism = 1
	DefaultSaltLength  = 16
	DefaultKeyLength   = 32
)

// Verification caps bound what a stored hash may demand of this
// process. Verify parses parameters from the encoded string, so a
// poisoned credential column could otherwise request gigabytes of
// memory per login attempt; anything beyond these is ErrMalformed.
const (
	maxMemory      = 1 << 21 // KiB — 2 GiB
	maxTime        = 512
	maxParallelism = 64
	maxSaltLength  = 256
	maxKeyLength   = 512
)
