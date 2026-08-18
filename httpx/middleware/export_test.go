package middleware

import (
	"io"
	"time"
)

// SetRandSource swaps the ID entropy source for a test and returns a
// restore func. Exists because rand failure is unreachable otherwise.
func SetRandSource(r io.Reader) (restore func()) { //nolint:nonamedreturns // the name documents the contract: defer the restore
	prev := randSource
	randSource = r

	return func() { randSource = prev }
}

// SetTimeNow swaps the package clock (rate limiter, idempotency
// MemoryStore) for a test and returns a restore func. Driven clocks
// instead of sleeps.
func SetTimeNow(now func() time.Time) (restore func()) { //nolint:nonamedreturns // the name documents the contract: defer the restore
	prev := timeNow
	timeNow = now

	return func() { timeNow = prev }
}
