package migrationx_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Wigata-Intech/w-tools/cli/migrationx"
)

// mgExpected is one Merge outcome: the root file names of the merged
// view in order, or the sentinel and message the error must carry.
type mgExpected struct {
	files  []string
	errIs  error
	errMsg string
}

var errMgEntryCount = errors.New("wrong entry count")

// mgNew runs New with the sqlite dialect over fsys against a fresh fake
// database.
func mgNew(t *testing.T, fsys fs.FS) (*migrationx.Migrator, *fakeState, error) {
	t.Helper()
	db, state := fakeDB(t)
	m, err := migrationx.New(db, fsys, migrationx.Config{Dialect: migrationx.DialectSQLite})
	return m, state, err
}

// mgMerge runs Merge and fails the test on error.
func mgMerge(t *testing.T, sources ...fs.FS) fs.FS {
	t.Helper()
	merged, err := migrationx.Merge(sources...)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	return merged
}

// mgCheck runs Merge over sources and checks the outcome: the error, or
// the merged root listing and fstest.TestFS over it.
func mgCheck(t *testing.T, sources []fs.FS, expected mgExpected) {
	t.Helper()
	merged, err := migrationx.Merge(sources...)
	if expected.errMsg != "" {
		if err == nil {
			t.Fatalf("Merge succeeded, want error %q", expected.errMsg)
		}
		if err.Error() != expected.errMsg {
			t.Fatalf("error = %q, want %q", err, expected.errMsg)
		}
		if expected.errIs != nil && !errors.Is(err, expected.errIs) {
			t.Fatalf("error = %v, want it to wrap %v", err, expected.errIs)
		}
		return
	}
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	entries, err := fs.ReadDir(merged, ".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if !slices.Equal(got, expected.files) {
		t.Errorf("files = %v, want %v", got, expected.files)
	}
	if err := fstest.TestFS(merged, expected.files...); err != nil {
		t.Errorf("fstest.TestFS: %v", err)
	}
}

