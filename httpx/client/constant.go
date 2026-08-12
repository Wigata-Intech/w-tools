package client

import "time"

// Defaults applied by New wherever config is zero-valued. "No timeout"
// is not expressible: that footgun stays in the stdlib.
const (
	DefaultTimeout             = 30 * time.Second
	DefaultMaxIdleConnsPerHost = 100 // stdlib default: 2
	DefaultIdleConnTimeout     = 90 * time.Second
)
