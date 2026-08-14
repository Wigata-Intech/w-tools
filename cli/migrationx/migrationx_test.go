package migrationx_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os/user"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wigata-Intech/w-tools/cli/migrationx"
)

// The fixed clock and the timestamps seeded history rows carry.
var (
	mxNow   = time.Date(2026, 8, 14, 10, 30, 0, 0, time.UTC) //nolint:gochecknoglobals // immutable fixture shared by every scenario
	mxT0900 = time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)   //nolint:gochecknoglobals // immutable fixture shared by every scenario
	mxT0905 = time.Date(2026, 8, 14, 9, 5, 0, 0, time.UTC)   //nolint:gochecknoglobals // immutable fixture shared by every scenario
)

const mxSeededAt = "2026-08-14T09:00:00Z"

// Migration file bodies. Each statement carries a unique marker (a1, b1,
// ...) so the execution log can be asserted without ambiguity.
const (
	mxUpA      = "CREATE TABLE a1 (id INTEGER);\nCREATE TABLE a2 (id INTEGER);\n"
	mxDownA    = "DROP TABLE a1;\n"
	mxUpB      = "CREATE TABLE b1 (id INTEGER);\n"
	mxDownB    = "DROP TABLE b1;\n"
	mxUpC      = "CREATE TABLE c1 (id INTEGER);\n"
	mxDownC    = "DROP TABLE c1;\n"
	mxUpD      = "CREATE TABLE d1 (id INTEGER);\n"
	mxUpE      = "CREATE TABLE e1 (id INTEGER);\n"
	mxUpBad    = "CREATE TABLE bad1 (\n\tid INTEGER\n);\n"
	mxUpNoTx   = "-- migrationx:no-transaction\nCREATE INDEX nx1 ON a1 (id);\n"
	mxDownNoTx = "-- migrationx:no-transaction\nDROP INDEX nx1;\n"
)

var errMxBoom = errors.New("boom")

