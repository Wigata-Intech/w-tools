package circuitbreaker

import "time"

// Defaults applied by New wherever config is zero-valued. They trip
// reluctantly on purpose: false opens hurt more than a few failed calls.
const (
	DefaultFailureRatio   = 0.5
	DefaultMinRequests    = 10
	DefaultWindow         = 10 * time.Second
	DefaultWindowBuckets  = 10
	DefaultOpenFor        = 30 * time.Second
	DefaultHalfOpenProbes = 1
)
