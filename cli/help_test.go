//nolint:dupword // golden help output: the help builtin row legitimately reads "help  help for app or a command".
package cli_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/cli"
)

// helpInput is one help invocation: the command tree and the arguments
// that trigger help output.
type helpInput struct {
	cmd  *cli.Command
	args []string
}

func TestWriteHelp(t *testing.T) {
	tests := []struct {
		name     string
		input    helpInput
		expected string
	}{
		{
			name: "long description used when set",
			input: helpInput{
				cmd:  &cli.Command{Name: "app", Short: "short text", Long: "the long description"},
				args: []string{"-h"},
			},
			expected: `the long description

Usage:
  app
`,
		},
		{
			name: "short fallback when long empty",
			input: helpInput{
				cmd:  &cli.Command{Name: "app", Short: "short text"},
				args: []string{"-h"},
			},
			expected: `short text

Usage:
  app
`,
		},
		{
			name: "no description and neither usage token",
			input: helpInput{
				cmd:  &cli.Command{Name: "app"},
				args: []string{"-h"},
			},
			expected: `Usage:
  app
`,
		},
		{
			name: "usage with command token only",
			input: helpInput{
				cmd: &cli.Command{
					Name:     "app",
					Commands: []*cli.Command{{Name: "serve", Short: "start the server"}},
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [command]

Commands:
  serve  start the server
  help   help for app or a command
`,
		},
		{
			name: "usage with flags token only",
			input: helpInput{
				cmd: &cli.Command{
					Name:  "app",
					Flags: func(fs *cli.FlagSet) { fs.String("name", "gopher", "the name") },
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [flags]

Flags:
  -name string  the name (env APP_NAME) (default "gopher")
`,
		},
		{
			name: "usage with both tokens",
			input: helpInput{
				cmd: &cli.Command{
					Name:     "app",
					Flags:    func(fs *cli.FlagSet) { fs.String("name", "gopher", "the name") },
					Commands: []*cli.Command{{Name: "serve", Short: "start the server"}},
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [command] [flags]

Commands:
  serve  start the server
  help   help for app or a command

Flags:
  -name string  the name (env APP_NAME) (default "gopher")
`,
		},
		{
			name: "commands in declaration order with builtins",
			input: helpInput{
				cmd: &cli.Command{
					Name:    "app",
					Version: "1.0.0",
					Commands: []*cli.Command{
						{Name: "serve", Short: "start the server"},
						{Name: "migrate", Short: "run migrations"},
					},
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [command]

Commands:
  serve    start the server
  migrate  run migrations
  help     help for app or a command
  version  print the version
`,
		},
		{
			name: "subcommand help has no commands section",
			input: helpInput{
				cmd: &cli.Command{
					Name:     "app",
					Commands: []*cli.Command{{Name: "serve", Short: "start the server"}},
				},
				args: []string{"help", "serve"},
			},
			expected: `start the server

Usage:
  app serve
`,
		},
		{
			name: "command without short renders bare row",
			input: helpInput{
				cmd: &cli.Command{
					Name:     "app",
					Commands: []*cli.Command{{Name: "serve"}},
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [command]

Commands:
  serve
  help   help for app or a command
`,
		},
		{
			name: "flag table types env defaults and secret",
			input: helpInput{
				cmd: &cli.Command{
					Name: "app",
					Flags: func(fs *cli.FlagSet) {
						fs.String("name", "gopher", "the name")
						fs.Int("count", 2, "how many")
						fs.Duration("timeout", 5*time.Second, "request timeout")
						fs.Duration("idle", 0, "idle window")
						fs.Bool("verbose", false, "verbose output")
						fs.String("note", "", "free note")
						fs.Int("port", 0, "listen port")
						fs.String("token", "hunter2", "api token")
						fs.Secret("token")
					},
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [flags]

Flags:
  -count int         how many (env APP_COUNT) (default "2")
  -idle duration     idle window (env APP_IDLE)
  -name string       the name (env APP_NAME) (default "gopher")
  -note string       free note (env APP_NOTE)
  -port int          listen port (env APP_PORT)
  -timeout duration  request timeout (env APP_TIMEOUT) (default "5s")
  -token string      api token (env APP_TOKEN) (default <secret>)
  -verbose           verbose output (env APP_VERBOSE)
`,
		},
		{
			name: "string defaults resembling zero literals still render",
			input: helpInput{
				cmd: &cli.Command{
					Name: "app",
					Flags: func(fs *cli.FlagSet) {
						fs.String("mode", "0", "mode selector")
						fs.String("strict", "false", "strictness")
					},
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [flags]

Flags:
  -mode string    mode selector (env APP_MODE) (default "0")
  -strict string  strictness (env APP_STRICT) (default "false")
`,
		},
		{
			name: "required flag carries its marker",
			input: helpInput{
				cmd: &cli.Command{
					Name: "app",
					Flags: func(fs *cli.FlagSet) {
						fs.String("api-key", "", "upstream key")
						fs.Required("api-key")
					},
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [flags]

Flags:
  -api-key string  upstream key (required) (env APP_API_KEY)
`,
		},
		{
			name: "env prefix override",
			input: helpInput{
				cmd: &cli.Command{
					Name:      "app",
					EnvPrefix: "my-service",
					Flags:     func(fs *cli.FlagSet) { fs.String("addr", "", "bind address") },
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [flags]

Flags:
  -addr string  bind address (env MY_SERVICE_ADDR)
`,
		},
		{
			name: "no prefix sentinel",
			input: helpInput{
				cmd: &cli.Command{
					Name:      "app",
					EnvPrefix: cli.NoPrefix,
					Flags:     func(fs *cli.FlagSet) { fs.String("addr", "", "bind address") },
				},
				args: []string{"-h"},
			},
			expected: `Usage:
  app [flags]

Flags:
  -addr string  bind address (env ADDR)
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			cli.SetIO(tt.input.cmd, &stdout, &stderr)
			code := cli.ExecuteArgs(context.Background(), tt.input.cmd, tt.input.args)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr.String())
			}
			if got := stdout.String(); got != tt.expected {
				t.Errorf("stdout = %q, want %q", got, tt.expected)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}