// mxChecksum returns the sha256 hex of an up file's bytes — the value the
// engine records, used to seed matching history rows.
func mxChecksum(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// mxFS builds a migration filesystem from filename -> contents.
func mxFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, data := range files {
		out[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return out
}

// mxResetExecuted drops the statement log so assertions cover only the
// call under test, not New's table bootstrap.
func mxResetExecuted(s *fakeState) {
	s.mu.Lock()
	s.executed = nil
	s.mu.Unlock()
}

// mxAssertApplied compares the committed history versions, order-blind.
func mxAssertApplied(t *testing.T, s *fakeState, want []int64) {
	t.Helper()
	got := s.appliedVersions()
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("applied versions = %v, want %v", got, want)
	}
}

// mxAssertCounts checks executedContaining for every substring.
func mxAssertCounts(t *testing.T, s *fakeState, counts map[string]int) {
	t.Helper()
	for sub, want := range counts {
		if got := s.executedContaining(sub); got != want {
			t.Errorf("executedContaining(%q) = %d, want %d", sub, got, want)
		}
	}
}

// mxAssertOrder checks that statements containing the substrings were
// executed in the given relative order.
func mxAssertOrder(t *testing.T, s *fakeState, subs []string) {
	t.Helper()
	s.mu.Lock()
	executed := slices.Clone(s.executed)
	s.mu.Unlock()
	i := 0
	for _, q := range executed {
		if i < len(subs) && strings.Contains(q, subs[i]) {
			i++
		}
	}
	if i != len(subs) {
		t.Errorf("statements %q not executed in that order; log:\n%s", subs, strings.Join(executed, "\n"))
	}
}

// mxInput is one engine scenario: the migration filesystem, the seeded
// history, and the fault to inject before the call under test.
type mxInput struct {
	dialect  migrationx.Dialect
	files    map[string]string
	seed     func(s *fakeState)
	allowOOO bool
	failOn   map[string]error
	lockBusy bool
	closeDB  bool
	target   int64 // UpTo / DownTo argument
}

// mxExpected is one scenario's outcome: the exact error text ("" means
// success), the committed versions afterward, and statement-log checks.
type mxExpected struct {
	err     string
	applied []int64
	counts  map[string]int
	order   []string
}

// mxRun builds a migrator over a fresh fake database, injects the
// scenario's fault, invokes call, and checks the outcome.
func mxRun(t *testing.T, input mxInput, call func(context.Context, *migrationx.Migrator) error, expected mxExpected) {
	t.Helper()
	restore := migrationx.SetTimeNow(func() time.Time { return mxNow })
	defer restore()

	db, state := fakeDB(t)
	if input.seed != nil {
		input.seed(state)
	}
	m, err := migrationx.New(db, mxFS(input.files), migrationx.Config{
		Dialect:         input.dialect,
		AllowOutOfOrder: input.allowOOO,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	mxResetExecuted(state)
	state.mu.Lock()
	maps.Copy(state.failOn, input.failOn)
	state.lockBusy = input.lockBusy
	state.mu.Unlock()
	if input.closeDB {
		_ = db.Close()
	}

	err = call(context.Background(), m)
	if expected.err == "" {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	} else if err == nil || err.Error() != expected.err {
		t.Fatalf("error = %v, want %q", err, expected.err)
	}
	mxAssertApplied(t, state, expected.applied)
	mxAssertCounts(t, state, expected.counts)
	if len(expected.order) > 0 {
		mxAssertOrder(t, state, expected.order)
	}
}

func TestNew(t *testing.T) {
	type input struct {
		nilDB   bool
		closeDB bool
		dialect migrationx.Dialect
		table   string
		files   map[string]string
		failOn  map[string]error
	}
	type expected struct {
		err    string
		counts map[string]int
	}
	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name:  "defaults create the sqlite history table",
			input: input{dialect: migrationx.DialectSQLite},
			expected: expected{
				counts: map[string]int{"CREATE TABLE IF NOT EXISTS migration_histories": 1},
			},
		},
		{
			name:  "custom table name is used",
			input: input{dialect: migrationx.DialectSQLite, table: "schema_history"},
			expected: expected{
				counts: map[string]int{"CREATE TABLE IF NOT EXISTS schema_history": 1},
			},
		},
		{
			name:  "mysql history table",
			input: input{dialect: migrationx.DialectMySQL},
			expected: expected{
				counts: map[string]int{
					"CREATE TABLE IF NOT EXISTS migration_histories": 1,
					"BIGINT PRIMARY KEY":                             1,
				},
			},
		},
		{
			name:     "nil db",
			input:    input{nilDB: true, dialect: migrationx.DialectSQLite},
			expected: expected{err: "migrationx: db must not be nil"},
		},
		{
			name:     "invalid dialect",
			input:    input{},
			expected: expected{err: "migrationx: Config.Dialect must be DialectSQLite or DialectMySQL: 0"},
		},
		{
			name:     "invalid table name",
			input:    input{dialect: migrationx.DialectSQLite, table: "bad-table"},
			expected: expected{err: `migrationx: table name must be [A-Za-z0-9_]+: "bad-table"`},
		},
		{
			name:     "load error propagates",
			input:    input{dialect: migrationx.DialectSQLite, files: map[string]string{"notes.txt": ""}},
			expected: expected{err: "migrationx: notes.txt: not a <unix-timestamp>_<name>.up.sql or .down.sql file"},
		},
		{
			name:     "closed database",
			input:    input{dialect: migrationx.DialectSQLite, closeDB: true},
			expected: expected{err: "migrationx: sql: database is closed"},
		},
		{
			name: "history table creation failure",
			input: input{
				dialect: migrationx.DialectSQLite,
				failOn:  map[string]error{"CREATE TABLE": errMxBoom},
			},
			expected: expected{err: "migrationx: creating migration_histories: boom"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, state := fakeDB(t)
			state.mu.Lock()
			maps.Copy(state.failOn, tt.input.failOn)
			state.mu.Unlock()
			if tt.input.closeDB {
				_ = db.Close()
			}
			if tt.input.nilDB {
				db = nil
			}
			m, err := migrationx.New(db, mxFS(tt.input.files), migrationx.Config{
				Dialect: tt.input.dialect,
				Table:   tt.input.table,
			})
			if tt.expected.err == "" {
				if err != nil {
					t.Fatalf("New() error = %v, want nil", err)
				}
				if m == nil {
					t.Fatal("New() = nil migrator")
				}
			} else if err == nil || err.Error() != tt.expected.err {
				t.Fatalf("New() error = %v, want %q", err, tt.expected.err)
			}
			mxAssertCounts(t, state, tt.expected.counts)
		})
	}
}

func TestUp(t *testing.T) {
	tests := []struct {
		name     string
		input    mxInput
		expected mxExpected
	}{
		{
			name: "applies all pending in order",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB},
			},
			expected: mxExpected{
				applied: []int64{100, 200},
				counts: map[string]int{
					"PRAGMA busy_timeout = 30000":     1,
					"BEGIN IMMEDIATE":                 2,
					"COMMIT":                          2,
					"INSERT INTO migration_histories": 2,
				},
				order: []string{
					"CREATE TABLE a1", "CREATE TABLE a2", "INSERT INTO migration_histories",
					"CREATE TABLE b1", "INSERT INTO migration_histories",
				},
			},
		},
		{
			name: "nothing pending is a no-op",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
					s.setApplied(200, "b", mxChecksum(mxUpB), mxSeededAt)
				},
			},
			expected: mxExpected{
				applied: []int64{100, 200},
				counts:  map[string]int{"BEGIN IMMEDIATE": 0, "INSERT INTO migration_histories": 0},
			},
		},
		{
			name: "mysql applies and releases the lock",
			input: mxInput{
				dialect: migrationx.DialectMySQL,
				files:   map[string]string{"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB},
			},
			expected: mxExpected{
				applied: []int64{100, 200},
				counts: map[string]int{
					"GET_LOCK":                        1,
					"RELEASE_LOCK":                    1,
					"INSERT INTO migration_histories": 2,
				},
				order: []string{"CREATE TABLE a1", "CREATE TABLE a2", "CREATE TABLE b1"},
			},
		},
		{
			name: "no-transaction migration skips the transaction",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_nx.up.sql": mxUpNoTx},
			},
			expected: mxExpected{
				applied: []int64{100},
				counts: map[string]int{
					"BEGIN":                           0,
					"COMMIT":                          0,
					"CREATE INDEX nx1":                1,
					"INSERT INTO migration_histories": 1,
				},
			},
		},
		{
			name: "allow out of order applies the late merge",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files: map[string]string{
					"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB, "300_c.up.sql": mxUpC,
					"400_d.up.sql": mxUpD, "500_e.up.sql": mxUpE,
				},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
					s.setApplied(200, "b", mxChecksum(mxUpB), mxSeededAt)
					s.setApplied(500, "e", mxChecksum(mxUpE), mxSeededAt)
				},
				allowOOO: true,
			},
			expected: mxExpected{
				applied: []int64{100, 200, 300, 400, 500},
				counts:  map[string]int{"CREATE TABLE c1": 1, "CREATE TABLE d1": 1, "CREATE TABLE e1": 0},
				order:   []string{"CREATE TABLE c1", "CREATE TABLE d1"},
			},
		},
		{
			name: "closed database",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				closeDB: true,
			},
			expected: mxExpected{err: "migrationx: sql: database is closed"},
		},
		{
			name: "sqlite busy timeout pragma failure",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				failOn:  map[string]error{"PRAGMA busy_timeout": errMxBoom},
			},
			expected: mxExpected{err: "migrationx: boom"},
		},
		{
			name: "mysql lock busy",
			input: mxInput{
				dialect:  migrationx.DialectMySQL,
				files:    map[string]string{"100_a.up.sql": mxUpA},
				lockBusy: true,
			},
			expected: mxExpected{
				err:    "migrationx: could not acquire the migration lock",
				counts: map[string]int{"RELEASE_LOCK": 0},
			},
		},
		{
			name: "mysql lock query failure",
			input: mxInput{
				dialect: migrationx.DialectMySQL,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				failOn:  map[string]error{"GET_LOCK": errMxBoom},
			},
			expected: mxExpected{err: "migrationx: acquiring lock: boom"},
		},
		{
			name: "history read failure",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				failOn:  map[string]error{"SELECT version": errMxBoom},
			},
			expected: mxExpected{err: "migrationx: reading migration_histories: boom"},
		},
		{
			name: "checksum tamper fails closed",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", "deadbeef", mxSeededAt)
				},
			},
			expected: mxExpected{
				err:     "migrationx: applied migration changed on disk: 100_a: recorded deadbeef, file " + mxChecksum(mxUpA),
				applied: []int64{100},
			},
		},
		{
			name: "orphan applied row fails closed",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(999, "ghost", "cafe", mxSeededAt)
				},
			},
			expected: mxExpected{
				err:     "migrationx: applied migration missing from the filesystem: 999_ghost",
				applied: []int64{999},
			},
		},
		{
			name: "refuses the late merge by default",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files: map[string]string{
					"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB, "300_c.up.sql": mxUpC,
					"400_d.up.sql": mxUpD, "500_e.up.sql": mxUpE,
				},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
					s.setApplied(200, "b", mxChecksum(mxUpB), mxSeededAt)
					s.setApplied(500, "e", mxChecksum(mxUpE), mxSeededAt)
				},
			},
			expected: mxExpected{
				err:     "migrationx: pending migrations older than the newest applied: 300_c, 400_d (rerun with out-of-order allowed to apply)",
				applied: []int64{100, 200, 500},
				counts:  map[string]int{"BEGIN IMMEDIATE": 0},
			},
		},
		{
			name: "sqlite write lock unavailable",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				failOn:  map[string]error{"BEGIN IMMEDIATE": errMxBoom},
			},
			expected: mxExpected{
				err: "migrationx: applying 100_a: migrationx: could not acquire the migration lock: boom",
			},
		},
		{
			name: "failing statement rolls back and stops",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files: map[string]string{
					"100_a.up.sql": mxUpA, "200_bad.up.sql": mxUpBad, "300_c.up.sql": mxUpC,
				},
				failOn: map[string]error{"bad1": errMxBoom},
			},
			expected: mxExpected{
				err:     "migrationx: applying 200_bad: boom (statement: CREATE TABLE bad1 ()",
				applied: []int64{100},
				counts:  map[string]int{"ROLLBACK": 1, "CREATE TABLE c1": 0},
			},
		},
		{
			name: "mysql failing statement rolls back",
			input: mxInput{
				dialect: migrationx.DialectMySQL,
				files:   map[string]string{"100_bad.up.sql": mxUpBad},
				failOn:  map[string]error{"bad1": errMxBoom},
			},
			expected: mxExpected{
				err:    "migrationx: applying 100_bad: boom (statement: CREATE TABLE bad1 ()",
				counts: map[string]int{"RELEASE_LOCK": 1},
			},
		},
		{
			name: "no-transaction failing statement stops",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_nx.up.sql": mxUpNoTx},
				failOn:  map[string]error{"nx1": errMxBoom},
			},
			expected: mxExpected{
				err:    "migrationx: applying 100_nx: boom (statement: CREATE INDEX nx1 ON a1 (id))",
				counts: map[string]int{"INSERT INTO migration_histories": 0},
			},
		},
		{
			name: "history insert failure rolls back",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				failOn:  map[string]error{"INSERT INTO": errMxBoom},
			},
			expected: mxExpected{
				err:    "migrationx: applying 100_a: boom",
				counts: map[string]int{"ROLLBACK": 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mxRun(t, tt.input, func(ctx context.Context, m *migrationx.Migrator) error {
				return m.Up(ctx)
			}, tt.expected)
		})
	}
}

