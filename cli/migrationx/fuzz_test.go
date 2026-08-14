package migrationx_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/cli/migrationx"
)

// FuzzParseScript feeds arbitrary sources through the statement scanner:
// no input may panic, an accepted source never yields a blank statement,
// and re-parsing the joined statements parses or errors — never panics —
// and cannot lose every statement.
func FuzzParseScript(f *testing.F) {
	seeds := []struct {
		src       string
		backslash bool
	}{
		{"", false},
		{"CREATE TABLE t (id INTEGER);", false},
		{"-- migrationx:no-transaction\nSELECT 1;", false},
		{"-- migrationx:statement begin\nSELECT 1;\nSELECT 2;\n-- migrationx:statement end\n", false},
		{"-- migrationx:statement begin\n", false},
		{"-- migrationx:statement end\n", false},
		{"-- migrationx:bogus\n", false},
		{"SELECT 'a; b';", false},
		{`SELECT "a; b";`, false},
		{"SELECT `a; b`;", false},
		{"SELECT 'it''s';", false},
		{`SELECT 'a\'b; c';`, true},
		{`SELECT 'a\'b; c\'';`, false},
		{"-- comment; here\nSELECT 1;", false},
		{"SELECT 1; -- done; really\n", false},
		{"/* multi\nline; */ SELECT 1;", false},
		{"SELECT 'open", false},
		{"/* open", false},
		{"SELECT 1;\r\nSELECT 2;\r\n", true},
	}
	for _, seed := range seeds {
		f.Add(seed.src, seed.backslash)
	}
	f.Fuzz(func(t *testing.T, src string, backslash bool) {
		statements, _, err := migrationx.ParseScript(src, backslash)
		if err != nil {
			return
		}
		for i, stmt := range statements {
			if strings.TrimSpace(stmt) == "" {
				t.Fatalf("statement %d is blank (input %q)", i, src)
			}
		}
		rejoined := strings.Join(statements, ";\n")
		restatements, _, err := migrationx.ParseScript(rejoined, backslash)
		if err != nil {
			return // an error is legal here — a statement may end in a line comment that swallows the join semicolon
		}
		if len(statements) > 0 && len(restatements) == 0 {
			t.Fatalf("re-parse lost every statement (input %q, rejoined %q)", src, rejoined)
		}
	})
}

// FuzzParseFilename feeds arbitrary names through the filename parser:
// no input may panic, and an accepted name has a positive version, a
// name in [a-z0-9_]+, and reconstructs to a filename that parses back
// to identical values.
func FuzzParseFilename(f *testing.F) {
	for _, seed := range []string{
		"1_init.up.sql",
		"1_init.down.sql",
		"1723600000_add_users.up.sql",
		"9223372036854775807_a.up.sql",
		"README.md",
		"1_x.sql",
		"x_1.up.sql",
		"1_Init.up.sql",
		"0_a.up.sql",
		"-1_a.up.sql",
		"1_.up.sql",
		"1.up.sql",
		"01_a.up.sql",
		"1__a.up.sql",
	} {
		f.Add(seed)
	}
	pattern := regexp.MustCompile(`^[a-z0-9_]+$`)
	f.Fuzz(func(t *testing.T, filename string) {
		version, name, down, err := migrationx.ParseFilename(filename)
		if err != nil {
			return
		}
		if version <= 0 {
			t.Fatalf("accepted non-positive version %d (input %q)", version, filename)
		}
		if !pattern.MatchString(name) {
			t.Fatalf("accepted name %q outside [a-z0-9_]+ (input %q)", name, filename)
		}
		suffix := "up"
		if down {
			suffix = "down"
		}
		rebuilt := fmt.Sprintf("%d_%s.%s.sql", version, name, suffix)
		version2, name2, down2, err := migrationx.ParseFilename(rebuilt)
		if err != nil {
			t.Fatalf("rebuilt filename %q rejected: %v (input %q)", rebuilt, err, filename)
		}
		if version2 != version || name2 != name || down2 != down {
			t.Fatalf("round trip diverged: %q -> (%d, %q, %t), rebuilt %q -> (%d, %q, %t)",
				filename, version, name, down, rebuilt, version2, name2, down2)
		}
	})
}
