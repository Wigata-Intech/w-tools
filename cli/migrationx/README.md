# migrationx

> Your schema's history as ordered SQL files — applied in transactions, verified by checksum, locked against concurrent deploys.

**Status:** 🚧 unreleased, ships inside the `cli` module (`cli/vX.Y.Z`). Database drivers never appear here — the engine runs on your `*sql.DB` (sqlite and mysql dialects).

## TL;DR

```bash
go get github.com/Wigata-Intech/w-tools/cli
```

- Migrations are `<unix-timestamp>_<name>.up.sql` / `.down.sql` pairs, minted by `Create` (timestamps, not sequence numbers — two engineers can't collide on merge), shipped via `embed.FS` or a directory
- Fail-closed on every run: checksum tampering, orphaned history, and late-merged out-of-order migrations all abort; out-of-order applies only with explicit permission
- Concurrent deploys are serialized by a database-side lock, and an already-applied migration is skipped, never re-executed (`-- migrationx:no-transaction` migrations excepted — deploy those serially)
- Rollbacks are audited: warn-level log lines with version, host, and operator — the database keeps no trace of a down by design
- Trigger/procedure bodies keep their semicolons inside `-- migrationx:statement begin` / `end`
- Zero third-party dependencies

## How to use with cli

One call wires the whole `migrate` tree into your command root:

```go
root.Commands = append(root.Commands, migrationx.Command(
	func(ctx context.Context, allowOutOfOrder bool) (*migrationx.Migrator, error) {
		db, err := sql.Open("your-driver", cfg.DSN) // your driver, your DSN
		if err != nil {
			return nil, err
		}
		sub, _ := fs.Sub(migrations, "migrations") // //go:embed migrations/*.sql
		return migrationx.New(db, sub, migrationx.Config{
			Dialect:         migrationx.DialectSQLite,
			AllowOutOfOrder: allowOutOfOrder,
		})
	},
))
```

```text
app migrate create add_users        # scaffold <unix>_add_users.{up,down}.sql
app migrate up                      # apply all pending
app migrate up -one | -to <version>
app migrate up -allow-out-of-order  # accept a late-merged branch
app migrate down                    # roll back the newest; -all | -to <version>
app migrate status                  # ✓ applied, pending, out-of-order
```

## How to use standalone

The engine is plain `database/sql` — no cli required:

```go
m, err := migrationx.New(db, migrationsFS, migrationx.Config{Dialect: migrationx.DialectMySQL})
if err != nil {
	return err // a stray file, bad name, or unparseable SQL aborts here, never at apply time
}
if err := m.Up(ctx); err != nil {
	return err
}
```

`Up`, `UpByOne`, `UpTo`, `Down`, `DownTo`, `Status`, `Version` — every mutating call verifies first (checksums, orphans, ordering) and each migration runs in its own transaction with the history write, commit or rollback as one unit. On mysql, DDL auto-commits mid-migration — single-DDL-statement migrations are the recommended practice there, and `Status` makes any partial state visible.

The full story — module overview, precedence, entrypoints: the [cli README](../README.md).
