// The breaker proof: httpx/client calls a dead upstream until
// x/circuitbreaker trips, then fails fast without touching the network.
// One-shot, like the redaction proof — it stages the failure itself and
// prints the moment the circuit opens.
//
//	go run ./breaker
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx/client"
	"github.com/Wigata-Intech/w-tools/x/circuitbreaker"
)

// The design's boundary, checked at compile time: the breaker satisfies
// httpx/client's hook structurally — neither package imports the other.
var _ client.Breaker = (*circuitbreaker.Breaker)(nil)

func main() {
	br := circuitbreaker.New(circuitbreaker.Config{
		MinRequests:  3,
		FailureRatio: 0.5,
		OnStateChange: func(from, to circuitbreaker.State) {
			fmt.Printf("circuit: %s -> %s\n", from, to)
		},
	})

	c := client.New(client.Config{
		Timeout: 500 * time.Millisecond,
		Breaker: br,
	})

	deadURL := "http://127.0.0.1:1" // nothing listens on port 1

	for i := 1; i <= 5; i++ {
		start := time.Now()
		resp, err := c.Get(context.Background(), deadURL)
		if resp != nil {
			_ = resp.Body.Close()
		}

		switch {
		case errors.Is(err, client.ErrCircuitOpen):
			fmt.Printf("call %d: CIRCUIT OPEN — failed in %s, network untouched\n", i, time.Since(start).Round(time.Microsecond))
		case err != nil:
			fmt.Printf("call %d: upstream error after %s\n", i, time.Since(start).Round(time.Microsecond))
		}
	}
}