func TestUpByOne(t *testing.T) {
	tests := []struct {
		name     string
		input    mxInput
		expected mxExpected
	}{
		{
			name: "applies exactly the oldest pending",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB},
			},
			expected: mxExpected{
				applied: []int64{100},
				counts:  map[string]int{"CREATE TABLE b1": 0, "INSERT INTO migration_histories": 1},
			},
		},
		{
			name: "nothing pending is a no-op",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
				},
			},
			expected: mxExpected{
				applied: []int64{100},
				counts:  map[string]int{"INSERT INTO migration_histories": 0},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mxRun(t, tt.input, func(ctx context.Context, m *migrationx.Migrator) error {
				return m.UpByOne(ctx)
			}, tt.expected)
		})
	}

	t.Run("sequential calls advance one migration at a time", func(t *testing.T) {
		restore := migrationx.SetTimeNow(func() time.Time { return mxNow })
		defer restore()
		db, state := fakeDB(t)
		m, err := migrationx.New(db, mxFS(map[string]string{
			"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB,
		}), migrationx.Config{Dialect: migrationx.DialectSQLite})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		ctx := context.Background()
		if err := m.UpByOne(ctx); err != nil {
			t.Fatalf("first UpByOne() error = %v", err)
		}
		mxAssertApplied(t, state, []int64{100})
		if err := m.UpByOne(ctx); err != nil {
			t.Fatalf("second UpByOne() error = %v", err)
		}
		mxAssertApplied(t, state, []int64{100, 200})
		if err := m.UpByOne(ctx); err != nil {
			t.Fatalf("exhausted UpByOne() error = %v", err)
		}
		mxAssertApplied(t, state, []int64{100, 200})
	})
}

