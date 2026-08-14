package migrationx_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeState is one in-memory database: history rows, an execution log,
// scripted failures, and a lock flag. It backs a database/sql driver so
// the engine runs its real SQL paths with no real database.
type fakeState struct {
	mu          sync.Mutex
	history     map[int64][4]string // version -> name, checksum, applied_at
	executed    []string            // every statement, in order
	failOn      map[string]error    // substring of statement -> injected error
	lockBusy    bool                // GET_LOCK returns 0
	beginErr    error               // injected from BeginTx
	rowsErr     error               // returned by history rows after the last row
	badRow      bool                // history rows yield a wrongly typed version
	journal     map[int64][4]string // pending inserts inside an open tx
	deletes     map[int64]bool      // pending deletes inside an open tx
	inTx        bool
	dirty       map[int64]bool // version -> dirty flag (mysql only in practice)
	badDirtyRow bool           // dirty-version rows yield a wrongly typed version

	// serialize makes the fake locks real: BEGIN/GET_LOCK block on
	// writerMu until COMMIT/ROLLBACK/RELEASE_LOCK. Set before any
	// concurrent runner starts.
	serialize bool
	writerMu  sync.Mutex
}

func (s *fakeState) log(q string) {
	s.executed = append(s.executed, q)
}

// executedContaining reports how many logged statements contain sub.
func (s *fakeState) executedContaining(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, q := range s.executed {
		if strings.Contains(q, sub) {
			n++
		}
	}
	return n
}

// appliedVersions returns the committed history versions, unordered.
func (s *fakeState) appliedVersions() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, 0, len(s.history))
	for v := range s.history {
		out = append(out, v)
	}
	return out
}

// setApplied seeds a committed history row.
func (s *fakeState) setApplied(version int64, name, checksum, appliedAt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[version] = [4]string{name, checksum, appliedAt}
}

// setDirty seeds a dirty flag for a version without touching history —
// scenarios that start already dirty from a previous failed run.
func (s *fakeState) setDirty(version int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty[version] = true
}

//nolint:gochecknoglobals // one process-wide counter mints unique driver names; sql.Register allows no unregister
var fakeSeq atomic.Int64

// fakeDB registers a fresh fake driver instance and opens it.
func fakeDB(tb testing.TB) (*sql.DB, *fakeState) {
	tb.Helper()
	state := &fakeState{
		history: map[int64][4]string{},
		failOn:  map[string]error{},
		dirty:   map[int64]bool{},
	}
	name := fmt.Sprintf("migrationx-fake-%d", fakeSeq.Add(1))
	sql.Register(name, fakeDriver{state: state})
	db, err := sql.Open(name, "fake")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	return db, state
}

type fakeDriver struct{ state *fakeState }

func (d fakeDriver) Open(string) (driver.Conn, error) {
	return &fakeConn{state: d.state}, nil
}

type fakeConn struct{ state *fakeState }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errFakeUnsupported }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return nil, errFakeUnsupported }

var errFakeUnsupported = errors.New("fake driver: unsupported path")

// BeginTx serves the mysql dialect's conn.BeginTx.
func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	s := c.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	s.beginLocked()
	return fakeTx{state: s}, nil
}

func (s *fakeState) beginLocked() {
	s.inTx = true
	s.journal = map[int64][4]string{}
	s.deletes = map[int64]bool{}
}

func (s *fakeState) commitLocked() {
	maps.Copy(s.history, s.journal)
	for v := range s.deletes {
		delete(s.history, v)
		delete(s.dirty, v)
	}
	s.inTx, s.journal, s.deletes = false, nil, nil
}

func (s *fakeState) rollbackLocked() {
	s.inTx, s.journal, s.deletes = false, nil, nil
}

type fakeTx struct{ state *fakeState }

func (t fakeTx) Commit() error {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	t.state.commitLocked()
	return nil
}

