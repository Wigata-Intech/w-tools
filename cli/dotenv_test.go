package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/cli"
)

// dotenvWrite writes content as a dotenv file and returns its path.
func dotenvWrite(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// dotenvClear unsets key now and restores the pre-test state afterwards.
func dotenvClear(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "sentinel")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

// dotenvInput is one LoadDotEnv invocation: the file content and the
// key whose resulting value the case asserts.
type dotenvInput struct {
	content string
	key     string
}

func TestLoadDotEnv(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    dotenvInput
		expected string
	}{
		{
			name:     "plain pair",
			input:    dotenvInput{content: "DOTENV_T_A=one\n", key: "DOTENV_T_A"},
			expected: "one",
		},
		{
			name:     "comments and blank lines skipped",
			input:    dotenvInput{content: "# a comment\n\n  \nDOTENV_T_B=two\n", key: "DOTENV_T_B"},
			expected: "two",
		},
		{
			name:     "whitespace around the key trimmed",
			input:    dotenvInput{content: "  DOTENV_T_C  =three\n", key: "DOTENV_T_C"},
			expected: "three",
		},
		{
			name:     "double quotes stripped",
			input:    dotenvInput{content: `DOTENV_T_D=" spaced value "` + "\n", key: "DOTENV_T_D"},
			expected: " spaced value ",
		},
		{
			name:     "single quotes stripped",
			input:    dotenvInput{content: "DOTENV_T_E='five'\n", key: "DOTENV_T_E"},
			expected: "five",
		},
		{
			name:     "mismatched quotes kept verbatim",
			input:    dotenvInput{content: `DOTENV_T_F="six'` + "\n", key: "DOTENV_T_F"},
			expected: `"six'`,
		},
		{
			name:     "crlf line endings handled",
			input:    dotenvInput{content: "DOTENV_T_G=seven\r\n", key: "DOTENV_T_G"},
			expected: "seven",
		},
		{
			name:     "empty value allowed",
			input:    dotenvInput{content: "DOTENV_T_H=\n", key: "DOTENV_T_H"},
			expected: "",
		},
		{
			name:     "value keeps its own equals signs",
			input:    dotenvInput{content: "DOTENV_T_I=a=b=c\n", key: "DOTENV_T_I"},
			expected: "a=b=c",
		},
		{
			name: "existing environment wins",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("DOTENV_T_J", "from-real-env")
			},
			input:    dotenvInput{content: "DOTENV_T_J=from-file\n", key: "DOTENV_T_J"},
			expected: "from-real-env",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFunc != nil {
				tt.mockFunc(t)
			} else {
				dotenvClear(t, tt.input.key)
			}
			if err := cli.LoadDotEnv(dotenvWrite(t, tt.input.content)); err != nil {
				t.Fatalf("LoadDotEnv: %v", err)
			}
			if got := os.Getenv(tt.input.key); got != tt.expected {
				t.Errorf("env %s = %q, want %q", tt.input.key, got, tt.expected)
			}
		})
	}

	t.Run("leading utf-8 bom stripped before the first key", func(t *testing.T) {
		dotenvClear(t, "DOTENV_T_BOM")
		if err := cli.LoadDotEnv(dotenvWrite(t, "\xef\xbb\xbfDOTENV_T_BOM=v\n")); err != nil {
			t.Fatalf("LoadDotEnv: %v", err)
		}
		if got := os.Getenv("DOTENV_T_BOM"); got != "v" {
			t.Errorf("env DOTENV_T_BOM = %q, want %q", got, "v")
		}
	})

	t.Run("shell-style export line errors instead of setting a wrong key", func(t *testing.T) {
		err := cli.LoadDotEnv(dotenvWrite(t, "export DOTENV_T_EXP=v\n"))
		if err == nil {
			t.Fatal("LoadDotEnv accepted an export-prefixed key")
		}
		if !strings.Contains(err.Error(), "line 1: not a KEY=VALUE line") {
			t.Fatalf("error = %v, want line 1 diagnosis", err)
		}
		if os.Getenv("export DOTENV_T_EXP") != "" {
			t.Error("a whitespace-containing key was set")
		}
	})

	t.Run("malformed line reports its number", func(t *testing.T) {
		err := cli.LoadDotEnv(dotenvWrite(t, "DOTENV_T_K=ok\nnot a pair\n"))
		if err == nil {
			t.Fatal("LoadDotEnv accepted a malformed line")
		}
		if !strings.Contains(err.Error(), "line 2: not a KEY=VALUE line") {
			t.Fatalf("error = %v, want line 2 diagnosis", err)
		}
	})

	t.Run("empty key reports its number", func(t *testing.T) {
		err := cli.LoadDotEnv(dotenvWrite(t, "=value\n"))
		if err == nil {
			t.Fatal("LoadDotEnv accepted an empty key")
		}
		if !strings.Contains(err.Error(), "line 1: not a KEY=VALUE line") {
			t.Fatalf("error = %v, want line 1 diagnosis", err)
		}
	})

	t.Run("nul in a value reports its number", func(t *testing.T) {
		err := cli.LoadDotEnv(dotenvWrite(t, "DOTENV_T_NUL=a\x00b\n"))
		if err == nil {
			t.Fatal("LoadDotEnv accepted a NUL value")
		}
		if !strings.Contains(err.Error(), "line 1: not a KEY=VALUE line") {
			t.Fatalf("error = %v, want line 1 diagnosis", err)
		}
	})

	t.Run("key the OS rejects reports its number", func(t *testing.T) {
		err := cli.LoadDotEnv(dotenvWrite(t, "DOT\x00KEY=v\n"))
		if err == nil {
			t.Fatal("LoadDotEnv accepted a NUL key")
		}
		if !strings.Contains(err.Error(), "line 1:") {
			t.Fatalf("error = %v, want line 1 diagnosis", err)
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		err := cli.LoadDotEnv(filepath.Join(t.TempDir(), "absent.env"))
		if err == nil {
			t.Fatal("LoadDotEnv accepted a missing file")
		}
		if !strings.Contains(err.Error(), "cli: dotenv:") {
			t.Fatalf("error = %v, want wrapped dotenv error", err)
		}
	})
}