func TestUpTo(t *testing.T) {
	files := map[string]string{
		"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB, "300_c.up.sql": mxUpC,
	}
	tests := []struct {
		name     string
		input    mxInput
		expected mxExpected
	}{
		{
			name:  "applies up to and including the target",
			input: mxInput{dialect: migrationx.DialectSQLite, files: files, target: 200},
			expected: mxExpected{
				applied: []int64{100, 200},
				counts:  map[string]int{"CREATE TABLE c1": 0},
			},
		},
		{
			name:     "target above the newest applies everything",
			input:    mxInput{dialect: migrationx.DialectSQLite, files: files, target: 999},
			expected: mxExpected{applied: []int64{100, 200, 300}},
		},
		{
			name:  "target below the oldest applies nothing",
			input: mxInput{dialect: migrationx.DialectSQLite, files: files, target: 50},
			expected: mxExpected{
				counts: map[string]int{"INSERT INTO migration_histories": 0},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mxRun(t, tt.input, func(ctx context.Context, m *migrationx.Migrator) error {
				return m.UpTo(ctx, tt.input.target)
			}, tt.expected)
		})
	}
}

func TestDown(t *testing.T) {
	files := map[string]string{
		"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA,
		"200_b.up.sql": mxUpB, "200_b.down.sql": mxDownB,
	}
	seedBoth := func(s *fakeState) {
		s.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
		s.setApplied(200, "b", mxChecksum(mxUpB), mxSeededAt)
	}
	tests := []struct {
		name     string
		input    mxInput
		expected mxExpected
	}{
		{
			name:  "rolls back only the newest applied",
			input: mxInput{dialect: migrationx.DialectSQLite, files: files, seed: seedBoth},
			expected: mxExpected{
				applied: []int64{100},
				counts: map[string]int{
					"DROP TABLE b1":                   1,
					"DROP TABLE a1":                   0,
					"DELETE FROM migration_histories": 1,
				},
				order: []string{"DROP TABLE b1", "DELETE FROM migration_histories"},
			},
		},
		{
			name:  "mysql rolls back and releases the lock",
			input: mxInput{dialect: migrationx.DialectMySQL, files: files, seed: seedBoth},
			expected: mxExpected{
				applied: []int64{100},
				counts:  map[string]int{"GET_LOCK": 1, "RELEASE_LOCK": 1, "DROP TABLE b1": 1},
			},
		},
		{
			name: "no-transaction down skips the transaction",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_nx.up.sql": mxUpNoTx, "100_nx.down.sql": mxDownNoTx},
				seed: func(s *fakeState) {
					s.setApplied(100, "nx", mxChecksum(mxUpNoTx), mxSeededAt)
				},
			},
			expected: mxExpected{
				counts: map[string]int{
					"BEGIN":                           0,
					"DROP INDEX nx1":                  1,
					"DELETE FROM migration_histories": 1,
				},
			},
		},
		{
			name:     "closed database",
			input:    mxInput{dialect: migrationx.DialectSQLite, files: files, seed: seedBoth, closeDB: true},
			expected: mxExpected{err: "migrationx: sql: database is closed", applied: []int64{100, 200}},
		},
		{
			name: "history read failure",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   files,
				seed:    seedBoth,
				failOn:  map[string]error{"SELECT version": errMxBoom},
			},
			expected: mxExpected{
				err:     "migrationx: reading migration_histories: boom",
				applied: []int64{100, 200},
			},
		},
		{
			name: "checksum tamper fails closed",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", "deadbeef", mxSeededAt)
				},
			},
			expected: mxExpected{
				err:     "migrationx: applied migration changed on disk: 100_a: recorded deadbeef, file " + mxChecksum(mxUpA),
				applied: []int64{100},
			},
		},
		{
			name: "orphan applied row fails closed",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA},
				seed: func(s *fakeState) {
					s.setApplied(999, "ghost", "cafe", mxSeededAt)
				},
			},
			expected: mxExpected{
				err:     "migrationx: applied migration missing from the filesystem: 999_ghost",
				applied: []int64{999},
			},
		},
		{
			name:     "nothing applied",
			input:    mxInput{dialect: migrationx.DialectSQLite, files: files},
			expected: mxExpected{err: "migrationx: nothing applied"},
		},
		{
			name: "missing down migration",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
				},
			},
			expected: mxExpected{
				err:     "migrationx: no down migration: 100_a",
				applied: []int64{100},
			},
		},
		{
			name: "failing down statement keeps the history row",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   files,
				seed:    seedBoth,
				failOn:  map[string]error{"DROP TABLE b1": errMxBoom},
			},
			expected: mxExpected{
				err:     "migrationx: rolling back 200_b: boom (statement: DROP TABLE b1)",
				applied: []int64{100, 200},
				counts:  map[string]int{"ROLLBACK": 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mxRun(t, tt.input, func(ctx context.Context, m *migrationx.Migrator) error {
				return m.Down(ctx)
			}, tt.expected)
		})
	}

	t.Run("audit lines record the rollback", func(t *testing.T) {
		restore := migrationx.SetTimeNow(func() time.Time { return mxNow })
		defer restore()
		db, state := fakeDB(t)
		state.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
		var buf bytes.Buffer
		m, err := migrationx.New(db, mxFS(map[string]string{
			"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA,
		}), migrationx.Config{
			Dialect: migrationx.DialectSQLite,
			Log:     slog.New(slog.NewTextHandler(&buf, nil)),
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if err := m.Down(context.Background()); err != nil {
			t.Fatalf("Down() error = %v", err)
		}
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("log lines = %d, want 2:\n%s", len(lines), buf.String())
		}
		for _, want := range []string{
			"level=WARN", `msg="migration rollback starting"`,
			"version=100", "name=a", "host=", "os_user=",
		} {
			if !strings.Contains(lines[0], want) {
				t.Errorf("starting line missing %q: %s", want, lines[0])
			}
		}
		for _, want := range []string{
			"level=WARN", `msg="migration rollback finished"`,
			"version=100", "name=a", "host=", "os_user=", "ok=true",
		} {
			if !strings.Contains(lines[1], want) {
				t.Errorf("finished line missing %q: %s", want, lines[1])
			}
		}
	})
}