func TestMerge(t *testing.T) {
	tests := []struct {
		name     string
		input    []fs.FS
		expected mgExpected
	}{
		{
			name:     "no sources merge into an empty filesystem",
			input:    nil,
			expected: mgExpected{files: []string{}},
		},
		{
			name: "one source passes its migration files through",
			input: []fs.FS{
				mxFS(map[string]string{"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA}),
			},
			expected: mgExpected{files: []string{"100_a.down.sql", "100_a.up.sql"}},
		},
		{
			name: "two sources merge sorted by name",
			input: []fs.FS{
				mxFS(map[string]string{"200_b.up.sql": mxUpB, "200_b.down.sql": mxDownB}),
				mxFS(map[string]string{"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA}),
			},
			expected: mgExpected{files: []string{"100_a.down.sql", "100_a.up.sql", "200_b.down.sql", "200_b.up.sql"}},
		},
		{
			name: "directories and dot-files in every source are skipped",
			input: []fs.FS{
				mxFS(map[string]string{"100_a.up.sql": mxUpA, ".gitkeep": "", "sub/300_c.up.sql": mxUpC}),
				mxFS(map[string]string{"200_b.up.sql": mxUpB, ".gitkeep": "", ".DS_Store": "x"}),
			},
			expected: mgExpected{files: []string{"100_a.up.sql", "200_b.up.sql"}},
		},
		{
			name: "any other non-migration file is carried through for New to reject",
			input: []fs.FS{
				mxFS(map[string]string{"100_a.up.sq": mxUpA, "README.md": "docs\n"}),
			},
			expected: mgExpected{files: []string{"100_a.up.sq", "README.md"}},
		},
		{
			name: "nil source",
			input: []fs.FS{
				mxFS(map[string]string{"100_a.up.sql": mxUpA}),
				nil,
			},
			expected: mgExpected{errMsg: "migrationx: migration source must not be nil: sources[1]"},
		},
		{
			name: "source whose root cannot be listed",
			input: []fs.FS{
				mxFS(map[string]string{"100_a.up.sql": mxUpA}),
				ldBrokenFS{},
			},
			expected: mgExpected{errIs: errLdList, errMsg: "migrationx: sources[1]: cannot list"},
		},
		{
			name: "same file name in two sources",
			input: []fs.FS{
				mxFS(map[string]string{"100_a.up.sql": mxUpA}),
				mxFS(map[string]string{"200_b.up.sql": mxUpB}),
				mxFS(map[string]string{"100_a.up.sql": mxUpA}),
			},
			expected: mgExpected{
				errIs:  migrationx.ErrDuplicateFile,
				errMsg: "migrationx: duplicate migration file: 100_a.up.sql in sources[0] and sources[2]",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgCheck(t, tt.input, tt.expected)
		})
	}

	t.Run("merged sources run as one timeline", mgOneTimeline)
	t.Run("checksums equal the single-source equivalent", mgChecksums)
	t.Run("same version under two names is rejected by New", mgDuplicateVersion)
	t.Run("mistyped migration file is rejected by New", mgMistyped)
	t.Run("paths outside the flat root do not open", mgOutsideRoot)
	t.Run("root directory refuses Read and reports directory info", mgRootDir)
	t.Run("concurrent opens of one merged view", mgConcurrent)
}

// mgOneTimeline runs two merged sources through Status, Up, and Down.
func mgOneTimeline(t *testing.T) {
	merged := mgMerge(t,
		mxFS(map[string]string{"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA}),
		mxFS(map[string]string{"200_b.up.sql": mxUpB, "200_b.down.sql": mxDownB, ".gitkeep": ""}),
	)
	m, state, err := mgNew(t, merged)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	status, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := []migrationx.Migration{{Version: 100, Name: "a"}, {Version: 200, Name: "b"}}
	if !slices.Equal(status, want) {
		t.Errorf("Status = %+v, want %+v", status, want)
	}
	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	mxAssertApplied(t, state, []int64{100, 200})
	mxAssertOrder(t, state, []string{"CREATE TABLE a1", "CREATE TABLE a2", "CREATE TABLE b1"})
	if err := m.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	mxAssertApplied(t, state, []int64{100})
	mxAssertCounts(t, state, map[string]int{"DROP TABLE b1": 1})
}

// mgChecksums proves merged checksums and Status equal the single-source ones.
func mgChecksums(t *testing.T) {
	files := map[string]string{
		"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA,
		"200_b.up.sql": mxUpB, "200_b.down.sql": mxDownB,
	}
	single, singleState, err := mgNew(t, mxFS(files))
	if err != nil {
		t.Fatalf("New single: %v", err)
	}
	merged, mergedState, err := mgNew(t, mgMerge(t,
		mxFS(map[string]string{"100_a.up.sql": mxUpA, "100_a.down.sql": mxDownA}),
		mxFS(map[string]string{"200_b.up.sql": mxUpB, "200_b.down.sql": mxDownB}),
	))
	if err != nil {
		t.Fatalf("New merged: %v", err)
	}
	for _, m := range []*migrationx.Migrator{single, merged} {
		if err := m.Up(context.Background()); err != nil {
			t.Fatalf("Up: %v", err)
		}
	}
	for _, v := range []int64{100, 200} {
		got, want := mergedState.history[v][1], singleState.history[v][1]
		if got != want || want == "" {
			t.Errorf("checksum %d = %q, want %q", v, got, want)
		}
	}
	if got := mergedState.history[100][1]; got != mxChecksum(mxUpA) {
		t.Errorf("checksum 100 = %q, want the up file's sha256", got)
	}
	singleRows, err := single.Status(context.Background())
	if err != nil {
		t.Fatalf("Status single: %v", err)
	}
	mergedRows, err := merged.Status(context.Background())
	if err != nil {
		t.Fatalf("Status merged: %v", err)
	}
	if !slices.Equal(mergedRows, singleRows) {
		t.Errorf("merged Status = %+v, want %+v", mergedRows, singleRows)
	}
}

// mgDuplicateVersion proves New still rejects one version under two names.
func mgDuplicateVersion(t *testing.T) {
	merged := mgMerge(t,
		mxFS(map[string]string{"100_a.up.sql": mxUpA}),
		mxFS(map[string]string{"100_b.up.sql": mxUpB}),
	)
	_, _, err := mgNew(t, merged)
	if err == nil || !strings.Contains(err.Error(), "duplicate migration version 100") {
		t.Fatalf("New error = %v, want duplicate migration version 100", err)
	}
}

// mgMistyped proves a mistyped file passes Merge and fails New.
func mgMistyped(t *testing.T) {
	merged := mgMerge(t,
		mxFS(map[string]string{"100_a.up.sql": mxUpA}),
		mxFS(map[string]string{"200_b.up.sq": mxUpB, ".gitkeep": ""}),
	)
	_, _, err := mgNew(t, merged)
	want := "migrationx: 200_b.up.sq: not a <unix-timestamp>_<name>.up.sql or .down.sql file"
	if err == nil || err.Error() != want {
		t.Fatalf("New error = %v, want %q", err, want)
	}
}

// mgOutsideRoot proves only root-level migration files open.
func mgOutsideRoot(t *testing.T) {
	merged := mgMerge(t, mxFS(map[string]string{".gitkeep": "", "sub/100_a.up.sql": mxUpA}))
	for name, want := range map[string]error{
		"../100_a.up.sql":  fs.ErrInvalid,
		"sub/100_a.up.sql": fs.ErrNotExist,
		".gitkeep":         fs.ErrNotExist,
		"sub":              fs.ErrNotExist,
	} {
		if _, err := merged.Open(name); !errors.Is(err, want) {
			t.Errorf("Open(%q) error = %v, want %v", name, err, want)
		}
	}
}

// mgRootDir exercises the root directory file directly.
func mgRootDir(t *testing.T) {
	merged := mgMerge(t, mxFS(map[string]string{"100_a.up.sql": mxUpA}))
	root, err := merged.Open(".")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = root.Close() }()
	if _, err := root.Read(make([]byte, 1)); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Read error = %v, want %v", err, fs.ErrInvalid)
	}
	info, err := root.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Name() != "." || !info.IsDir() || info.Mode() != fs.ModeDir|0o555 || info.Size() != 0 ||
		!info.ModTime().IsZero() || info.Sys() != nil {
		t.Errorf("Stat = %s, want the read-only root directory", fs.FormatFileInfo(info))
	}
	dir, ok := root.(fs.ReadDirFile)
	if !ok {
		t.Fatal("root is not an fs.ReadDirFile")
	}
	if entries, err := dir.ReadDir(1); err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir(1) = %v, %v, want one entry", entries, err)
	}
	if _, err := dir.ReadDir(1); !errors.Is(err, io.EOF) {
		t.Errorf("exhausted ReadDir(1) error = %v, want io.EOF", err)
	}
}

// mgConcurrent reads one merged view from several goroutines.
func mgConcurrent(t *testing.T) {
	merged := mgMerge(t,
		mxFS(map[string]string{"100_a.up.sql": mxUpA}),
		mxFS(map[string]string{"200_b.up.sql": mxUpB}),
	)
	done := make(chan error)
	for range 8 {
		go func() {
			entries, err := fs.ReadDir(merged, ".")
			if err == nil && len(entries) != 2 {
				err = errMgEntryCount
			}
			if err == nil {
				_, err = fs.ReadFile(merged, "200_b.up.sql")
			}
			done <- err
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Errorf("concurrent read: %v", err)
		}
	}
}
