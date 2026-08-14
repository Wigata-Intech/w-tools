package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

var errDotEnvLine = errors.New("not a KEY=VALUE line")

// LoadDotEnv loads KEY=VALUE lines from path into the process
// environment, then returns — the environment layer picks the values up
// through the normal precedence, so keys carry the same prefixed names
// real environment variables do. Call it before Execute.
//
// Variables already present in the environment win: the file supplies
// defaults for a bare machine, the real environment — a container
// orchestrator, CI — always overrides it.
//
// The format is the minimal dotenv subset: one KEY=VALUE per line, blank
// lines and #-comments skipped, whitespace trimmed around the key,
// matching single or double quotes stripped from the value, a leading
// UTF-8 BOM ignored. A key containing whitespace — including shell-style
// "export KEY=..." lines — is a line-numbered error, never a silently
// wrong variable. No interpolation, no escapes, no multiline values — a
// file needing those belongs to an orchestrator's env_file, not to this
// loader.
func LoadDotEnv(path string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- the caller's own dotenv path; reading it is the feature
	if err != nil {
		return fmt.Errorf("cli: dotenv: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsFunc(key, unicode.IsSpace) {
			return fmt.Errorf("cli: dotenv %s line %d: %w", path, n+1, errDotEnvLine)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if q := value[0]; (q == '"' || q == '\'') && value[len(value)-1] == q {
				value = value[1 : len(value)-1]
			}
		}
		if strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("cli: dotenv %s line %d: %w", path, n+1, errDotEnvLine)
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("cli: dotenv %s line %d: %w", path, n+1, err)
		}
	}
	return nil
}
