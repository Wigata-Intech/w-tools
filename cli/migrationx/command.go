package migrationx

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Wigata-Intech/w-tools/cli"
)

var (
	errCreateUsage  = errors.New("migrationx: create takes exactly one argument: the migration name")
	errQuantityToUp = errors.New("migrationx: --one and --to are mutually exclusive")
	errQuantityToDn = errors.New("migrationx: --all and --to are mutually exclusive")
)

// Command builds the migrate command tree — create, up, down, status —
// for registration in a cli root. open constructs the Migrator after
// flags resolve: the DSN, dialect, and filesystem live in the caller's
// own configuration; allowOutOfOrder carries the --allow-out-of-order
// flag into Config.
//
// The grammar: the bare verb does the safe default (up applies
// everything, down rolls back one), --one/--all flip the quantity,
// --to targets a version, and combining a quantity flag with --to is an
// error.
func Command(open func(ctx context.Context, allowOutOfOrder bool) (*Migrator, error)) *cli.Command {
	var (
		one, all, allowOOO bool
		upTo, downTo       int64
		dir                string
	)

	run := func(fn func(ctx context.Context, m *Migrator) error) func(context.Context, []string) error {
		return func(ctx context.Context, _ []string) error {
			m, err := open(ctx, allowOOO)
			if err != nil {
				return err
			}
			return fn(ctx, m)
		}
	}

	return &cli.Command{
		Name:  "migrate",
		Short: "manage database migrations",
		Commands: []*cli.Command{
			{
				Name:  "create",
				Short: "scaffold a timestamped migration pair",
				Flags: func(fs *cli.FlagSet) {
					fs.StringVar(&dir, "dir", "migrations", "migrations directory")
				},
				Run: func(_ context.Context, args []string) error {
					if len(args) != 1 {
						return errCreateUsage
					}
					up, down, err := Create(dir, args[0])
					if err != nil {
						return err
					}
					_, _ = fmt.Fprintln(os.Stdout, up)
					_, _ = fmt.Fprintln(os.Stdout, down)
					return nil
				},
			},
			{
				Name:  "up",
				Short: "apply pending migrations",
				Flags: func(fs *cli.FlagSet) {
					fs.BoolVar(&one, "one", false, "apply exactly one migration")
					fs.Int64Var(&upTo, "to", 0, "apply up to and including this version")
					fs.BoolVar(&allowOOO, "allow-out-of-order", false, "apply late-merged migrations older than the newest applied")
				},
				Run: run(func(ctx context.Context, m *Migrator) error {
					switch {
					case one && upTo > 0:
						return errQuantityToUp
					case one:
						return m.UpByOne(ctx)
					case upTo > 0:
						return m.UpTo(ctx, upTo)
					default:
						return m.Up(ctx)
					}
				}),
			},
			{
				Name:  "down",
				Short: "roll back the newest migration",
				Flags: func(fs *cli.FlagSet) {
					fs.BoolVar(&all, "all", false, "roll back everything")
					fs.Int64Var(&downTo, "to", 0, "roll back to (and keep) this version")
				},
				Run: run(func(ctx context.Context, m *Migrator) error {
					switch {
					case all && downTo > 0:
						return errQuantityToDn
					case all:
						return m.DownTo(ctx, 0)
					case downTo > 0:
						return m.DownTo(ctx, downTo)
					default:
						return m.Down(ctx)
					}
				}),
			},
			{
				Name:  "status",
				Short: "show applied and pending migrations",
				Run: run(func(ctx context.Context, m *Migrator) error {
					rows, err := m.Status(ctx)
					if err != nil {
						return err
					}
					for _, r := range rows {
						mark, note := " ", ""
						switch {
						case r.Orphaned:
							mark = "✓"
							note = "orphaned: file missing"
						case r.Applied:
							mark = "✓"
							if !r.AppliedAt.IsZero() {
								note = r.AppliedAt.UTC().Format("2006-01-02 15:04:05")
							}
						case r.OutOfOrder:
							note = "out of order"
						}
						_, _ = fmt.Fprintf(os.Stdout, "%s %d_%s %s\n", mark, r.Version, r.Name, note)
					}
					return nil
				}),
			},
		},
	}
}
