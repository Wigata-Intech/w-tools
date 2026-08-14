package migrationx

import (
	"context"
	"database/sql"
	"os/user"
	"time"
)

// SetTimeNow replaces the clock and returns a restore func.
func SetTimeNow(now func() time.Time) func() {
	prev := timeNow
	timeNow = now
	return func() { timeNow = prev }
}

// ParseScript exposes parseScript to the blackbox tests, flattened into
// the split statements, the no-transaction flag, and any parse error.
func ParseScript(src string, backslashEscapes bool) ([]string, bool, error) {
	s, err := parseScript(src, backslashEscapes)
	return s.statements, s.noTx, err
}

// ParseFilename exposes parseFilename to the blackbox tests, flattened
// into the version, the migration name, and the up/down direction.
func ParseFilename(filename string) (int64, string, bool, error) {
	h, err := parseFilename(filename)
	return h.version, h.name, h.down, err
}

// SetCurrentUser replaces the user lookup and returns a restore func.
func SetCurrentUser(f func() (*user.User, error)) func() {
	prev := currentUser
	currentUser = f
	return func() { currentUser = prev }
}

// BeginSQLite exposes the sqlite dialect's begin for the cancelled
// context branch, unreachable through the public API with a fake driver.
func BeginSQLite(ctx context.Context, conn *sql.Conn) error {
	_, err := sqliteDialect{}.begin(ctx, conn)
	return err
}

// RunExecRaw exposes the no-transaction skip probe: the branch fires
// only in a true runner race, which no deterministic driver sequence
// can produce through the public API.
func RunExecRaw(ctx context.Context, m *Migrator, version int64) (bool, error) {
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()
	return m.execRaw(ctx, conn, version, []string{"SELECT 1"}, func() error { return nil })
}

// RunApply exposes one transactional apply: the in-tx probe skip fires
// only in a true runner race, unreachable deterministically through the
// public API.
func RunApply(ctx context.Context, m *Migrator, version int64) error {
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	for _, mig := range m.migrations {
		if mig.version == version {
			return m.apply(ctx, conn, mig)
		}
	}
	return errNothingApplied
}
