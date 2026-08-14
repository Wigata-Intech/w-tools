package migrationx_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Wigata-Intech/w-tools/cli"
	"github.com/Wigata-Intech/w-tools/cli/migrationx"
)

// cmdRoot wires the migrate command over a fake-driver Migrator.
func cmdRoot(t *testing.T, files map[string]string) (*cli.Command, *fakeState) {
	t.Helper()
	db, state := fakeDB(t)
	fsys := fstest.MapFS{}
	for name, src := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(src)}
	}
	open := func(_ context.Context, allowOOO bool) (*migrationx.Migrator, error) {
		return migrationx.New(db, fsys, migrationx.Config{ //nolint:contextcheck // New has no ctx parameter; bootstrap uses its own
			Dialect:         migrationx.DialectSQLite,
			AllowOutOfOrder: allowOOO,
			Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}
	root := &cli.Command{Name: "app", Commands: []*cli.Command{migrationx.Command(open)}}
	return root, state
}

// cmdExec runs root.Execute with the given argv via the public API.
func cmdExec(t *testing.T, root *cli.Command, args []string) int {
	t.Helper()
	prev := os.Args
	os.Args = append([]string{"app"}, args...)
	t.Cleanup(func() { os.Args = prev })
	return root.Execute(context.Background())
}

// cmdQuiet routes the process streams to /dev/null for the test and
// restores them afterwards — the migrate verbs print to os.Stdout.
func cmdQuiet(t *testing.T) {
	t.Helper()
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	prevOut, prevErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = null, null
	t.Cleanup(func() {
		os.Stdout, os.Stderr = prevOut, prevErr
		_ = null.Close()
	})
}

func TestCommand(t *testing.T) {
	files := map[string]string{
		"100_a.up.sql":   "CREATE TABLE a1 (x INTEGER);",
		"100_a.down.sql": "DROP TABLE a1;",
		"200_b.up.sql":   "CREATE TABLE b1 (x INTEGER);",
		"200_b.down.sql": "DROP TABLE b1;",
	}
	tests := []struct {
		name     string
		input    []string
		expected string // statement executed exactly once; "" checks exit only
		code     int
	}{
		{name: "up applies all", input: []string{"migrate", "up"}, expected: "CREATE TABLE b1"},
		{name: "up one", input: []string{"migrate", "up", "-one"}, expected: "CREATE TABLE a1"},
		{name: "up to", input: []string{"migrate", "up", "-to", "100"}, expected: "CREATE TABLE a1"},
		{name: "up one with to conflicts", input: []string{"migrate", "up", "-one", "-to", "100"}, code: 1},
		{name: "down all with to conflicts", input: []string{"migrate", "down", "-all", "-to", "100"}, code: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdQuiet(t)
			root, state := cmdRoot(t, files)
			if code := cmdExec(t, root, tt.input); code != tt.code {
				t.Fatalf("exit = %d, want %d", code, tt.code)
			}
			if tt.expected != "" && state.executedContaining(tt.expected) != 1 {
				t.Errorf("%q not executed exactly once", tt.expected)
			}
		})
	}

	t.Run("up down downall status round trip incl allow-out-of-order", func(t *testing.T) {
		cmdQuiet(t)
		root, state := cmdRoot(t, files)
		steps := []struct {
			args []string
			code int
		}{
			{[]string{"migrate", "up"}, 0},
			{[]string{"migrate", "down"}, 0},
			{[]string{"migrate", "down", "-all"}, 0},
			{[]string{"migrate", "down", "-to", "0"}, 1}, // nothing applied
			{[]string{"migrate", "up", "-allow-out-of-order"}, 0},
			{[]string{"migrate", "status"}, 0},
		}
		for _, s := range steps {
			if code := cmdExec(t, root, s.args); code != s.code {
				t.Fatalf("%v exit = %d, want %d", s.args, code, s.code)
			}
		}
		if n := state.executedContaining("DROP TABLE b1"); n != 1 {
			t.Errorf("down executed %d times, want 1", n)
		}
	})

	t.Run("create scaffolds and validates arity", func(t *testing.T) {
		cmdQuiet(t)
		dir := t.TempDir()
		root, _ := cmdRoot(t, files)
		if code := cmdExec(t, root, []string{"migrate", "create", "-dir", dir, "add_users"}); code != 0 {
			t.Fatal("create failed")
		}
		if m, _ := filepath.Glob(filepath.Join(dir, "*_add_users.up.sql")); len(m) != 1 {
			t.Fatalf("scaffold missing: %v", m)
		}
		if code := cmdExec(t, root, []string{"migrate", "create", "-dir", dir}); code != 1 {
			t.Fatal("create without a name should fail")
		}
	})

	t.Run("open error propagates", func(t *testing.T) {
		errFile := filepath.Join(t.TempDir(), "stderr")
		f, err := os.Create(errFile) // #nosec G304 -- path inside t.TempDir
		if err != nil {
			t.Fatal(err)
		}
		prev := os.Stderr
		os.Stderr = f
		t.Cleanup(func() { os.Stderr = prev; _ = f.Close() })

		failing := &cli.Command{Name: "app", Commands: []*cli.Command{
			migrationx.Command(func(context.Context, bool) (*migrationx.Migrator, error) {
				return nil, errMxBoom
			}),
		}}
		if code := cmdExec(t, failing, []string{"migrate", "up"}); code != 1 {
			t.Fatal("open error should fail the run")
		}
		got, _ := os.ReadFile(errFile) // #nosec G304 -- path inside t.TempDir
		if !strings.Contains(string(got), "boom") {
			t.Errorf("stderr = %q", got)
		}
	})
}

