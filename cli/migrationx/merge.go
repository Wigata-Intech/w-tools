package migrationx

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"
)

// ErrDuplicateFile reports that two sources passed to Merge carry a file
// with the same name.
var ErrDuplicateFile = errors.New("migrationx: duplicate migration file")

var errNilSource = errors.New("migrationx: migration source must not be nil")

// Merge combines several migration sources — a service's own directory
// plus the embedded migrations of the libraries it uses — into one
// flat filesystem for New, so they share one ordered timeline and one
// history table.
//
// From each source's root, Merge skips directories and dot-files (names
// starting with "."), such as a .gitkeep that keeps a //go:embed pattern
// valid. Every other root entry is carried into the merged view
// unchanged, so New still rejects anything that is not a well-formed
// migration file, exactly as it does for a single source.
//
// The same file name in two sources is an error wrapping
// ErrDuplicateFile; the same version under two different names passes
// Merge and is rejected by New. A nil source or a source whose root
// cannot be listed is an error. Merge with no sources returns an empty
// filesystem.
//
// The file listing is taken when Merge runs; opening a file reads it
// from the source that owns it, so its bytes — and the checksum New
// records — are the source's own.
func Merge(sources ...fs.FS) (fs.FS, error) {
	merged := &mergedFS{owner: map[string]fs.FS{}}
	ownerIndex := map[string]int{}
	for i, src := range sources {
		if src == nil {
			return nil, fmt.Errorf("%w: sources[%d]", errNilSource, i)
		}
		entries, err := fs.ReadDir(src, ".")
		if err != nil {
			return nil, fmt.Errorf("migrationx: sources[%d]: %w", i, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || strings.HasPrefix(name, ".") {
				continue
			}
			if prev, ok := ownerIndex[name]; ok {
				return nil, fmt.Errorf("%w: %s in sources[%d] and sources[%d]", ErrDuplicateFile, name, prev, i)
			}
			ownerIndex[name] = i
			merged.owner[name] = src
			merged.entries = append(merged.entries, entry)
		}
	}
	slices.SortFunc(merged.entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return merged, nil
}

// mergedFS is Merge's flat view: a root directory listing entries, each
// file opened through the source in owner.
type mergedFS struct {
	owner   map[string]fs.FS
	entries []fs.DirEntry
}

// Open opens the root directory or a root-level file of its owning source.
func (m *mergedFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return &mergedDir{entries: m.entries}, nil
	}
	src, ok := m.owner[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return src.Open(name)
}

// mergedDir is an open root directory of a mergedFS.
type mergedDir struct {
	entries []fs.DirEntry
	offset  int
}

func (d *mergedDir) Stat() (fs.FileInfo, error) { return rootInfo{}, nil }
func (d *mergedDir) Close() error               { return nil }

func (d *mergedDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: fs.ErrInvalid}
}

// ReadDir follows the fs.ReadDirFile contract: n > 0 returns at most n
// entries and io.EOF once none remain; n <= 0 returns all remaining.
func (d *mergedDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.offset:]
	if n > 0 {
		if len(rest) == 0 {
			return nil, io.EOF
		}
		rest = rest[:min(n, len(rest))]
	}
	d.offset += len(rest)
	return slices.Clone(rest), nil
}

// rootInfo describes the merged root directory.
type rootInfo struct{}

func (rootInfo) Name() string       { return "." }
func (rootInfo) Size() int64        { return 0 }
func (rootInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (rootInfo) ModTime() time.Time { return time.Time{} }
func (rootInfo) IsDir() bool        { return true }
func (rootInfo) Sys() any           { return nil }
