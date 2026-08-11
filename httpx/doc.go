// Package httpx is a thin layer over net/http: a server with production
// timeouts and graceful shutdown, route groups over ServeMux, middleware
// chaining, JSON helpers with RFC 9457 errors and domain-error mapping,
// body binding, and HTML rendering for BFF services.
//
// Handlers stay plain http.HandlerFunc and patterns are ServeMux patterns —
// anything written for net/http works here unchanged, and anything written
// for httpx works under bare net/http.
package httpx
