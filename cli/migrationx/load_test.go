package migrationx_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Wigata-Intech/w-tools/cli/migrationx"
)

// ldNew builds a MapFS from files and runs New against a fake database
// with the sqlite dialect.
func ldNew(t *testing.T, files map[string]string) (*migrationx.Migrator, error) {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	db, _ := fakeDB(t)
	return migrationx.New(db, fsys, migrationx.Config{Dialect: migrationx.DialectSQLite})
}

// ldMigration is one expected Status row; nothing is applied in these
// tests, so version and name identify it fully.
type ldMigration struct {
	version int64
	name    string
}

// ldExpected is one load outcome: the Status rows in order, or a
// substring the New error must contain.
type ldExpected struct {
	migrations []ldMigration
	errMsg     string
}

var (
	errLdList     = errors.New("cannot list")
	errLdVanished = errors.New("file vanished")
)

// ldBrokenFS fails every Open: the directory-listing error path.
type ldBrokenFS struct{}

func (ldBrokenFS) Open(string) (fs.File, error) {
	return nil, errLdList
}

// ldVanishingFS lists the directory but fails to open any file in it:
// the error path between ReadDir and ReadFile.
type ldVanishingFS struct{ inner fstest.MapFS }

func (v ldVanishingFS) Open(name string) (fs.File, error) {
	if name == "." {
		return v.inner.Open(name)
	}
	return nil, errLdVanished
}

func TestLoadMigrations(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected ldExpected
	}{
		{
			name: "up and down pair loads",
			input: map[string]string{
				"1_init.up.sql":   "CREATE TABLE t (id INTEGER);\n",
				"1_init.down.sql": "DROP TABLE t;\n",
			},
			expected: ldExpected{migrations: []ldMigration{{1, "init"}}},
		},
		{
			name: "up without a down loads and sorts by version",
			input: map[string]string{
				"2_users.up.sql": "CREATE TABLE users (id INTEGER);\n",
				"1_init.up.sql":  "CREATE TABLE t (id INTEGER);\n",
			},
			expected: ldExpected{migrations: []ldMigration{{1, "init"}, {2, "users"}}},
		},
		{
			name: "down without an up",
			input: map[string]string{
				"1_init.down.sql": "DROP TABLE t;\n",
			},
			expected: ldExpected{errMsg: "migrationx: 1_init.down.sql: down migration without an up migration"},
		},
		{
			name: "down whose name mismatches its up",
			input: map[string]string{
				"1_a.up.sql":   "SELECT 1;\n",
				"1_b.down.sql": "SELECT 1;\n",
			},
			expected: ldExpected{errMsg: "migrationx: 1_b.down.sql: down migration without an up migration"},
		},
		{
			name: "duplicate version across two up files",
			input: map[string]string{
				"1_a.up.sql": "SELECT 1;\n",
				"1_b.up.sql": "SELECT 1;\n",
			},
			expected: ldExpected{errMsg: "duplicate migration version 1"},
		},
		{
			name: "stray readme",
			input: map[string]string{
				"README.md": "docs\n",
			},
			expected: ldExpected{errMsg: "migrationx: README.md: not a <unix-timestamp>_<name>.up.sql or .down.sql file"},
		},
		{
			name: "sql file without an up or down marker",
			input: map[string]string{
				"1_x.sql": "SELECT 1;\n",
			},
			expected: ldExpected{errMsg: "migrationx: 1_x.sql: not a <unix-timestamp>_<name>.up.sql or .down.sql file"},
		},
		{
			name: "filename without a name separator",
			input: map[string]string{
				"1.up.sql": "SELECT 1;\n",
			},
			expected: ldExpected{errMsg: "migrationx: 1.up.sql: not a <unix-timestamp>_<name>.up.sql or .down.sql file"},
		},
		{
			name: "non-numeric version",
			input: map[string]string{
				"x_1.up.sql": "SELECT 1;\n",
			},
			expected: ldExpected{errMsg: "migrationx: x_1.up.sql: migration version must be a positive integer"},
		},
		{
			name: "version zero",
			input: map[string]string{
				"0_a.up.sql": "SELECT 1;\n",
			},
			expected: ldExpected{errMsg: "migrationx: 0_a.up.sql: migration version must be a positive integer"},
		},
		{
			name: "negative version",
			input: map[string]string{
				"-1_a.up.sql": "SELECT 1;\n",
			},
			expected: ldExpected{errMsg: "migrationx: -1_a.up.sql: migration version must be a positive integer"},
		},
		{
			name: "uppercase name",
			input: map[string]string{
				"1_Init.up.sql": "SELECT 1;\n",
			},
			expected: ldExpected{errMsg: "migrationx: 1_Init.up.sql: migration name must be lowercase [a-z0-9_]"},
		},
		{
			name: "subdirectory is a stray entry",
			input: map[string]string{
				"sub/1_a.up.sql": "SELECT 1;\n",
			},
			expected: ldExpected{errMsg: "migrationx: sub: not a <unix-timestamp>_<name>.up.sql or .down.sql file"},
		},
		{
			name: "script that fails to parse aborts New naming the file",
			input: map[string]string{
				"1_bad.up.sql": "SELECT 'oops\n",
			},
			expected: ldExpected{errMsg: "migrationx: 1_bad.up.sql: unterminated quote or comment at end of file"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := ldNew(t, tt.input)
			if tt.expected.errMsg != "" {
				if err == nil {
					t.Fatalf("New succeeded, want error containing %q", tt.expected.errMsg)
				}
				if !strings.Contains(err.Error(), tt.expected.errMsg) {
					t.Fatalf("error = %q, want it to contain %q", err, tt.expected.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			status, err := m.Status(context.Background())
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			got := make([]ldMigration, 0, len(status))
			for _, row := range status {
				if row.Applied || !row.AppliedAt.IsZero() || row.OutOfOrder {
					t.Errorf("row %d_%s reports applied state on a fresh database", row.Version, row.Name)
				}
				got = append(got, ldMigration{version: row.Version, name: row.Name})
			}
			if !slices.Equal(got, tt.expected.migrations) {
				t.Errorf("migrations = %v, want %v", got, tt.expected.migrations)
			}
		})
	}

	t.Run("unreadable filesystem aborts New", func(t *testing.T) {
		db, _ := fakeDB(t)
		_, err := migrationx.New(db, ldBrokenFS{}, migrationx.Config{Dialect: migrationx.DialectSQLite})
		if err == nil {
			t.Fatal("New accepted an unreadable filesystem")
		}
		if !strings.Contains(err.Error(), "cannot list") {
			t.Fatalf("error = %q, want the Open failure", err)
		}
	})

	t.Run("file unreadable after listing aborts New", func(t *testing.T) {
		db, _ := fakeDB(t)
		fsys := ldVanishingFS{inner: fstest.MapFS{
			"1_a.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;\n")},
		}}
		_, err := migrationx.New(db, fsys, migrationx.Config{Dialect: migrationx.DialectSQLite})
		if err == nil {
			t.Fatal("New accepted an unreadable migration file")
		}
		if !strings.Contains(err.Error(), "file vanished") {
			t.Fatalf("error = %q, want the read failure", err)
		}
	})
}
