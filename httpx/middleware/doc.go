// Package middleware is httpx's standard middleware set.
//
// Canonical order, outermost first:
//
//	RealIP → RequestID → Trace → Logger → Recover → CORS → RateLimit → Idempotency → handler
//
// RealIP first so everything downstream sees the real client; Recover
// inside Logger so a panic is logged as the 500 it became, with its
// latency; the gates (CORS, RateLimit, Idempotency) innermost so their
// short-circuit responses are logged like any other and their code runs
// under Recover. In this order CORS preflights are deliberately
// unmetered — they short-circuit before RateLimit; place RateLimit
// before CORS to meter them too. Idempotency sits inside RateLimit so a
// rate-limited request never consumes an idempotency claim.
package middleware
