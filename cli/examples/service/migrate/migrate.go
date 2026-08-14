// Package migrate constructs the migration command tree; main registers
// it. The subcommands are placeholders until cli/migrationx lands — the
// shape (a nested tree built in its own package) is what this shows.
package migrate

import (
	"context"
	"fmt"

	"github.com/Wigata-Intech/w-tools/cli"
)

// Command builds the migrate subcommand tree: a nil-Run parent that only
// dispatches, with one child per verb.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "migrate",
		Short: "manage database migrations",
		Commands: []*cli.Command{
			{Name: "up", Short: "apply pending migrations", Run: placeholder("up")},
			{Name: "down", Short: "roll back one migration", Run: placeholder("down")},
			{Name: "status", Short: "show applied and pending migrations", Run: placeholder("status")},
		},
	}
}

func placeholder(verb string) func(context.Context, []string) error {
	return func(_ context.Context, _ []string) error {
		fmt.Printf("migrate %s: cli/migrationx is in development — this command wires to its engine when it lands\n", verb)
		return nil
	}
}
