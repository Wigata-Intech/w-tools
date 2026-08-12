// Package client is httpx's outbound side: an http.Client wrapper with
// production transport tuning, a mandatory timeout, a circuit-breaker
// hook, W3C traceparent propagation from the request context, and
// opt-in request/response logging that inherits the supplied logger's
// redaction.
package client
