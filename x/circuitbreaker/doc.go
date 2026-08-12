// Package circuitbreaker is a three-state circuit breaker: closed while
// the upstream is healthy, open (failing fast) after the failure ratio
// trips, half-open to probe recovery. Allow/Record guards any
// operation, RoundTripper guards a native *http.Client, and
// httpx/client's Breaker hook is satisfied structurally.
package circuitbreaker
