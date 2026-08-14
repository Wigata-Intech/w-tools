package migrationx_test

import (
	"slices"
	"testing"

	"github.com/Wigata-Intech/w-tools/cli/migrationx"
)

// scanInput is one ParseScript invocation: the file source and the
// dialect's backslash-escape flag.
type scanInput struct {
	src       string
	backslash bool
}

// scanExpected is the parse outcome: the split statements, the
// no-transaction flag, and the exact error text ("" means success).
type scanExpected struct {
	statements []string
	noTx       bool
	errMsg     string
}

func TestParseScript(t *testing.T) {
	tests := []struct {
		name     string
		input    scanInput
		expected scanExpected
	}{
		{
			name:     "single statement without a trailing semicolon",
			input:    scanInput{src: "CREATE TABLE t (id INTEGER)"},
			expected: scanExpected{statements: []string{"CREATE TABLE t (id INTEGER)"}},
		},
		{
			name:     "single statement with a trailing semicolon",
			input:    scanInput{src: "CREATE TABLE t (id INTEGER);\n"},
			expected: scanExpected{statements: []string{"CREATE TABLE t (id INTEGER)"}},
		},
		{
			name:  "multiple statements split on semicolons",
			input: scanInput{src: "CREATE TABLE a (id INTEGER);\nCREATE TABLE b (id INTEGER);\n"},
			expected: scanExpected{statements: []string{
				"CREATE TABLE a (id INTEGER)",
				"CREATE TABLE b (id INTEGER)",
			}},
		},
		{
			name:     "semicolon inside single quotes not split",
			input:    scanInput{src: "INSERT INTO t VALUES ('a; b');"},
			expected: scanExpected{statements: []string{"INSERT INTO t VALUES ('a; b')"}},
		},
		{
			name:     "semicolon inside double quotes not split",
			input:    scanInput{src: `SELECT "a; b";`},
			expected: scanExpected{statements: []string{`SELECT "a; b"`}},
		},
		{
			name:     "semicolon inside backticks not split",
			input:    scanInput{src: "SELECT `a; b`;"},
			expected: scanExpected{statements: []string{"SELECT `a; b`"}},
		},
		{
			name:     "doubled quote escapes inside a string",
			input:    scanInput{src: "INSERT INTO t VALUES ('it''s; fine');"},
			expected: scanExpected{statements: []string{"INSERT INTO t VALUES ('it''s; fine')"}},
		},
		{
			name:     "mysql backslash escape keeps the quote open",
			input:    scanInput{src: `SELECT 'a\'b; c';`, backslash: true},
			expected: scanExpected{statements: []string{`SELECT 'a\'b; c'`}},
		},
		{
			name:  "sqlite literal backslash lets the quote close and splits",
			input: scanInput{src: `SELECT 'a\'b; c\'';`},
			expected: scanExpected{statements: []string{
				`SELECT 'a\'b`,
				`c\''`,
			}},
		},
		{
			name:  "semicolon in a line comment not split",
			input: scanInput{src: "-- setup; note the semicolon\nCREATE TABLE t (id INTEGER);\n"},
			expected: scanExpected{statements: []string{
				"-- setup; note the semicolon\nCREATE TABLE t (id INTEGER)",
			}},
		},
		{
			name:     "trailing comment after the final semicolon is dropped, not executed",
			input:    scanInput{src: "SELECT 1; -- done; really\n"},
			expected: scanExpected{statements: []string{"SELECT 1"}},
		},
		{
			name:     "near-miss annotation without the space aborts",
			input:    scanInput{src: "--migrationx:no-transaction\nSELECT 1;\n"},
			expected: scanExpected{errMsg: `line 1: unknown migrationx annotation: "--migrationx:no-transaction"`},
		},
		{
			name:     "mysql hash comment does not split or execute",
			input:    scanInput{src: "# note; here\nSELECT 1;\n", backslash: true},
			expected: scanExpected{statements: []string{"# note; here\nSELECT 1"}},
		},
		{
			name:     "comment-only file yields no statements",
			input:    scanInput{src: "-- header\n/* block; */\n"},
			expected: scanExpected{statements: nil},
		},
		{
			name:     "semicolon in a block comment spanning lines not split",
			input:    scanInput{src: "/* multi\nline; comment */\nSELECT 1;\n"},
			expected: scanExpected{statements: []string{"/* multi\nline; comment */\nSELECT 1"}},
		},
		{
			name: "statement region keeps semicolons and flushes as one",
			input: scanInput{src: "CREATE TABLE t (id INTEGER);\n" +
				"-- migrationx:statement begin\n" +
				"CREATE TRIGGER trg AFTER INSERT ON t BEGIN\n" +
				"UPDATE t SET id = 1;\n" +
				"END;\n" +
				"-- migrationx:statement end\n" +
				"SELECT 1;\n"},
			expected: scanExpected{statements: []string{
				"CREATE TABLE t (id INTEGER)",
				"CREATE TRIGGER trg AFTER INSERT ON t BEGIN\nUPDATE t SET id = 1;\nEND;",
				"SELECT 1",
			}},
		},
		{
			name:  "no-transaction annotation sets the flag",
			input: scanInput{src: "-- migrationx:no-transaction\nCREATE INDEX i ON t (id);\n"},
			expected: scanExpected{
				statements: []string{"CREATE INDEX i ON t (id)"},
				noTx:       true,
			},
		},
		{
			name:     "annotation text inside an open quote stays literal",
			input:    scanInput{src: "SELECT 'a\n-- migrationx:bogus\nb';"},
			expected: scanExpected{statements: []string{"SELECT 'a\n-- migrationx:bogus\nb'"}},
		},
		{
			name:  "crlf line endings",
			input: scanInput{src: "-- migrationx:no-transaction\r\nSELECT 1;\r\nSELECT 2;\r\n"},
			expected: scanExpected{
				statements: []string{"SELECT 1", "SELECT 2"},
				noTx:       true,
			},
		},
		{
			name:     "empty file yields no statements",
			input:    scanInput{src: ""},
			expected: scanExpected{},
		},
		{
			name:     "nested statement begin",
			input:    scanInput{src: "-- migrationx:statement begin\n-- migrationx:statement begin\n"},
			expected: scanExpected{errMsg: "line 2: statement begin inside an open statement region"},
		},
		{
			name:     "statement end without a begin",
			input:    scanInput{src: "-- migrationx:statement end\n"},
			expected: scanExpected{errMsg: "line 1: statement end without a begin"},
		},
		{
			name:     "unknown annotation",
			input:    scanInput{src: "SELECT 1;\n-- migrationx:bogus\n"},
			expected: scanExpected{errMsg: `line 2: unknown migrationx annotation: "-- migrationx:bogus"`},
		},
		{
			name:     "statement region never closed",
			input:    scanInput{src: "-- migrationx:statement begin\nSELECT 1;\n"},
			expected: scanExpected{errMsg: "statement region never closed"},
		},
		{
			name:     "unterminated quote",
			input:    scanInput{src: "SELECT 'oops\n"},
			expected: scanExpected{errMsg: "unterminated quote or comment at end of file"},
		},
		{
			name:     "unterminated block comment",
			input:    scanInput{src: "/* oops\n"},
			expected: scanExpected{errMsg: "unterminated quote or comment at end of file"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statements, noTx, err := migrationx.ParseScript(tt.input.src, tt.input.backslash)
			if tt.expected.errMsg != "" {
				if err == nil {
					t.Fatalf("ParseScript succeeded, want error %q", tt.expected.errMsg)
				}
				if err.Error() != tt.expected.errMsg {
					t.Fatalf("error = %q, want %q", err, tt.expected.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseScript: %v", err)
			}
			if !slices.Equal(statements, tt.expected.statements) {
				t.Errorf("statements = %q, want %q", statements, tt.expected.statements)
			}
			if noTx != tt.expected.noTx {
				t.Errorf("noTx = %t, want %t", noTx, tt.expected.noTx)
			}
		})
	}
}