// TestCommandOutput pins the status rendering and the remaining verb
// error paths through captured process streams.
func TestCommandOutput(t *testing.T) {
	files := map[string]string{
		"100_a.up.sql":   "CREATE TABLE a1 (x INTEGER);",
		"100_a.down.sql": "DROP TABLE a1;",
		"300_c.up.sql":   "CREATE TABLE c1 (x INTEGER);",
	}

	t.Run("status renders applied, pending, out-of-order, and orphaned rows", func(t *testing.T) {
		outFile := filepath.Join(t.TempDir(), "stdout")
		f, err := os.Create(outFile) // #nosec G304 -- path inside t.TempDir
		if err != nil {
			t.Fatal(err)
		}
		prev := os.Stdout
		os.Stdout = f
		t.Cleanup(func() { os.Stdout = prev; _ = f.Close() })

		withB := map[string]string{"200_b.up.sql": "CREATE TABLE b1 (x INTEGER);"}
		maps.Copy(withB, files)
		root, state := cmdRoot(t, withB)
		sum := sha256.Sum256([]byte(withB["300_c.up.sql"]))
		state.setApplied(300, "c", hex.EncodeToString(sum[:]), "2026-08-14T00:00:00Z")
		state.setApplied(999, "ghost", "cafe", "2026-08-14T00:00:00Z")
		if code := cmdExec(t, root, []string{"migrate", "status"}); code != 0 {
			t.Fatal("status failed")
		}
		got, _ := os.ReadFile(outFile) // #nosec G304 -- path inside t.TempDir
		out := string(got)
		if !strings.Contains(out, "✓ 300_c 2026-08-14 00:00:00") ||
			!strings.Contains(out, "  100_a out of order") ||
			!strings.Contains(out, "  200_b out of order") ||
			!strings.Contains(out, "✓ 999_ghost orphaned: file missing") {
			t.Errorf("status output = %q", out)
		}
	})

	t.Run("status failure propagates", func(t *testing.T) {
		cmdQuiet(t)
		root, state := cmdRoot(t, files)
		state.mu.Lock()
		state.failOn["FROM migration_histories"] = errMxBoom
		state.mu.Unlock()
		if code := cmdExec(t, root, []string{"migrate", "status"}); code != 1 {
			t.Fatal("status should fail")
		}
	})

	t.Run("down all rolls everything back", func(t *testing.T) {
		cmdQuiet(t)
		all := map[string]string{
			"100_a.up.sql": "CREATE TABLE a1 (x INTEGER);", "100_a.down.sql": "DROP TABLE a1;",
			"300_c.up.sql": "CREATE TABLE c1 (x INTEGER);", "300_c.down.sql": "DROP TABLE c1;",
		}
		root, state := cmdRoot(t, all)
		if code := cmdExec(t, root, []string{"migrate", "up"}); code != 0 {
			t.Fatal("up failed")
		}
		if code := cmdExec(t, root, []string{"migrate", "down", "-to", "100"}); code != 0 {
			t.Fatal("down -to failed")
		}
		if code := cmdExec(t, root, []string{"migrate", "down", "-all"}); code != 0 {
			t.Fatal("down -all failed")
		}
		for _, drop := range []string{"DROP TABLE a1", "DROP TABLE c1"} {
			if n := state.executedContaining(drop); n != 1 {
				t.Errorf("%q executed %d times, want 1", drop, n)
			}
		}
	})

	t.Run("create rejects a bad name through the verb", func(t *testing.T) {
		cmdQuiet(t)
		root, _ := cmdRoot(t, files)
		if code := cmdExec(t, root, []string{"migrate", "create", "-dir", t.TempDir(), "BadName"}); code != 1 {
			t.Fatal("create should reject an uppercase name")
		}
	})
}
