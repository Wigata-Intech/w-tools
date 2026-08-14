package migrationx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"database/sql/driver"
)

var errLockTimeout = errors.New("migrationx: could not acquire the migration lock")

// dialectOps is the seam between the engine and one database's way of
// doing DDL, locking, and transactions. Two implementations, both
// internal: a bring-your-own-dialect surface is speculative until asked
// for.
type dialectOps interface {
	createTable(ctx context.Context, conn *sql.Conn, table string) error
	lock(ctx context.Context, conn *sql.Conn, table string, timeout time.Duration) error
	unlock(ctx context.Context, conn *sql.Conn, table string) error
	begin(ctx context.Context, conn *sql.Conn) (txOps, error)
	backslashEscapes() bool
	formatTime(t time.Time) string
}

// txOps is one migration's transaction. commit and rollback receive an
// uncancellable context: cleanup must reach the database even when the
// deploy's signal context has already fired.
type txOps interface {
	exec(ctx context.Context, query string, args ...any) error
	has(ctx context.Context, table string, version int64) (bool, error)
	commit(ctx context.Context) error
	rollback(ctx context.Context) error
}

//nolint:ireturn // the seam exists to return one of two dialect shapes
func dialectFor(d Dialect) (dialectOps, error) {
	switch d {
	case DialectSQLite:
		return sqliteDialect{}, nil
	case DialectMySQL:
		return mysqlDialect{}, nil
	default:
		return nil, fmt.Errorf("migrationx: %w: %d", errBadDialect, d)
	}
}

var errBadDialect = errors.New("Config.Dialect must be DialectSQLite or DialectMySQL")

// sqliteDialect: transactional DDL, single-writer file locking. The
// migration lock is the write transaction itself — BEGIN IMMEDIATE takes
// the file's write lock up front, and busy_timeout bounds the wait.
// Transactions run as raw statements on the pinned connection because
// database/sql offers no way to request an IMMEDIATE transaction.
type sqliteDialect struct{}

func (sqliteDialect) createTable(ctx context.Context, conn *sql.Conn, table string) error {
	_, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	checksum   TEXT NOT NULL,
	applied_at TEXT NOT NULL
)`)
	return err
}

func (sqliteDialect) lock(ctx context.Context, conn *sql.Conn, _ string, timeout time.Duration) error {
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", timeout.Milliseconds())); err != nil {
		return fmt.Errorf("migrationx: %w", err)
	}
	return nil
}

func (sqliteDialect) unlock(context.Context, *sql.Conn, string) error { return nil }

//nolint:ireturn // the seam exists to return one of two tx shapes
func (sqliteDialect) begin(ctx context.Context, conn *sql.Conn) (txOps, error) {
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", errLockTimeout, err)
	}
	return rawTx{conn: conn}, nil
}

func (sqliteDialect) backslashEscapes() bool { return false }

func (sqliteDialect) formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// rawTx drives a transaction with literal BEGIN/COMMIT/ROLLBACK on a
// pinned connection.
type rawTx struct {
	conn *sql.Conn
}

func (t rawTx) exec(ctx context.Context, query string, args ...any) error {
	_, err := t.conn.ExecContext(ctx, query, args...)
	return err
}

func (t rawTx) has(ctx context.Context, table string, version int64) (bool, error) {
	return versionExists(ctx, t.conn, table, version)
}

func (t rawTx) commit(ctx context.Context) error {
	_, err := t.conn.ExecContext(ctx, "COMMIT")
	return err
}

// rollback must not leave an open IMMEDIATE transaction on a connection
// headed back to the consumer's pool: if ROLLBACK cannot be delivered,
// the connection is poisoned so the pool discards it.
func (t rawTx) rollback(ctx context.Context) error {
	if _, err := t.conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		_ = t.conn.Raw(func(any) error { return driver.ErrBadConn })
		return err
	}
	return nil
}

// mysqlDialect: GET_LOCK serializes concurrent runners; DDL implicitly
// commits, so a failed multi-statement migration can leave partial DDL
// applied with no history row — documented loudly, visible via Status.
type mysqlDialect struct{}

func (mysqlDialect) createTable(ctx context.Context, conn *sql.Conn, table string) error {
	_, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (
	version    BIGINT PRIMARY KEY,
	name       VARCHAR(255) NOT NULL,
	checksum   CHAR(64) NOT NULL,
	applied_at DATETIME NOT NULL
)`)
	return err
}

func (mysqlDialect) lock(ctx context.Context, conn *sql.Conn, table string, timeout time.Duration) error {
	var got sql.NullInt64
	row := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", "migrationx:"+table, int64(timeout.Seconds()))
	if err := row.Scan(&got); err != nil {
		return fmt.Errorf("migrationx: acquiring lock: %w", err)
	}
	if !got.Valid || got.Int64 != 1 {
		return errLockTimeout
	}
	return nil
}

func (mysqlDialect) unlock(ctx context.Context, conn *sql.Conn, table string) error {
	_, err := conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", "migrationx:"+table)
	return err
}

//nolint:ireturn // the seam exists to return one of two tx shapes
func (mysqlDialect) begin(ctx context.Context, conn *sql.Conn) (txOps, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return sqlTx{tx: tx}, nil
}

func (mysqlDialect) backslashEscapes() bool { return true }

func (mysqlDialect) formatTime(t time.Time) string { return t.UTC().Format(time.DateTime) }

// sqlTx wraps the standard database/sql transaction.
type sqlTx struct {
	tx *sql.Tx
}

func (t sqlTx) has(ctx context.Context, table string, version int64) (bool, error) {
	var one int64
	err := t.tx.QueryRowContext(ctx,
		"SELECT 1 FROM "+table+" WHERE version = ?", version).Scan(&one) // #nosec G202 -- table is validated [A-Za-z0-9_]+ at New
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// versionExists is the shared row probe for raw connections.
func versionExists(ctx context.Context, conn *sql.Conn, table string, version int64) (bool, error) {
	var one int64
	err := conn.QueryRowContext(ctx,
		"SELECT 1 FROM "+table+" WHERE version = ?", version).Scan(&one) // #nosec G202 -- table is validated [A-Za-z0-9_]+ at New
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (t sqlTx) exec(ctx context.Context, query string, args ...any) error {
	_, err := t.tx.ExecContext(ctx, query, args...)
	return err
}

func (t sqlTx) commit(context.Context) error   { return t.tx.Commit() }
func (t sqlTx) rollback(context.Context) error { return t.tx.Rollback() }
