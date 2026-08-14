package cli_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/cli"
)

func TestResolvePrecedence(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    []string
		expected string
	}{
		{
			name: "explicit flag beats environment",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_PORT", "2000")
			},
			input:    []string{"-port", "3000"},
			expected: "3000",
		},
		{
			name: "environment value applies through the flag Set path",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_PORT", "42")
			},
			expected: "42",
		},
		{
			name: "environment beats config file",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_PORT", "2000")
				t.Setenv("APP_CONFIG", flagsWriteFile(t, `{"port": 4000}`))
			},
			expected: "2000",
		},
		{
			name: "environment file beats config file",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_PORT_FILE", flagsWriteFile(t, "2500\n"))
				t.Setenv("APP_CONFIG", flagsWriteFile(t, `{"port": 4000}`))
			},
			expected: "2500",
		},
		{
			name: "config file beats default",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_CONFIG", flagsWriteFile(t, `{"port": 4000}`))
			},
			expected: "4000",
		},
		{
			name:     "default holds when nothing is set",
			expected: "1000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFunc != nil {
				tt.mockFunc(t)
			}
			var port int
			var got string
			root := &cli.Command{
				Name:   "app",
				Config: cli.ConfigFile{Flag: "config"},
				Flags: func(fs *cli.FlagSet) {
					fs.IntVar(&port, "port", 1000, "")
				},
				Run: func(context.Context, []string) error {
					got = strconv.Itoa(port)
					return nil
				},
			}
			code, stderr := flagsRun(t, root, tt.input)
			if code != 0 {
				t.Fatalf("exit code = %d, expected 0; stderr %q", code, stderr)
			}
			if got != tt.expected {
				t.Errorf("port = %s, expected %s", got, tt.expected)
			}
		})
	}
}

// flagsEnvExpected is the outcome of one TestSetFromEnv case; stderr is
// matched exactly when set, stderrHas as a substring when set.
type flagsEnvExpected struct {
	code      int
	value     string
	stderr    string
	stderrHas string
}

func TestSetFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    []string
		expected flagsEnvExpected
	}{
		{
			name: "environment value applies",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_PORT", "42")
			},
			expected: flagsEnvExpected{code: 0, value: `"d" 42 2`},
		},
		{
			name: "file value applies with trailing newline trimmed",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_NAME_FILE", flagsWriteFile(t, "web\n"))
			},
			expected: flagsEnvExpected{code: 0, value: `"web" 1 2`},
		},
		{
			name: "file value applies with trailing CRLF trimmed",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_NAME_FILE", flagsWriteFile(t, "web\r\n"))
			},
			expected: flagsEnvExpected{code: 0, value: `"web" 1 2`},
		},
		{
			name: "file value keeps interior and trailing spaces",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_NAME_FILE", flagsWriteFile(t, "a b \r\n"))
			},
			expected: flagsEnvExpected{code: 0, value: `"a b " 1 2`},
		},
		{
			name: "both environment and file set",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_NAME", "a")
				t.Setenv("APP_NAME_FILE", flagsWriteFile(t, "b"))
			},
			expected: flagsEnvExpected{
				code:   2,
				stderr: "cli: conflicting environment configuration: both APP_NAME and APP_NAME_FILE are set\n",
			},
		},
		{
			name: "missing file",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_NAME_FILE", filepath.Join(t.TempDir(), "absent"))
			},
			expected: flagsEnvExpected{
				code:      2,
				stderrHas: "cli: reading APP_NAME_FILE: open ",
			},
		},
		{
			name: "invalid value for a plain flag echoes the value",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_PORT", "abc")
			},
			expected: flagsEnvExpected{
				code:   2,
				stderr: "cli: invalid value \"abc\" for flag -port from APP_PORT\n",
			},
		},
		{
			name: "invalid value for a secret flag omits the value",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_SECRET_PORT", "abc")
			},
			expected: flagsEnvExpected{
				code:   2,
				stderr: "cli: invalid value for flag -secret-port from APP_SECRET_PORT\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFunc != nil {
				tt.mockFunc(t)
			}
			var name string
			var port, sport int
			var got string
			root := &cli.Command{
				Name: "app",
				Flags: func(fs *cli.FlagSet) {
					fs.StringVar(&name, "name", "d", "")
					fs.IntVar(&port, "port", 1, "")
					fs.IntVar(&sport, "secret-port", 2, "")
					fs.Secret("secret-port")
				},
				Run: func(context.Context, []string) error {
					got = fmt.Sprintf("%q %d %d", name, port, sport)
					return nil
				},
			}
			code, stderr := flagsRun(t, root, tt.input)
			if code != tt.expected.code {
				t.Fatalf("exit code = %d, expected %d; stderr %q", code, tt.expected.code, stderr)
			}
			if got != tt.expected.value {
				t.Errorf("value = %q, expected %q", got, tt.expected.value)
			}
			if tt.expected.stderrHas != "" {
				if !strings.Contains(stderr, tt.expected.stderrHas) {
					t.Errorf("stderr = %q, expected to contain %q", stderr, tt.expected.stderrHas)
				}
			} else if stderr != tt.expected.stderr {
				t.Errorf("stderr = %q, expected %q", stderr, tt.expected.stderr)
			}
		})
	}
}

// flagsPrefixInput shapes the root for one TestEnvPrefix case.
type flagsPrefixInput struct {
	rootName  string
	envPrefix string
	flagName  string
}

func TestEnvPrefix(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    flagsPrefixInput
		expected string
	}{
		{
			name: "prefix derived from the root name",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("MY_SERVICE_X", "from-env")
			},
			input:    flagsPrefixInput{rootName: "my-service", flagName: "x"},
			expected: "from-env",
		},
		{
			name: "EnvPrefix overrides the derived prefix",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("CUSTOM2_X", "from-env")
			},
			input:    flagsPrefixInput{rootName: "my-service", envPrefix: "Custom2", flagName: "x"},
			expected: "from-env",
		},
		{
			name: "NoPrefix binds the bare flag name",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("HTTP_ADDR", "from-env")
			},
			input:    flagsPrefixInput{rootName: "app", envPrefix: cli.NoPrefix, flagName: "http-addr"},
			expected: "from-env",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mockFunc(t)
			var v, got string
			root := &cli.Command{
				Name:      tt.input.rootName,
				EnvPrefix: tt.input.envPrefix,
				Flags: func(fs *cli.FlagSet) {
					fs.StringVar(&v, tt.input.flagName, "", "")
				},
				Run: func(context.Context, []string) error {
					got = v
					return nil
				},
			}
			code, stderr := flagsRun(t, root, nil)
			if code != 0 {
				t.Fatalf("exit code = %d, expected 0; stderr %q", code, stderr)
			}
			if got != tt.expected {
				t.Errorf("value = %q, expected %q", got, tt.expected)
			}
		})
	}
}
