package migrationx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

var (
	errStrayFile        = errors.New("not a <unix-timestamp>_<name>.up.sql or .down.sql file")
	errBadName          = errors.New("migration name must be lowercase [a-z0-9_]")
	errBadVersion       = errors.New("migration version must be a positive integer")
	errDuplicateVersion = errors.New("duplicate migration version")
	errDownWithoutUp    = errors.New("down migration without an up migration")
)

// migration is one loaded version: the parsed up script, the optional
// down script, and the checksum of the raw up bytes.
type migration struct {
	version  int64
	name     string
	checksum string
	up       script
	down     *script
}

// loadMigrations reads and validates every file in the filesystem root:
// filenames parse, names pair, versions are unique, and both scripts
// parse — a migration set with a stray or malformed file aborts at New,
// never at apply time.
func loadMigrations(fsys fs.FS, backslashEscapes bool) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("migrationx: %w", err)
	}

	byVersion := map[int64]*migration{}
	var halves []half

	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("migrationx: %s: %w", entry.Name(), errStrayFile)
		}
		h, err := parseFilename(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("migrationx: %s: %w", entry.Name(), err)
		}
		halves = append(halves, h)
	}

	// Up halves first: a down file's version must already exist.
	sort.SliceStable(halves, func(i, j int) bool {
		if halves[i].down != halves[j].down {
			return !halves[i].down
		}
		return halves[i].version < halves[j].version
	})

	for _, h := range halves {
		suffix := "up"
		if h.down {
			suffix = "down"
		}
		filename := fmt.Sprintf("%d_%s.%s.sql", h.version, h.name, suffix)
		data, err := fs.ReadFile(fsys, filename)
		if err != nil {
			return nil, fmt.Errorf("migrationx: %w", err)
		}
		parsed, err := parseScript(string(data), backslashEscapes)
		if err != nil {
			return nil, fmt.Errorf("migrationx: %s: %w", filename, err)
		}

		existing := byVersion[h.version]
		if h.down {
			if existing == nil || existing.name != h.name {
				return nil, fmt.Errorf("migrationx: %s: %w", filename, errDownWithoutUp)
			}
			existing.down = &parsed
			continue
		}
		if existing != nil {
			return nil, fmt.Errorf("migrationx: %s: %w %d", filename, errDuplicateVersion, h.version)
		}
		sum := sha256.Sum256(data)
		byVersion[h.version] = &migration{
			version:  h.version,
			name:     h.name,
			checksum: hex.EncodeToString(sum[:]),
			up:       parsed,
		}
	}

	out := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// half is one parsed migration filename.
type half struct {
	version int64
	name    string
	down    bool
}

// parseFilename splits <version>_<name>.<up|down>.sql.
func parseFilename(filename string) (half, error) {
	rest, ok := strings.CutSuffix(filename, ".sql")
	if !ok {
		return half{}, errStrayFile
	}
	var down bool
	if r, isDown := strings.CutSuffix(rest, ".down"); isDown {
		rest, down = r, true
	} else if r, isUp := strings.CutSuffix(rest, ".up"); isUp {
		rest = r
	} else {
		return half{}, errStrayFile
	}

	versionText, name, ok := strings.Cut(rest, "_")
	if !ok || name == "" {
		return half{}, errStrayFile
	}
	version, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || version <= 0 || versionText != strconv.FormatInt(version, 10) {
		return half{}, errBadVersion
	}
	if !validName(name) {
		return half{}, errBadName
	}
	return half{version: version, name: name, down: down}, nil
}

func validName(name string) bool {
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return name != ""
}
