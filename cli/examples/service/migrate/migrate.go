// Package migrate wires cli/migrationx into the service: one call
// composes the whole migrate create|up|down|status tree.
package migrate

import (
	"context"
	"errors"

	"github.com/Wigata-Intech/w-tools/cli"
	"github.com/Wigata-Intech/w-tools/cli/migrationx"
)

var errNoDriver = errors.New(
	"this dependency-free example stops at the database seam: in a real service, " +
		"import your sql driver, open *sql.DB from your configuration, and return " +
		"migrationx.New(db, migrationsFS, migrationx.Config{...}) here — " +
		"`migrate create` works without a database")

// Command builds the migrate tree. The open callback runs after flags
// resolve; it owns the DSN, the driver, and the embedded migrations.
func Command() *cli.Command {
	return migrationx.Command(func(_ context.Context, _ bool) (*migrationx.Migrator, error) {
		return nil, errNoDriver
	})
}