func TestDownTo(t *testing.T) {
	files := map[string]string{
		"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA,
		"200_b.up.sql": mxUpB, "200_b.down.sql": mxDownB,
		"300_c.up.sql": mxUpC, "300_c.down.sql": mxDownC,
	}
	seedAll := func(s *fakeState) {
		s.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
		s.setApplied(200, "b", mxChecksum(mxUpB), mxSeededAt)
		s.setApplied(300, "c", mxChecksum(mxUpC), mxSeededAt)
	}
	tests := []struct {
		name     string
		input    mxInput
		expected mxExpected
	}{
		{
			name:  "rolls back everything above the target",
			input: mxInput{dialect: migrationx.DialectSQLite, files: files, seed: seedAll, target: 100},
			expected: mxExpected{
				applied: []int64{100},
				counts:  map[string]int{"DROP TABLE c1": 1, "DROP TABLE b1": 1, "DROP TABLE a1": 0},
				order:   []string{"DROP TABLE c1", "DROP TABLE b1"},
			},
		},
		{
			name:  "zero rolls back everything newest first",
			input: mxInput{dialect: migrationx.DialectSQLite, files: files, seed: seedAll, target: 0},
			expected: mxExpected{
				counts: map[string]int{"DELETE FROM migration_histories": 3},
				order:  []string{"DROP TABLE c1", "DROP TABLE b1", "DROP TABLE a1"},
			},
		},
		{
			name: "skips versions never applied",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   files,
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
					s.setApplied(300, "c", mxChecksum(mxUpC), mxSeededAt)
				},
				target: 0,
			},
			expected: mxExpected{
				counts: map[string]int{"DROP TABLE c1": 1, "DROP TABLE b1": 0, "DROP TABLE a1": 1},
				order:  []string{"DROP TABLE c1", "DROP TABLE a1"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mxRun(t, tt.input, func(ctx context.Context, m *migrationx.Migrator) error {
				return m.DownTo(ctx, tt.input.target)
			}, tt.expected)
		})
	}
}

func TestStatus(t *testing.T) {
	type expected struct {
		err  string
		rows []migrationx.Migration
	}
	tests := []struct {
		name     string
		input    mxInput
		expected expected
	}{
		{
			name: "merges the filesystem and the history",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files: map[string]string{
					"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB,
					"300_c.up.sql": mxUpC, "400_d.up.sql": mxUpD,
				},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), "2026-08-14T09:00:00Z")
					s.setApplied(300, "c", mxChecksum(mxUpC), "2026-08-14T09:05:00Z")
				},
			},
			expected: expected{
				rows: []migrationx.Migration{
					{Version: 100, Name: "a", Applied: true, AppliedAt: mxT0900},
					{Version: 200, Name: "b", OutOfOrder: true},
					{Version: 300, Name: "c", Applied: true, AppliedAt: mxT0905},
					{Version: 400, Name: "d"},
				},
			},
		},
		{
			name: "mysql datetime applied at parses",
			input: mxInput{
				dialect: migrationx.DialectMySQL,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), "2026-08-14 09:00:00")
				},
			},
			expected: expected{
				rows: []migrationx.Migration{
					{Version: 100, Name: "a", Applied: true, AppliedAt: mxT0900},
				},
			},
		},
		{
			name: "unreadable applied at becomes the zero time",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), "not-a-time")
				},
			},
			expected: expected{
				rows: []migrationx.Migration{
					{Version: 100, Name: "a", Applied: true},
				},
			},
		},
		{
			name: "closed database",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				closeDB: true,
			},
			expected: expected{err: "migrationx: sql: database is closed"},
		},
		{
			name: "history read failure",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				failOn:  map[string]error{"SELECT version": errMxBoom},
			},
			expected: expected{err: "migrationx: reading migration_histories: boom"},
		},
		{
			name: "checksum tamper fails closed",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", "deadbeef", mxSeededAt)
				},
			},
			expected: expected{
				err: "migrationx: applied migration changed on disk: 100_a: recorded deadbeef, file " + mxChecksum(mxUpA),
			},
		},
		{
			name: "orphan applied row fails closed",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(999, "ghost", "cafe", mxSeededAt)
				},
			},
			expected: expected{err: "migrationx: applied migration missing from the filesystem: 999_ghost"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, state := fakeDB(t)
			if tt.input.seed != nil {
				tt.input.seed(state)
			}
			m, err := migrationx.New(db, mxFS(tt.input.files), migrationx.Config{Dialect: tt.input.dialect})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			state.mu.Lock()
			maps.Copy(state.failOn, tt.input.failOn)
			state.mu.Unlock()
			if tt.input.closeDB {
				_ = db.Close()
			}
			rows, err := m.Status(context.Background())
			if tt.expected.err == "" {
				if err != nil {
					t.Fatalf("Status() error = %v, want nil", err)
				}
			} else if err == nil || err.Error() != tt.expected.err {
				t.Fatalf("Status() error = %v, want %q", err, tt.expected.err)
			}
			if !slices.Equal(rows, tt.expected.rows) {
				t.Errorf("Status() = %+v, want %+v", rows, tt.expected.rows)
			}
		})
	}

	t.Run("records the clock as applied at", func(t *testing.T) {
		restore := migrationx.SetTimeNow(func() time.Time { return mxNow })
		defer restore()
		db, _ := fakeDB(t)
		m, err := migrationx.New(db, mxFS(map[string]string{"100_a.up.sql": mxUpA}),
			migrationx.Config{Dialect: migrationx.DialectSQLite})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		ctx := context.Background()
		if err := m.Up(ctx); err != nil {
			t.Fatalf("Up() error = %v", err)
		}
		rows, err := m.Status(ctx)
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		if len(rows) != 1 || !rows[0].Applied || !rows[0].AppliedAt.Equal(mxNow) {
			t.Errorf("Status() = %+v, want version 100 applied at %v", rows, mxNow)
		}
	})
}

