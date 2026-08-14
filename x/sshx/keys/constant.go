package keys

// RSA size policy: Generate refuses below MinRSABits outright (NIST SP 800-57
// puts 2048 at the 112-bit security floor) and uses DefaultRSABits when the
// caller passes zero.
const (
	MinRSABits     = 2048
	DefaultRSABits = 3072
)