func (t fakeTx) Rollback() error {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	t.state.rollbackLocked()
	return nil
}

// ExecContext handles every statement the engine issues.
func (c *fakeConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil { // real drivers refuse a dead context
		return nil, err
	}
	s := c.state
	if s.serialize && strings.HasPrefix(query, "BEGIN") {
		s.writerMu.Lock()
	}
	if s.serialize && (query == "COMMIT" || query == "ROLLBACK" || strings.Contains(query, "RELEASE_LOCK")) {
		defer s.writerMu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log(query)

	for sub, err := range s.failOn {
		if strings.Contains(query, sub) {
			return nil, err
		}
	}

	switch {
	case strings.HasPrefix(query, "BEGIN"):
		s.beginLocked()
	case query == "COMMIT":
		s.commitLocked()
	case query == "ROLLBACK":
		s.rollbackLocked()
	case strings.HasPrefix(query, "INSERT INTO"):
		version, _ := args[0].Value.(int64)
		name, _ := args[1].Value.(string)
		checksum, _ := args[2].Value.(string)
		appliedAt, _ := args[3].Value.(string)
		row := [4]string{name, checksum, appliedAt}
		if s.inTx {
			s.journal[version] = row
		} else {
			s.history[version] = row
		}
		if strings.Contains(query, "dirty") {
			s.dirty[version] = true
		}
	case strings.HasPrefix(query, "UPDATE") && strings.Contains(query, "dirty"):
		value, _ := args[0].Value.(int64)
		version, _ := args[1].Value.(int64)
		s.dirty[version] = value != 0
	case strings.HasPrefix(query, "DELETE FROM"):
		version, _ := args[0].Value.(int64)
		if s.inTx {
			s.deletes[version] = true
		} else {
			delete(s.history, version)
			delete(s.dirty, version)
		}
	}
	return driver.RowsAffected(1), nil
}

// QueryContext serves the history read and GET_LOCK.
func (c *fakeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := c.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log(query)

	for sub, err := range s.failOn {
		if strings.Contains(query, sub) {
			return nil, err
		}
	}

	if strings.HasPrefix(query, "SELECT 1 FROM") {
		version, _ := args[0].Value.(int64)
		_, committed := c.state.history[version]
		_, journaled := c.state.journal[version]
		_, doomed := c.state.deletes[version]
		rows := &fakeRows{cols: []string{"one"}}
		if (committed || journaled) && !doomed {
			rows.rows = [][]driver.Value{{int64(1)}}
		}
		return rows, nil
	}

	if strings.HasPrefix(query, "SELECT version FROM") && strings.Contains(query, "dirty") {
		rows := &fakeRows{cols: []string{"version"}}
		for v, dirty := range s.dirty {
			if !dirty {
				continue
			}
			value := any(v)
			if s.badDirtyRow {
				value = "not-a-number"
			}
			rows.rows = append(rows.rows, []driver.Value{value})
		}
		return rows, nil
	}

	if strings.Contains(query, "GET_LOCK") {
		if s.serialize {
			s.mu.Unlock()
			s.writerMu.Lock()
			s.mu.Lock()
		}
		got := int64(1)
		if s.lockBusy {
			got = 0
		}
		return &fakeRows{cols: []string{"got"}, rows: [][]driver.Value{{got}}}, nil
	}

	rows := &fakeRows{cols: []string{"version", "name", "checksum", "applied_at"}, err: s.rowsErr}
	for v, row := range s.history {
		version := any(v)
		if s.badRow {
			version = "not-a-number"
		}
		rows.rows = append(rows.rows, []driver.Value{version, row[0], row[1], row[2]})
	}
	return rows, nil
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	next int
	err  error
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.next >= len(r.rows) {
		if r.err != nil {
			err := r.err
			r.err = nil
			return err
		}
		return io.EOF
	}
	copy(dest, r.rows[r.next])
	r.next++
	return nil
}
