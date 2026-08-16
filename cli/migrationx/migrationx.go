package migrationx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	errNilDB            = errors.New("migrationx: db must not be nil")
	errBadTable         = errors.New("migrationx: table name must be [A-Za-z0-9_]+")
	errChecksumMismatch = errors.New("migrationx: applied migration changed on disk")
	errOrphan           = errors.New("migrationx: applied migration missing from the filesystem")
	errOutOfOrder       = errors.New("migrationx: pending migrations older than the newest applied")
	errNoDown           = errors.New("migrationx: no down migration")
	errNothingApplied   = errors.New("migrationx: nothing applied")
	errBadLockTimeout   = errors.New("migrationx: LockTimeout must be at least one second")
	errDirty            = errors.New("migrationx: dirty migration from a previous no-transaction run")
)

var tablePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// Config configures New. Dialect is required; every other field has a
// safe default.
type Config struct {
	// Dialect selects the database: DialectSQLite or DialectMySQL.
	Dialect Dialect

	// Table is the history table name. Default DefaultTable.
	Table string

	// Log receives progress and the down-audit lines. Nil means
	// slog.Default().
	Log *slog.Logger

	// AllowOutOfOrder applies pending migrations older than the newest
	// applied one — the late-merged branch — instead of refusing. A
	// deploy-time human decision: never enable it unattended.
	AllowOutOfOrder bool

	// LockTimeout bounds the wait for the migration lock. Default
	// DefaultLockTimeout.
	LockTimeout time.Duration
}

// Migration is one row of Status: a version known to the filesystem, the
// history table, or both.
type Migration struct {
	Version    int64
	Name       string
	Applied    bool
	AppliedAt  time.Time // zero when not applied, or when the stored value was unreadable
	OutOfOrder bool      // pending and older than the newest applied
	Dirty      bool      // applied but left dirty by a previous no-transaction run; resolve manually, see doc.go
}

// Migrator applies, rolls back, and reports migrations. Create one with
// New; it is safe for a single caller — migrations are a deploy step,
// not a concurrent workload.
type Migrator struct {
	db         *sql.DB
	dialect    dialectOps
	migrations []migration
	table      string
	log        *slog.Logger
	allowOOO   bool
	lockWait   time.Duration
}

