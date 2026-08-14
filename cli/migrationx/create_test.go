package migrationx_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/cli/migrationx"
)

// crFixedUnix is the frozen Create clock: every case mints this version.
const crFixedUnix = 1723600000

// crExpected is one Create outcome: the expected file basenames, or the
// exact error text ("" means success).
type crExpected struct {
	up     string
	down   string
	errMsg string
}

func TestCreate(t *testing.T) {
	restore := migrationx.SetTimeNow(func() time.Time { return time.Unix(crFixedUnix, 0) })
	defer restore()

	tests := []struct {
		name     string
		input    string
		expected crExpected
	}{
		{
			name:  "creates the timestamped pair",
			input: "add_users",
			expected: crExpected{
				up:   "1723600000_add_users.up.sql",
				down: "1723600000_add_users.down.sql",
			},
		},
		{
			name:     "uppercase name rejected",
			input:    "AddUsers",
			expected: crExpected{errMsg: `migrationx: "AddUsers": migration name must be lowercase [a-z0-9_]`},
		},
		{
			name:     "empty name rejected",
			input:    "",
			expected: crExpected{errMsg: `migrationx: "": migration name must be lowercase [a-z0-9_]`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			up, down, err := migrationx.Create(dir, tt.input)
			if tt.expected.errMsg != "" {
				if err == nil {
					t.Fatalf("Create succeeded, want error %q", tt.expected.errMsg)
				}
				if err.Error() != tt.expected.errMsg {
					t.Fatalf("error = %q, want %q", err, tt.expected.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if want := filepath.Join(dir, tt.expected.up); up != want {
				t.Errorf("up path = %q, want %q", up, want)
			}
			if want := filepath.Join(dir, tt.expected.down); down != want {
				t.Errorf("down path = %q, want %q", down, want)
			}
			header := "-- " + tt.input + "\n"
			for _, path := range []string{up, down} {
				data, err := os.ReadFile(path) // #nosec G304 -- reading back the file Create just wrote in t.TempDir
				if err != nil {
					t.Fatalf("reading %s: %v", path, err)
				}
				if string(data) != header {
					t.Errorf("%s content = %q, want %q", path, data, header)
				}
			}
		})
	}

	t.Run("second call in the same second errors", func(t *testing.T) {
		dir := t.TempDir()
		up, _, err := migrationx.Create(dir, "add_users")
		if err != nil {
			t.Fatalf("first Create: %v", err)
		}
		_, _, err = migrationx.Create(dir, "add_users")
		if err == nil {
			t.Fatal("second Create in the same second succeeded")
		}
		if want := "migrationx: migration files already exist: " + up; err.Error() != want {
			t.Fatalf("error = %q, want %q", err, want)
		}
	})

	t.Run("created files load through New", func(t *testing.T) {
		dir := t.TempDir()
		if _, _, err := migrationx.Create(dir, "roundtrip"); err != nil {
			t.Fatalf("Create: %v", err)
		}
		db, _ := fakeDB(t)
		m, err := migrationx.New(db, os.DirFS(dir), migrationx.Config{Dialect: migrationx.DialectSQLite})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		status, err := m.Status(context.Background())
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if len(status) != 1 || status[0].Version != crFixedUnix || status[0].Name != "roundtrip" {
			t.Fatalf("status = %+v, want one pending %d_roundtrip", status, int64(crFixedUnix))
		}
	})

	t.Run("unwritable directory fails the up write", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o555); err != nil { // #nosec G302 -- read-only dir provokes the WriteFile error branch
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // #nosec G302 -- restore so t.TempDir cleanup can remove it
		_, _, err := migrationx.Create(dir, "x")
		if err == nil {
			t.Fatal("Create succeeded in an unwritable directory")
		}
		if !strings.Contains(err.Error(), "1723600000_x.up.sql") {
			t.Fatalf("error = %q, want it to name the up file", err)
		}
	})

	t.Run("dangling symlink at the down path fails the down write", func(t *testing.T) {
		dir := t.TempDir()
		missing := filepath.Join(t.TempDir(), "gone", "target")
		if err := os.Symlink(missing, filepath.Join(dir, "1723600000_x.down.sql")); err != nil {
			t.Fatal(err)
		}
		_, _, err := migrationx.Create(dir, "x")
		if err == nil {
			t.Fatal("Create succeeded through a dangling down symlink")
		}
		if !strings.Contains(err.Error(), "1723600000_x.down.sql") {
			t.Fatalf("error = %q, want it to name the down file", err)
		}
	})
}

// TestCreateDirectory covers the directory bootstrap Create performs.
func TestCreateDirectory(t *testing.T) {
	t.Run("unwritable parent fails directory creation", func(t *testing.T) {
		parent := t.TempDir()
		if err := os.Chmod(parent, 0o555); err != nil { // #nosec G302 -- read-only parent provokes the MkdirAll error branch
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(parent, 0o755) }) // #nosec G302 -- restore for cleanup
		if _, _, err := migrationx.Create(filepath.Join(parent, "sub"), "x"); err == nil {
			t.Fatal("Create succeeded under a read-only parent")
		}
	})

	t.Run("creates the directory when absent", func(t *testing.T) {
		restore := migrationx.SetTimeNow(func() time.Time { return time.Unix(1723600001, 0) })
		defer restore()
		dir := filepath.Join(t.TempDir(), "nested", "migrations")
		up, _, err := migrationx.Create(dir, "first")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := os.Stat(up); err != nil {
			t.Fatalf("up file missing: %v", err)
		}
	})
}
