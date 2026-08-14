# migrationx

> Your schema's history as ordered SQL files — applied in transactions, verified by checksum, locked against concurrent deploys.

**Status: v0, released** — ships inside the `cli` module (tagged `cli/vX.Y.Z`). Database drivers never appear here — the engine runs on your `*sql.DB` (sqlite and mysql dialects).

## TL;DR

```bash
go get github.com/Wigata-Intech/w-tools/cli
```

- Migrations are `<unix-timestamp>_<name>.up.sql` / `.down.sql` pairs, minted by `Create` (timestamps, not sequence numbers — two engineers can't collide on merge), shipped via `embed.FS` or a directory
- Fail-closed on every run: checksum tampering, orphaned history, and late-merged out-of-order migrations all abort; out-of-order applies only with explicit permission
- Concurrent deploys are serialized by a database-side lock, and an already-applied migration is skipped, never re-executed (`-- migrationx:no-transaction` migrations excepted — deploy those serially)
- On mysql, a no-transaction migration that fails partway leaves its history row `dirty`; `Up`/`Down` refuse to run again until an operator resolves it by hand, `Status` reports it inline via `Migration.Dirty` instead of failing
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

`Up`, `UpByOne`, `UpTo`, `Down`, `DownTo`, `Status`, `Version` — every mutating call verifies first (checksums, orphans, ordering) and each migration runs in its own transaction with the history write, commit or rollback as one unit. On mysql, DDL auto-commits mid-migration — single-DDL-statement migrations are the recommended practice there.

A `-- migrationx:no-transaction` migration carries that same exposure by design: its statements run outside any transaction, so a failure partway through can leave the schema changed with no history row to show it. The history table's `dirty` column tracks that case — `Up`/`Down` refuse to run again while any version is dirty, and `Status` surfaces it via `Migration.Dirty` instead of failing. Resolving it is a manual operator step: verify or repair the schema by hand, then either `UPDATE <table> SET dirty = 0 WHERE version = ...` or delete the row if the migration never really landed, before rerunning. `New` heals a history table created before this column existed, adding it automatically.

## What it costs

Fake-driver numbers — the engine's own work with the database costing nothing. Measured on a MacBook Pro — Apple M2 Pro (10 cores), 16 GB RAM, macOS 26.5.2, go1.26.6.

```bash
cd cli/migrationx && go test -run='^$' -bench=. -benchmem .
```

<details>
<summary>Raw output</summary>

```text
goos: darwin
goarch: arm64
pkg: github.com/Wigata-Intech/w-tools/cli/migrationx
cpu: Apple M2 Pro
BenchmarkParseScript-10    	   29318	     39679 ns/op	   42320 B/op	     509 allocs/op
BenchmarkLoad-10           	    6358	    185756 ns/op	  130201 B/op	    3443 allocs/op
BenchmarkUpTen-10          	   25497	     47126 ns/op	   38358 B/op	     557 allocs/op
ok  	github.com/Wigata-Intech/w-tools/cli/migrationx	4.721s
```

</details>

| Measure | Result |
| ------- | ------ |
| Scanning a 100-statement file (`BenchmarkParseScript`) | ~40 µs |
| Loading + checksumming 100 migration pairs (`BenchmarkLoad`) | ~186 µs |
| Applying ten migrations — transactions, probes, history (`BenchmarkUpTen`) | ~47 µs of engine overhead |

Migrations run once per deploy; real cost is your SQL, not the engine. Fuzzing covers the two own-parsers — the statement scanner (mirror-oracle invariants) and the filename parser (round-trip invariants):

<details>
<summary>Fuzzing — commands and raw output</summary>

```text
$ go test -run='^$' -fuzz=FuzzParseScript -fuzztime=10s .
$ go test -run='^$' -fuzz=FuzzParseFilename -fuzztime=10s .
```

</details>


The full story — module overview, precedence, entrypoints: the [cli README](../README.md).