// New validates the configuration, loads and validates every migration
// file, and creates the history table if it does not exist — the first
// deploy needs no manual bootstrap.
func New(db *sql.DB, fsys fs.FS, cfg Config) (*Migrator, error) {
	if db == nil {
		return nil, errNilDB
	}
	d, err := dialectFor(cfg.Dialect)
	if err != nil {
		return nil, err
	}
	table := cfg.Table
	if table == "" {
		table = DefaultTable
	}
	if !tablePattern.MatchString(table) {
		return nil, fmt.Errorf("%w: %q", errBadTable, table)
	}
	logger := cfg.Log
	if logger == nil {
		logger = slog.Default()
	}
	lockWait := cfg.LockTimeout
	if lockWait == 0 {
		lockWait = DefaultLockTimeout
	}
	if lockWait < time.Second {
		return nil, fmt.Errorf("%w: %s", errBadLockTimeout, lockWait)
	}

	migrations, err := loadMigrations(fsys, d.backslashEscapes())
	if err != nil {
		return nil, err
	}

	m := &Migrator{
		db:         db,
		dialect:    d,
		migrations: migrations,
		table:      table,
		log:        logger,
		allowOOO:   cfg.AllowOutOfOrder,
		lockWait:   lockWait,
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrationx: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := d.createTable(ctx, conn, table); err != nil {
		return nil, fmt.Errorf("migrationx: creating %s: %w", table, err)
	}
	if dirtyD, ok := d.(dirtyOps); ok {
		if err := dirtyD.ensureDirtyColumn(ctx, conn, table); err != nil {
			return nil, fmt.Errorf("migrationx: healing %s: %w", table, err)
		}
	}
	return m, nil
}

// Up applies every pending migration in version order.
func (m *Migrator) Up(ctx context.Context) error {
	return m.up(ctx, 0, 0)
}

// UpByOne applies exactly the oldest pending migration.
func (m *Migrator) UpByOne(ctx context.Context) error {
	return m.up(ctx, 1, 0)
}

// UpTo applies pending migrations up to and including version.
func (m *Migrator) UpTo(ctx context.Context, version int64) error {
	return m.up(ctx, 0, version)
}

// Down rolls back the applied migration with the highest version.
func (m *Migrator) Down(ctx context.Context) error {
	return m.down(ctx, -1)
}

// DownTo rolls back every applied migration with a version greater than
// version, highest first; DownTo(ctx, 0) rolls back everything. Negative
// versions are treated as 0.
func (m *Migrator) DownTo(ctx context.Context, version int64) error {
	return m.down(ctx, max(version, 0))
}

// Status reports every migration the filesystem or the history table
// knows, in version order. It takes no lock. A version left dirty by a
// previous no-transaction run is reported with Dirty set, never as an
// error — Up and Down are the calls that refuse dirty state.
func (m *Migrator) Status(ctx context.Context) ([]Migration, error) {
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrationx: %w", err)
	}
	defer func() { _ = conn.Close() }()

	applied, err := m.appliedRows(ctx, conn)
	if err != nil {
		return nil, err
	}
	if err := m.verify(applied); err != nil {
		return nil, err
	}
	dirty, err := m.dirtySet(ctx, conn)
	if err != nil {
		return nil, err
	}

	var maxApplied int64
	for version := range applied {
		maxApplied = max(maxApplied, version)
	}

	out := make([]Migration, 0, len(m.migrations))
	for _, mig := range m.migrations {
		row, ok := applied[mig.version]
		entry := Migration{Version: mig.version, Name: mig.name, Applied: ok}
		if ok {
			entry.AppliedAt = row.appliedAt
			entry.Dirty = dirty[mig.version]
		} else {
			entry.OutOfOrder = mig.version < maxApplied
		}
		out = append(out, entry)
	}
	return out, nil
}

// Version reports the newest applied version, 0 when none. Like Status,
// it takes no lock and does not fail on dirty state — Up and Down are
// the calls that refuse it.
func (m *Migrator) Version(ctx context.Context) (int64, error) {
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("migrationx: %w", err)
	}
	defer func() { _ = conn.Close() }()

	applied, err := m.appliedRows(ctx, conn)
	if err != nil {
		return 0, err
	}
	if err := m.verify(applied); err != nil {
		return 0, err
	}
	var maxApplied int64
	for version := range applied {
		maxApplied = max(maxApplied, version)
	}
	return maxApplied, nil
}

// up locks, verifies, and applies pending migrations. limit 0 means all;
// target 0 means no upper bound.
func (m *Migrator) up(ctx context.Context, limit int, target int64) error {
	return m.locked(ctx, func(conn *sql.Conn) error {
		applied, err := m.appliedRows(ctx, conn)
		if err != nil {
			return err
		}
		if err := m.verify(applied); err != nil {
			return err
		}
		if err := m.checkDirty(ctx, conn); err != nil {
			return err
		}

		var maxApplied int64
		for version := range applied {
			maxApplied = max(maxApplied, version)
		}

		var pending, outOfOrder []migration
		for _, mig := range m.migrations {
			if _, ok := applied[mig.version]; ok {
				continue
			}
			pending = append(pending, mig)
			if mig.version < maxApplied {
				outOfOrder = append(outOfOrder, mig)
			}
		}
		if len(outOfOrder) > 0 && !m.allowOOO {
			return fmt.Errorf("%w: %s (rerun with out-of-order allowed to apply)",
				errOutOfOrder, versionList(outOfOrder))
		}

		count := 0
		for _, mig := range pending {
			if target > 0 && mig.version > target {
				continue
			}
			if limit > 0 && count == limit {
				break
			}
			if err := m.apply(ctx, conn, mig); err != nil {
				return err
			}
			count++
		}
		return nil
	})
}

