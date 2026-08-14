package keys

import "io"

// GenerateWithRand is Generate with an injectable entropy source so the
// generation-failure branches are deterministically testable.
func GenerateWithRand(rng io.Reader, alg Algorithm, bits int, comment string) (*Pair, error) {
	return generate(alg, bits, comment, rng)
}

// EncodePair exposes the encoding helper so its error branches are reachable
// with key types Generate itself would never produce.
func EncodePair(priv any, comment string) (*Pair, error) {
	return encodePair(priv, comment)
}
