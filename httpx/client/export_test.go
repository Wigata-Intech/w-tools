package client

import (
	"io"
	"net/http"
)

// SetRandSource swaps the span-id entropy source for a test and returns
// a restore func. Exists because rand failure is unreachable otherwise.
func SetRandSource(r io.Reader) (restore func()) { //nolint:nonamedreturns // the name documents the contract: defer the restore
	prev := randSource
	randSource = r

	return func() { randSource = prev }
}

// MaxIdleConnsPerHost reads the per-host idle cap New set on the transport.
func (c *Client) MaxIdleConnsPerHost() int {
	return c.hc.Transport.(*http.Transport).MaxIdleConnsPerHost //nolint:forcetypeassert // New always sets an *http.Transport
}