// down locks, verifies, and rolls back applied migrations newer than
// target, newest first. target -1 means exactly one step.
func (m *Migrator) down(ctx context.Context, target int64) error {
	return m.locked(ctx, func(conn *sql.Conn) error {
		applied, err := m.appliedRows(ctx, conn)
		if err != nil {
			return err
		}
		if err := m.verify(applied); err != nil {
			return err
		}
		if err := m.checkDirty(ctx, conn); err != nil {
			return err
		}
		if len(applied) == 0 {
			return errNothingApplied
		}

		for _, mig := range slices.Backward(m.migrations) {
			if _, ok := applied[mig.version]; !ok {
				continue
			}
			if target >= 0 && mig.version <= target {
				break
			}
			if err := m.rollback(ctx, conn, mig); err != nil {
				return err
			}
			if target < 0 {
				break
			}
		}
		return nil
	})
}

// locked pins one connection, holds the migration lock around fn, and
// always releases both.
func (m *Migrator) locked(ctx context.Context, fn func(conn *sql.Conn) error) error {
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrationx: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := m.dialect.lock(ctx, conn, m.table, m.lockWait); err != nil {
		return err
	}
	defer func() {
		if err := m.dialect.unlock(context.WithoutCancel(ctx), conn, m.table); err != nil {
			m.log.WarnContext(ctx, "migration lock release failed", "err", err)
		}
	}()

	return fn(conn)
}

// appliedRow is one history-table row.
type appliedRow struct {
	name      string
	checksum  string
	appliedAt time.Time
}

// appliedRows loads the history table keyed by version.
func (m *Migrator) appliedRows(ctx context.Context, conn *sql.Conn) (map[int64]appliedRow, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT version, name, checksum, applied_at FROM "+m.table) // #nosec G202 -- table is validated [A-Za-z0-9_]+ at New
	if err != nil {
		return nil, fmt.Errorf("migrationx: reading %s: %w", m.table, err)
	}
	defer func() { _ = rows.Close() }()

	applied := map[int64]appliedRow{}
	for rows.Next() {
		var version int64
		var row appliedRow
		var appliedAt string
		if err := rows.Scan(&version, &row.name, &row.checksum, &appliedAt); err != nil {
			return nil, fmt.Errorf("migrationx: reading %s: %w", m.table, err)
		}
		row.appliedAt = parseTime(appliedAt)
		applied[version] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrationx: reading %s: %w", m.table, err)
	}
	return applied, nil
}

// verify fails closed on the two history incidents: an applied migration
// whose file changed, and an applied migration whose file is gone.
func (m *Migrator) verify(applied map[int64]appliedRow) error {
	byVersion := map[int64]migration{}
	for _, mig := range m.migrations {
		byVersion[mig.version] = mig
	}
	for version, row := range applied {
		mig, ok := byVersion[version]
		if !ok {
			return fmt.Errorf("%w: %d_%s", errOrphan, version, row.name)
		}
		if mig.checksum != row.checksum {
			return fmt.Errorf("%w: %d_%s: recorded %s, file %s",
				errChecksumMismatch, version, mig.name, row.checksum, mig.checksum)
		}
	}
	return nil
}

// checkDirty fails closed when a previous no-transaction run left a
// version dirty: the dialect owns clearing it during a run, an operator
// owns clearing it after a failure — this method only ever refuses. Only
// Up and Down call it; Status and Version report dirty state instead of
// failing on it.
func (m *Migrator) checkDirty(ctx context.Context, conn *sql.Conn) error {
	dirty, err := m.dirtySet(ctx, conn)
	if err != nil {
		return err
	}
	if len(dirty) == 0 {
		return nil
	}
	versions := make([]int64, 0, len(dirty))
	for v := range dirty {
		versions = append(versions, v)
	}
	return fmt.Errorf("%w: %s (resolve manually before rerunning)", errDirty, dirtyVersionList(versions))
}

