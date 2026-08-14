// Package cron constructs the scheduler entrypoint; main registers it.
package cron

import (
	"context"
	"fmt"
	"time"

	"github.com/Wigata-Intech/w-tools/cli"
)

// Command builds the cron subcommand: a ticker loop that stops with the
// signal-wired context, the same shutdown path as the servers.
func Command(logLevel *string) *cli.Command {
	var every time.Duration
	return &cli.Command{
		Name:  "cron",
		Short: "run the scheduled jobs",
		Flags: func(fs *cli.FlagSet) {
			fs.DurationVar(&every, "every", time.Minute, "job interval")
		},
		Run: func(ctx context.Context, _ []string) error {
			fmt.Printf("cron: every %s (log-level=%s) — Ctrl-C to stop\n", every, *logLevel)
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case now := <-t.C:
					fmt.Printf("cron: tick %s\n", now.Format(time.TimeOnly))
				}
			}
		},
	}
}
