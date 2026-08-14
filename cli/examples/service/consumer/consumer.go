// Package consumer constructs the queue-consumer entrypoint; main
// registers it.
package consumer

import (
	"context"
	"fmt"
	"time"

	"github.com/Wigata-Intech/w-tools/cli"
)

// Command builds the consumer subcommand. The time-based loop stands in
// for a real queue subscription; the shutdown path is the part that
// matters — the context ends the loop mid-stream, no message abandoned.
func Command(logLevel *string) *cli.Command {
	return &cli.Command{
		Name:  "consumer",
		Short: "consume the message queue",
		Run: func(ctx context.Context, _ []string) error {
			fmt.Printf("consumer: waiting for messages (log-level=%s) — Ctrl-C to stop\n", *logLevel)
			for n := 1; ; n++ {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(time.Second):
					fmt.Printf("consumer: handled message #%d\n", n)
				}
			}
		},
	}
}