// dirtySet reports every version a previous no-transaction run left
// dirty, empty when the dialect tracks no such state.
func (m *Migrator) dirtySet(ctx context.Context, conn *sql.Conn) (map[int64]bool, error) {
	dirtyD, ok := m.dialect.(dirtyOps)
	if !ok {
		return map[int64]bool{}, nil
	}
	versions, err := dirtyD.dirtyVersions(ctx, conn, m.table)
	if err != nil {
		return nil, fmt.Errorf("migrationx: reading dirty state: %w", err)
	}
	set := make(map[int64]bool, len(versions))
	for _, v := range versions {
		set[v] = true
	}
	return set, nil
}

// dirtyVersionList renders dirty versions for the refusal error, sorted
// for a deterministic message regardless of the dialect's row order.
func dirtyVersionList(versions []int64) string {
	sorted := slices.Clone(versions)
	slices.Sort(sorted)
	parts := make([]string, len(sorted))
	for i, v := range sorted {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(parts, ", ")
}

// apply runs one up migration and records it, transactionally where the
// file allows.
func (m *Migrator) apply(ctx context.Context, conn *sql.Conn, mig migration) error {
	start := timeNow()
	insert := "INSERT INTO " + m.table + " (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)" // #nosec G202 -- table is validated [A-Za-z0-9_]+ at New
	appliedAt := m.dialect.formatTime(start)

	var err error
	var skipped bool
	if mig.up.noTx {
		record := func() error {
			_, e := conn.ExecContext(ctx, insert, mig.version, mig.name, mig.checksum, appliedAt)
			return e
		}
		var premark func() error
		if dirtyD, ok := m.dialect.(dirtyOps); ok {
			premark = func() error {
				return dirtyD.markDirty(ctx, conn, m.table, mig.version, mig.name, mig.checksum, appliedAt)
			}
			record = func() error {
				return dirtyD.setDirty(ctx, conn, m.table, mig.version, false)
			}
		}
		skipped, err = m.execRaw(ctx, conn, mig.version, mig.up.statements, premark, record)
	} else {
		skipped, err = m.execTx(ctx, conn, mig.version, false, mig.up.statements, func(tx txOps) error {
			return tx.exec(ctx, insert, mig.version, mig.name, mig.checksum, appliedAt)
		})
	}
	if err != nil {
		return fmt.Errorf("migrationx: applying %d_%s: %w", mig.version, mig.name, err)
	}
	if skipped {
		m.log.InfoContext(ctx, "migration already applied by a concurrent runner",
			"version", mig.version, "name", mig.name)
		return nil
	}
	m.log.InfoContext(ctx, "migration applied",
		"version", mig.version, "name", mig.name, "duration_ms", timeNow().Sub(start).Milliseconds())
	return nil
}

// rollback runs one down migration, deletes its history row, and emits
// the audit lines — the database keeps no trace of a rollback by design,
// so the log is the surviving record.
func (m *Migrator) rollback(ctx context.Context, conn *sql.Conn, mig migration) error {
	if mig.down == nil {
		return fmt.Errorf("%w: %d_%s", errNoDown, mig.version, mig.name)
	}
	host, _ := os.Hostname()
	audit := make([]any, 0, 12)
	audit = append(audit,
		"version", mig.version, "name", mig.name,
		"host", host, "os_user", osUser())
	m.log.WarnContext(ctx, "migration rollback starting", audit...)

	start := timeNow()
	remove := "DELETE FROM " + m.table + " WHERE version = ?" // #nosec G202 -- table is validated [A-Za-z0-9_]+ at New

	var err error
	if mig.down.noTx {
		var premark func() error
		if dirtyD, ok := m.dialect.(dirtyOps); ok {
			premark = func() error {
				return dirtyD.setDirty(ctx, conn, m.table, mig.version, true)
			}
		}
		_, err = m.execRaw(ctx, conn, -1, mig.down.statements, premark, func() error {
			_, e := conn.ExecContext(ctx, remove, mig.version)
			return e
		})
	} else {
		_, err = m.execTx(ctx, conn, mig.version, true, mig.down.statements, func(tx txOps) error {
			return tx.exec(ctx, remove, mig.version)
		})
	}

	audit = append(audit, "duration_ms", timeNow().Sub(start).Milliseconds(), "ok", err == nil)
	m.log.WarnContext(ctx, "migration rollback finished", audit...)
	if err != nil {
		return fmt.Errorf("migrationx: rolling back %d_%s: %w", mig.version, mig.name, err)
	}
	return nil
}

// execTx runs statements plus the history write in one transaction.
// Inside the transaction it re-probes the history row — the sqlite lock
// is per-transaction, not per-run, so a concurrent runner may have
// applied (or rolled back) this version between the unlocked read and
// here; the probe makes the interleaving skip instead of double-execute.
// Cleanup runs on an uncancellable context so a fired deploy signal can
// never strand an open transaction on a pooled connection.
func (m *Migrator) execTx(ctx context.Context, conn *sql.Conn, version int64, down bool, statements []string, record func(txOps) error) (bool, error) {
	cleanupCtx := context.WithoutCancel(ctx)
	tx, err := m.dialect.begin(ctx, conn)
	if err != nil {
		return false, err
	}
	exists, err := tx.has(cleanupCtx, m.table, version)
	if err != nil {
		_ = tx.rollback(cleanupCtx)
		return false, err
	}
	if exists != down {
		_ = tx.rollback(cleanupCtx)
		return true, nil
	}
	for _, stmt := range statements {
		if err := tx.exec(ctx, stmt); err != nil {
			_ = tx.rollback(cleanupCtx)
			return false, fmt.Errorf("%w (statement: %s)", err, firstLine(stmt))
		}
	}
	if err := record(tx); err != nil {
		_ = tx.rollback(cleanupCtx)
		return false, err
	}
	return false, tx.commit(cleanupCtx)
}

// execRaw runs statements outside any transaction — the no-transaction
// annotation — then the history write. version >= 0 probes the history
// row first so a concurrent runner's finished apply is skipped, never
// re-executed: for an unprotected data backfill, running twice is the
// incident. premark, when non-nil, runs after the probe and before the
// statements — the dirty-state hook for dialects where a failed
// statement can leave the database changed with nothing to show it.
func (m *Migrator) execRaw(ctx context.Context, conn *sql.Conn, version int64, statements []string, premark, record func() error) (bool, error) {
	if version >= 0 {
		exists, err := versionExists(ctx, conn, m.table, version)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	if premark != nil {
		if err := premark(); err != nil {
			return false, err
		}
	}
	for _, stmt := range statements {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return false, fmt.Errorf("%w (statement: %s)", err, firstLine(stmt))
		}
	}
	return false, record()
}

// currentUser is a seam over user.Current; tests replace it.
var currentUser = user.Current //nolint:gochecknoglobals // test seam, same pattern as timeNow

// osUser names the operator for the audit line, portably.
func osUser() string {
	if u, err := currentUser(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

func versionList(migrations []migration) string {
	parts := make([]string, len(migrations))
	for i, mig := range migrations {
		parts[i] = fmt.Sprintf("%d_%s", mig.version, mig.name)
	}
	return strings.Join(parts, ", ")
}

// firstLine bounds a statement echo in errors to its first line.
func firstLine(stmt string) string {
	line, _, _ := strings.Cut(stmt, "\n")
	return line
}

// parseTime reads either dialect's stored format; a zero time means the
// stored value was unreadable, which Status surfaces as-is rather than
// failing a read-only report.
func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, time.DateTime} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
