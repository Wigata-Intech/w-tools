package circuitbreaker

import "time"

// SetNow swaps b's clock for a test and returns a restore func. Driven
// clocks instead of sleeps.
func SetNow(b *Breaker, now func() time.Time) (restore func()) { //nolint:nonamedreturns // the name documents the contract: defer the restore
	prev := b.now
	b.now = now

	return func() { b.now = prev }
}