func TestVersion(t *testing.T) {
	type expected struct {
		err     string
		version int64
	}
	tests := []struct {
		name     string
		input    mxInput
		expected expected
	}{
		{
			name: "reports the newest applied",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files: map[string]string{
					"100_a.up.sql": mxUpA, "200_b.up.sql": mxUpB, "300_c.up.sql": mxUpC,
				},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", mxChecksum(mxUpA), mxSeededAt)
					s.setApplied(300, "c", mxChecksum(mxUpC), mxSeededAt)
				},
			},
			expected: expected{version: 300},
		},
		{
			name: "zero when nothing applied",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
			},
			expected: expected{version: 0},
		},
		{
			name: "closed database",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				closeDB: true,
			},
			expected: expected{err: "migrationx: sql: database is closed"},
		},
		{
			name: "history read failure",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				failOn:  map[string]error{"SELECT version": errMxBoom},
			},
			expected: expected{err: "migrationx: reading migration_histories: boom"},
		},
		{
			name: "checksum tamper fails closed",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(100, "a", "deadbeef", mxSeededAt)
				},
			},
			expected: expected{err: "migrationx: applied migration changed on disk: 100_a: recorded deadbeef, file " + mxChecksum(mxUpA)},
		},
		{
			name: "orphan applied row fails closed",
			input: mxInput{
				dialect: migrationx.DialectSQLite,
				files:   map[string]string{"100_a.up.sql": mxUpA},
				seed: func(s *fakeState) {
					s.setApplied(999, "ghost", "cafe", mxSeededAt)
				},
			},
			expected: expected{err: "migrationx: applied migration missing from the filesystem: 999_ghost"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, state := fakeDB(t)
			if tt.input.seed != nil {
				tt.input.seed(state)
			}
			m, err := migrationx.New(db, mxFS(tt.input.files), migrationx.Config{Dialect: tt.input.dialect})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			state.mu.Lock()
			maps.Copy(state.failOn, tt.input.failOn)
			state.mu.Unlock()
			if tt.input.closeDB {
				_ = db.Close()
			}
			version, err := m.Version(context.Background())
			if tt.expected.err == "" {
				if err != nil {
					t.Fatalf("Version() error = %v, want nil", err)
				}
			} else if err == nil || err.Error() != tt.expected.err {
				t.Fatalf("Version() error = %v, want %q", err, tt.expected.err)
			}
			if version != tt.expected.version {
				t.Errorf("Version() = %d, want %d", version, tt.expected.version)
			}
		})
	}
}

