package client

import "io"

// SetRandSource swaps the span-id entropy source for a test and returns
// a restore func. Exists because rand failure is unreachable otherwise.
func SetRandSource(r io.Reader) (restore func()) { //nolint:nonamedreturns // the name documents the contract: defer the restore
	prev := randSource
	randSource = r

	return func() { randSource = prev }
}
