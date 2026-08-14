package migrationx

import "time"

// Dialect selects the database-specific behavior: history-table DDL, the
// migration lock, and transaction control. Config.Dialect is required.
type Dialect int

// The supported dialects. The zero value is invalid — choosing a
// database is an explicit decision.
const (
	DialectSQLite Dialect = iota + 1
	DialectMySQL
)

const (
	// DefaultTable is the history table name when Config.Table is empty.
	DefaultTable = "migration_histories"

	// DefaultLockTimeout bounds the wait for the migration lock when
	// Config.LockTimeout is zero.
	DefaultLockTimeout = 30 * time.Second
)

// timeNow returns the current time; tests replace it via SetTimeNow.
//
//nolint:gochecknoglobals // the clock seam
var timeNow = time.Now