// mxNewMigrator builds a Migrator over the fake driver for the fault
// subtests.
func mxNewMigrator(t *testing.T, db *sql.DB, dialect migrationx.Dialect, files map[string]string) *migrationx.Migrator {
	t.Helper()
	m, err := migrationx.New(db, mxFS(files), migrationx.Config{Dialect: dialect, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestFaultPaths drives the driver-fault branches the scenario tables
// cannot reach: a failing BeginTx, a mistyped history row, and a rows
// iteration error.
func TestFaultPaths(t *testing.T) {
	t.Run("mysql BeginTx failure surfaces from apply", func(t *testing.T) {
		db, state := fakeDB(t)
		state.mu.Lock()
		state.beginErr = errMxBoom
		state.mu.Unlock()
		m := mxNewMigrator(t, db, migrationx.DialectMySQL, map[string]string{"100_a.up.sql": mxUpA})
		err := m.Up(context.Background())
		if err == nil || !strings.Contains(err.Error(), "applying 100_a: boom") {
			t.Fatalf("err = %v, want BeginTx failure naming the migration", err)
		}
	})

	t.Run("mistyped history row fails the read", func(t *testing.T) {
		db, state := fakeDB(t)
		m := mxNewMigrator(t, db, migrationx.DialectSQLite, map[string]string{"100_a.up.sql": mxUpA})
		state.mu.Lock()
		state.history[100] = [4]string{"a", "x", "y"}
		state.badRow = true
		state.mu.Unlock()
		if _, err := m.Version(context.Background()); err == nil || !strings.Contains(err.Error(), "reading migration_histories") {
			t.Fatalf("err = %v, want scan failure reading the table", err)
		}
	})

	t.Run("rows iteration error fails the read", func(t *testing.T) {
		db, state := fakeDB(t)
		m := mxNewMigrator(t, db, migrationx.DialectSQLite, map[string]string{"100_a.up.sql": mxUpA})
		state.mu.Lock()
		state.rowsErr = errMxBoom
		state.mu.Unlock()
		if _, err := m.Version(context.Background()); err == nil || !strings.Contains(err.Error(), "reading migration_histories") {
			t.Fatalf("err = %v, want rows error reading the table", err)
		}
	})
}

// TestConcurrentRunners proves the design criterion: two racing Up
// runners, exactly one applies each migration — per dialect lock path.
func TestConcurrentRunners(t *testing.T) {
	for _, dialect := range []migrationx.Dialect{migrationx.DialectSQLite, migrationx.DialectMySQL} {
		t.Run(fmt.Sprintf("dialect %d", dialect), func(t *testing.T) {
			db, state := fakeDB(t)
			files := map[string]string{
				"100_a.up.sql": "CREATE TABLE race_t1 (x INTEGER);",
				"200_b.up.sql": "CREATE TABLE race_t2 (x INTEGER);",
			}
			m1 := mxNewMigrator(t, db, dialect, files)
			m2 := mxNewMigrator(t, db, dialect, files)
			state.mu.Lock()
			state.serialize = true
			state.mu.Unlock()

			errs := make(chan error, 2)
			for _, m := range []*migrationx.Migrator{m1, m2} {
				go func() { errs <- m.Up(context.Background()) }()
			}
			for range 2 {
				if err := <-errs; err != nil {
					t.Fatalf("concurrent Up: %v", err)
				}
			}
			for _, stmt := range []string{"CREATE TABLE race_t1", "CREATE TABLE race_t2"} {
				if n := state.executedContaining(stmt); n != 1 {
					t.Errorf("%q executed %d times, want exactly 1", stmt, n)
				}
			}
		})
	}
}

// TestNoTxConcurrency drives the raw-path probe: under the mysql
// run-long lock a second runner skips a finished no-transaction
// migration, and a failing probe aborts before any statement runs.
func TestNoTxConcurrency(t *testing.T) {
	files := map[string]string{"100_a.up.sql": "-- migrationx:no-transaction\nUPDATE backfill SET x = 1;"}

	t.Run("second runner skips, backfill runs once", func(t *testing.T) {
		db, state := fakeDB(t)
		m1 := mxNewMigrator(t, db, migrationx.DialectMySQL, files)
		m2 := mxNewMigrator(t, db, migrationx.DialectMySQL, files)
		state.mu.Lock()
		state.serialize = true
		state.mu.Unlock()
		errs := make(chan error, 2)
		for _, m := range []*migrationx.Migrator{m1, m2} {
			go func() { errs <- m.Up(context.Background()) }()
		}
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatalf("concurrent Up: %v", err)
			}
		}
		if n := state.executedContaining("UPDATE backfill"); n != 1 {
			t.Errorf("backfill executed %d times, want exactly 1", n)
		}
	})

	t.Run("failing probe inside a transaction rolls back", func(t *testing.T) {
		db, state := fakeDB(t)
		m := mxNewMigrator(t, db, migrationx.DialectSQLite, map[string]string{"100_a.up.sql": mxUpA})
		state.mu.Lock()
		state.failOn["SELECT 1 FROM"] = errMxBoom
		state.mu.Unlock()
		if err := m.Up(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want tx probe failure", err)
		}
		if n := state.executedContaining("ROLLBACK"); n != 1 {
			t.Errorf("ROLLBACK executed %d times, want 1", n)
		}
	})

	t.Run("failing probe aborts before statements", func(t *testing.T) {
		db, state := fakeDB(t)
		m := mxNewMigrator(t, db, migrationx.DialectSQLite, files)
		state.mu.Lock()
		state.failOn["SELECT 1 FROM"] = errMxBoom
		state.mu.Unlock()
		if err := m.Up(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want probe failure", err)
		}
		if n := state.executedContaining("UPDATE backfill"); n != 0 {
			t.Errorf("backfill executed %d times, want 0", n)
		}
	})

	t.Run("audit user falls back to the environment", func(t *testing.T) {
		restore := migrationx.SetCurrentUser(func() (*user.User, error) { return nil, errMxBoom })
		defer restore()
		t.Setenv("USER", "envfallback")
		db, _ := fakeDB(t)
		files := map[string]string{"100_a.up.sql": mxUpA, "100_a.down.sql": "DROP TABLE a1;"}
		m := mxNewMigrator(t, db, migrationx.DialectSQLite, files)
		if err := m.Up(context.Background()); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		lg := slog.New(slog.NewTextHandler(&buf, nil))
		m2, err := migrationx.New(db, mxFS(files), migrationx.Config{Dialect: migrationx.DialectSQLite, Log: lg})
		if err != nil {
			t.Fatal(err)
		}
		if err := m2.Down(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "os_user=envfallback") {
			t.Errorf("audit line missing env fallback user: %s", buf.String())
		}
	})
}

// TestDriverFaults drives the remaining failure branches: rollback
// delivery failure with connection poisoning, unlock failure logging,
// a dead pool at New, record-write failures in both execution modes,
// and the cancelled-context begin.
func TestDriverFaults(t *testing.T) {
	files := map[string]string{"100_a.up.sql": mxUpA}

	t.Run("rollback failure poisons the connection, statement error survives", func(t *testing.T) {
		db, state := fakeDB(t)
		m := mxNewMigrator(t, db, migrationx.DialectSQLite, files)
		state.mu.Lock()
		state.failOn["CREATE TABLE a1"] = errMxBoom
		state.failOn["ROLLBACK"] = errMxBoom
		state.mu.Unlock()
		err := m.Up(context.Background())
		if err == nil || !strings.Contains(err.Error(), "applying 100_a: boom") {
			t.Fatalf("err = %v, want the statement error", err)
		}
	})

	t.Run("unlock failure is logged, run still succeeds", func(t *testing.T) {
		db, state := fakeDB(t)
		var buf bytes.Buffer
		fsys := mxFS(files)
		m, err := migrationx.New(db, fsys, migrationx.Config{
			Dialect: migrationx.DialectMySQL,
			Log:     slog.New(slog.NewTextHandler(&buf, nil)),
		})
		if err != nil {
			t.Fatal(err)
		}
		state.mu.Lock()
		state.failOn["RELEASE_LOCK"] = errMxBoom
		state.mu.Unlock()
		if err := m.Up(context.Background()); err != nil {
			t.Fatalf("Up: %v", err)
		}
		if !strings.Contains(buf.String(), "migration lock release failed") {
			t.Errorf("log = %q, want unlock warning", buf.String())
		}
	})

	t.Run("closed pool fails New", func(t *testing.T) {
		db, _ := fakeDB(t)
		_ = db.Close()
		_, err := migrationx.New(db, mxFS(files), migrationx.Config{Dialect: migrationx.DialectSQLite})
		if err == nil || !strings.Contains(err.Error(), "migrationx:") {
			t.Fatalf("err = %v, want wrapped pool error", err)
		}
	})

	t.Run("history write failure rolls back a tx migration", func(t *testing.T) {
		db, state := fakeDB(t)
		m := mxNewMigrator(t, db, migrationx.DialectSQLite, files)
		state.mu.Lock()
		state.failOn["INSERT INTO"] = errMxBoom
		state.mu.Unlock()
		if err := m.Up(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want history write failure", err)
		}
		if n := len(state.appliedVersions()); n != 0 {
			t.Errorf("history rows = %d, want 0 after rollback", n)
		}
	})

	t.Run("history write failure surfaces from a no-transaction migration", func(t *testing.T) {
		db, state := fakeDB(t)
		noTx := map[string]string{"100_a.up.sql": "-- migrationx:no-transaction\nCREATE TABLE a1 (x INTEGER);"}
		m := mxNewMigrator(t, db, migrationx.DialectSQLite, noTx)
		state.mu.Lock()
		state.failOn["INSERT INTO"] = errMxBoom
		state.mu.Unlock()
		if err := m.Up(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want record failure", err)
		}
	})

	t.Run("cancelled context begin passes the error through unwrapped", func(t *testing.T) {
		db, _ := fakeDB(t)
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = migrationx.BeginSQLite(ctx, conn)
		if err == nil {
			t.Fatal("begin succeeded on a cancelled context")
		}
		if strings.Contains(err.Error(), "could not acquire the migration lock") {
			t.Fatalf("err = %v: a cancellation must not masquerade as lock contention", err)
		}
	})
}

// TestExecRawSkip drives the race-only no-transaction probe skip via the
// export seam.
func TestExecRawSkip(t *testing.T) {
	db, state := fakeDB(t)
	m := mxNewMigrator(t, db, migrationx.DialectSQLite, map[string]string{"100_a.up.sql": mxUpA})
	state.setApplied(100, "a", "irrelevant", "2026-08-14T00:00:00Z")
	skipped, err := migrationx.RunExecRaw(context.Background(), m, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Fatal("existing row should skip the raw execution")
	}
}

// TestNewCreateTableFailure covers the bootstrap DDL error.
func TestNewCreateTableFailure(t *testing.T) {
	db, state := fakeDB(t)
	state.mu.Lock()
	state.failOn["CREATE TABLE IF NOT EXISTS"] = errMxBoom
	state.mu.Unlock()
	_, err := migrationx.New(db, mxFS(map[string]string{"100_a.up.sql": mxUpA}), migrationx.Config{Dialect: migrationx.DialectSQLite})
	if err == nil || !strings.Contains(err.Error(), "creating migration_histories") {
		t.Fatalf("err = %v, want bootstrap failure", err)
	}
}

// TestApplySkip drives the race-only in-transaction probe skip.
func TestApplySkip(t *testing.T) {
	db, state := fakeDB(t)
	var buf bytes.Buffer
	m, err := migrationx.New(db, mxFS(map[string]string{"100_a.up.sql": mxUpA}), migrationx.Config{
		Dialect: migrationx.DialectSQLite,
		Log:     slog.New(slog.NewTextHandler(&buf, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	state.setApplied(100, "a", "raced", "2026-08-14T00:00:00Z")
	if err := migrationx.RunApply(context.Background(), m, 100); err != nil {
		t.Fatal(err)
	}
	if n := state.executedContaining("CREATE TABLE a1"); n != 0 {
		t.Errorf("statements executed %d times, want 0 on skip", n)
	}
	if !strings.Contains(buf.String(), "already applied by a concurrent runner") {
		t.Errorf("log = %q, want the skip line", buf.String())
	}
}

// TestNewLockTimeoutValidation pins the sub-second rejection.
func TestNewLockTimeoutValidation(t *testing.T) {
	db, _ := fakeDB(t)
	_, err := migrationx.New(db, mxFS(map[string]string{"100_a.up.sql": mxUpA}), migrationx.Config{
		Dialect:     migrationx.DialectSQLite,
		LockTimeout: 500 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "LockTimeout must be at least one second") {
		t.Fatalf("err = %v, want validation error", err)
	}
}
