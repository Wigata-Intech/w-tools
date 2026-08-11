// Package middleware is httpx's standard middleware set.
//
// Canonical order, outermost first:
//
//	RealIP → RequestID → Trace → Logger → Recover → handler
//
// RealIP first so everything downstream sees the real client; Recover
// inside Logger so a panic is logged as the 500 it became, with its
// latency.
package middleware
