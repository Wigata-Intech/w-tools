package migrationx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errCreateExists = errors.New("migrationx: migration files already exist")

// Create scaffolds a timestamped migration pair in dir —
// <unix>_<name>.up.sql and .down.sql — and returns both paths. The
// version is the current unix second: tool-minted timestamps are what
// keep two engineers' migrations from colliding on merge. The name must
// be lowercase [a-z0-9_].
func Create(dir, name string) (string, string, error) {
	if !validName(name) {
		return "", "", fmt.Errorf("migrationx: %q: %w", name, errBadName)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", fmt.Errorf("migrationx: %w", err)
	}
	version := timeNow().Unix()
	up := filepath.Join(dir, fmt.Sprintf("%d_%s.up.sql", version, name))
	down := filepath.Join(dir, fmt.Sprintf("%d_%s.down.sql", version, name))

	for _, path := range []string{up, down} {
		if _, statErr := os.Stat(path); statErr == nil {
			return "", "", fmt.Errorf("%w: %s", errCreateExists, path)
		}
	}
	header := fmt.Sprintf("-- %s\n", name)
	if err := os.WriteFile(up, []byte(header), 0o600); err != nil {
		return "", "", fmt.Errorf("migrationx: %w", err)
	}
	if err := os.WriteFile(down, []byte(header), 0o600); err != nil {
		_ = os.Remove(up)
		return "", "", fmt.Errorf("migrationx: %w", err)
	}
	return up, down, nil
}
