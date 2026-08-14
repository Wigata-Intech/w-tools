// Package migrationx is a SQL migration engine on the standard library:
// ordered SQL files, a history table with checksums, transactions where
// the database supports them, and a migration lock — with database
// drivers staying entirely on the consumer's side.
//
// Migrations are pairs of files named <unix-timestamp>_<name>.up.sql and
// .down.sql, minted by Create and shipped via embed.FS or a directory.
// The engine runs on the consumer's *sql.DB against sqlite or mysql.
//
// Every mutating run verifies before it acts, and ambiguity aborts:
// an applied migration whose file changed (checksum mismatch) or
// disappeared (orphan) stops the run; pending migrations older than the
// newest applied one — the late-merged branch — are refused unless
// Config.AllowOutOfOrder acknowledges them.
//
// Concurrent runners are serialized by a database-side lock, and a
// migration another runner already applied is skipped, never
// re-executed. The one exception is a no-transaction migration: its
// statements run outside any transaction, so the concurrent-runner
// guarantee does not cover it — deploy those serially.
//
// On mysql, a no-transaction migration's statements can fail partway
// through with no transaction to roll back, leaving the schema changed
// but the history row absent or stale. The history table's dirty column
// tracks this: the row is marked dirty before the statements run and
// cleared on success, so a version a failed run left dirty stays visible
// afterward. Up and Down refuse to run again while any version is dirty;
// Status and Version report it (Migration.Dirty) without failing — they
// take no lock, so a legitimate run in progress must not look like a
// failure to either. Resolving a dirty version is an operator step this
// package cannot automate: inspect and repair whatever the failed
// statements left half-done, then clear the row — UPDATE the history
// table setting dirty to 0, or delete the row if the migration never
// really applied — before rerunning.
//
// A migration engine bug is a data incident, not a bug report: when in
// doubt, this package stops.
package migrationx
