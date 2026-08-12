package httpx

import "time"

// MethodQuery is the HTTP QUERY method (RFC 10008): safe, idempotent
// queries carried in the request body. Go rc-1.27 added net/http.MethodQuery
// with the identical value; when this module's floor reaches 1.27, this
// constant becomes an alias for it — no caller changes either way.
const MethodQuery = "QUERY"

// Defaults applied by New and Bind wherever config is zero-valued. Every
// default exists because Go's own zero (usually "no limit") is the wrong
// one for production; the numbers live here so they are documented API.
const (
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultReadTimeout       = 10 * time.Second
	DefaultWriteTimeout      = 30 * time.Second
	DefaultIdleTimeout       = 120 * time.Second
	DefaultShutdownGrace     = 15 * time.Second
	DefaultMaxHeaderBytes    = 1 << 20        // 1 MiB
	DefaultMaxBind           = int64(1) << 20 // 1 MiB

	// DefaultMaxBody caps body capture.
	DefaultMaxBody = 64 << 10 // 64 KiB
)
