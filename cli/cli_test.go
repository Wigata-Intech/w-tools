//nolint:dupword // golden help output: the help builtin row legitimately reads "help  help for app or a command".
package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/Wigata-Intech/w-tools/cli"
)

var errExecBoom = errors.New("boom")

// execInput is one Execute invocation: a command-tree factory wired to the
// captured stdout, and the raw arguments.
type execInput struct {
	build func(out io.Writer) *cli.Command
	args  []string
}

// execExpected is the observable outcome of one Execute invocation.
type execExpected struct {
	code   int
	stdout string
	stderr string
}

// execPrintf writes formatted output inside Run funcs; the buffer write
// cannot fail.
func execPrintf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// execEcho returns a Run that records the label and its operands on out.
func execEcho(out io.Writer, label string) func(context.Context, []string) error {
	return func(_ context.Context, args []string) error {
		execPrintf(out, "%s %q\n", label, args)
		return nil
	}
}

func TestExecute(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    execInput
		expected execExpected
	}{
		{
			name: "root run with no args",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{Name: "app", Run: execEcho(out, "root")}
				},
			},
			expected: execExpected{code: 0, stdout: "root []\n"},
		},
		{
			name: "subcommand dispatch with operands",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:     "app",
						Commands: []*cli.Command{{Name: "serve", Run: execEcho(out, "serve")}},
					}
				},
				args: []string{"serve", "a", "b"},
			},
			expected: execExpected{code: 0, stdout: "serve [\"a\" \"b\"]\n"},
		},
		{
			name: "nested subcommand three levels",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name: "app",
						Commands: []*cli.Command{{
							Name:     "db",
							Commands: []*cli.Command{{Name: "migrate", Run: execEcho(out, "migrate")}},
						}},
					}
				},
				args: []string{"db", "migrate", "up"},
			},
			expected: execExpected{code: 0, stdout: "migrate [\"up\"]\n"},
		},
		{
			name: "operand not matching subcommand goes to run",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:     "app",
						Run:      execEcho(out, "root"),
						Commands: []*cli.Command{{Name: "serve", Run: execEcho(out, "serve")}},
					}
				},
				args: []string{"other", "x"},
			},
			expected: execExpected{code: 0, stdout: "root [\"other\" \"x\"]\n"},
		},
		{
			name: "unknown operand on nil-run command",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:     "app",
						Commands: []*cli.Command{{Name: "serve", Run: execEcho(out, "serve")}},
					}
				},
				args: []string{"nope"},
			},
			expected: execExpected{
				code:   2,
				stderr: "app: unknown command: nope\nRun 'app --help' for usage.\n",
			},
		},
		{
			name: "bare nil-run root prints help",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:  "app",
						Short: "demo app",
						Commands: []*cli.Command{
							{Name: "serve", Short: "start the server", Run: execEcho(out, "serve")},
						},
					}
				},
			},
			expected: execExpected{
				code: 0,
				stdout: `demo app

Usage:
  app [command]

Commands:
  serve  start the server
  help   help for app or a command
`,
			},
		},
		{
			name: "run error",
			input: execInput{
				build: func(_ io.Writer) *cli.Command {
					return &cli.Command{
						Name: "app",
						Run:  func(_ context.Context, _ []string) error { return errExecBoom },
					}
				},
			},
			expected: execExpected{code: 1, stderr: "boom\n"},
		},
		{
			name: "unknown flag",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{Name: "app", Run: execEcho(out, "root")}
				},
				args: []string{"-nope"},
			},
			expected: execExpected{
				code:   2,
				stderr: "flag provided but not defined: -nope\nRun 'app --help' for usage.\n",
			},
		},
		{
			name: "help flag at root",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{Name: "app", Short: "demo app", Run: execEcho(out, "root")}
				},
				args: []string{"-h"},
			},
			expected: execExpected{code: 0, stdout: "demo app\n\nUsage:\n  app\n"},
		},
		{
			name: "help flag at subcommand",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name: "app",
						Commands: []*cli.Command{
							{Name: "serve", Short: "start the server", Run: execEcho(out, "serve")},
						},
					}
				},
				args: []string{"serve", "--help"},
			},
			expected: execExpected{code: 0, stdout: "start the server\n\nUsage:\n  app serve\n"},
		},
		{
			name: "version subcommand",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{Name: "app", Version: "1.2.3", Run: execEcho(out, "root"), Commands: []*cli.Command{
						{Name: "serve", Run: execEcho(out, "serve")},
					}}
				},
				args: []string{"version"},
			},
			expected: execExpected{code: 0, stdout: "app 1.2.3\n"},
		},
		{
			name: "version operand on a leaf root is data, not the builtin",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{Name: "app", Version: "1.2.3", Run: execEcho(out, "root")}
				},
				args: []string{"version"},
			},
			expected: execExpected{code: 0, stdout: "root [\"version\"]\n"},
		},
		{
			name: "version flag",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{Name: "app", Version: "1.2.3", Run: execEcho(out, "root")}
				},
				args: []string{"--version"},
			},
			expected: execExpected{code: 0, stdout: "app 1.2.3\n"},
		},
		{
			name: "version operand without version set",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:     "app",
						Commands: []*cli.Command{{Name: "serve", Run: execEcho(out, "serve")}},
					}
				},
				args: []string{"version"},
			},
			expected: execExpected{
				code:   2,
				stderr: "app: unknown command: version\nRun 'app --help' for usage.\n",
			},
		},
		{
			name: "version flag after subcommand",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:     "app",
						Version:  "1.2.3",
						Commands: []*cli.Command{{Name: "serve", Run: execEcho(out, "serve")}},
					}
				},
				args: []string{"serve", "--version"},
			},
			expected: execExpected{
				code:   2,
				stderr: "flag provided but not defined: -version\nRun 'app serve --help' for usage.\n",
			},
		},
		{
			name: "user-declared version flag wins over auto flag",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					var v string
					return &cli.Command{
						Name:    "app",
						Version: "9.9.9",
						Flags: func(fs *cli.FlagSet) {
							fs.StringVar(&v, "version", "", "user version")
						},
						Run: func(_ context.Context, _ []string) error {
							execPrintf(out, "flag version=%s\n", v)
							return nil
						},
					}
				},
				args: []string{"-version", "custom"},
			},
			expected: execExpected{code: 0, stdout: "flag version=custom\n"},
		},
		{
			name: "help builtin for root",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:  "app",
						Short: "demo app",
						Commands: []*cli.Command{
							{Name: "serve", Short: "start the server", Run: execEcho(out, "serve")},
						},
					}
				},
				args: []string{"help"},
			},
			expected: execExpected{
				code: 0,
				stdout: `demo app

Usage:
  app [command]

Commands:
  serve  start the server
  help   help for app or a command
`,
			},
		},
		{
			name: "help builtin for subcommand",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name: "app",
						Commands: []*cli.Command{
							{Name: "serve", Short: "start the server", Run: execEcho(out, "serve")},
						},
					}
				},
				args: []string{"help", "serve"},
			},
			expected: execExpected{code: 0, stdout: "start the server\n\nUsage:\n  app serve\n"},
		},
		{
			name: "help builtin unknown command",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:     "app",
						Commands: []*cli.Command{{Name: "serve", Run: execEcho(out, "serve")}},
					}
				},
				args: []string{"help", "nope"},
			},
			expected: execExpected{code: 2, stderr: "app: unknown command: nope\nRun 'app --help' for usage.\n"},
		},
		{
			name: "root flag before subcommand name",
			input: execInput{
				build: execLevelTree,
				args:  []string{"-log-level", "debug", "serve"},
			},
			expected: execExpected{code: 0, stdout: "log-level=debug\n"},
		},
		{
			name: "root flag after subcommand name",
			input: execInput{
				build: execLevelTree,
				args:  []string{"serve", "-log-level", "debug"},
			},
			expected: execExpected{code: 0, stdout: "log-level=debug\n"},
		},
		{
			name: "later parse wins when flag set at both levels",
			input: execInput{
				build: execLevelTree,
				args:  []string{"-log-level", "early", "serve", "-log-level", "late"},
			},
			expected: execExpected{code: 0, stdout: "log-level=late\n"},
		},
		{
			name: "environment applies when flag not set",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_LOG_LEVEL", "fromenv")
			},
			input: execInput{
				build: execLevelTree,
				args:  []string{"serve"},
			},
			expected: execExpected{code: 0, stdout: "log-level=fromenv\n"},
		},
		{
			name: "explicit flag beats environment",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_LOG_LEVEL", "fromenv")
			},
			input: execInput{
				build: execLevelTree,
				args:  []string{"-log-level", "explicit", "serve"},
			},
			expected: execExpected{code: 0, stdout: "log-level=explicit\n"},
		},
		{
			name: "environment resolution error",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_LOG_LEVEL", "fromenv")
				t.Setenv("APP_LOG_LEVEL_FILE", "/nonexistent")
			},
			input: execInput{
				build: execLevelTree,
				args:  []string{"serve"},
			},
			expected: execExpected{
				code:   2,
				stderr: "cli: conflicting environment configuration: both APP_LOG_LEVEL and APP_LOG_LEVEL_FILE are set\n",
			},
		},
		{
			name: "secret flag parse error omits the value",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name: "app",
						Flags: func(fs *cli.FlagSet) {
							fs.Int("token", 0, "api token")
							fs.Secret("token")
						},
						Run: execEcho(out, "root"),
					}
				},
				args: []string{"-token", "hunter2"},
			},
			expected: execExpected{
				code:   2,
				stderr: "invalid value for flag -token\nRun 'app --help' for usage.\n",
			},
		},
		{
			name: "secret bool flag parse error omits the value",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name: "app",
						Flags: func(fs *cli.FlagSet) {
							fs.Bool("magic", false, "magic mode")
							fs.Secret("magic")
						},
						Run: execEcho(out, "root"),
					}
				},
				args: []string{"-magic=hunter2"},
			},
			expected: execExpected{
				code:   2,
				stderr: "invalid value for flag -magic\nRun 'app --help' for usage.\n",
			},
		},
		{
			name: "double dash stops subcommand dispatch",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name: "app",
						Run:  execEcho(out, "root"),
						Commands: []*cli.Command{
							{Name: "serve", Run: execEcho(out, "serve")},
						},
					}
				},
				args: []string{"--", "serve"},
			},
			expected: execExpected{code: 0, stdout: "root [\"serve\"]\n"},
		},
		{
			name: "double dash stops the builtins",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:    "app",
						Version: "1.2.3",
						Run:     execEcho(out, "root"),
						Commands: []*cli.Command{
							{Name: "serve", Run: execEcho(out, "serve")},
						},
					}
				},
				args: []string{"--", "version"},
			},
			expected: execExpected{code: 0, stdout: "root [\"version\"]\n"},
		},
		{
			name: "double dash below a subcommand keeps flag-like operands",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name: "app",
						Commands: []*cli.Command{
							{Name: "serve", Run: execEcho(out, "serve")},
						},
					}
				},
				args: []string{"serve", "--", "-x"},
			},
			expected: execExpected{code: 0, stdout: "serve [\"-x\"]\n"},
		},
		{
			name: "user subcommand named help wins over the builtin",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name: "app",
						Commands: []*cli.Command{
							{Name: "help", Run: execEcho(out, "user-help")},
						},
					}
				},
				args: []string{"help"},
			},
			expected: execExpected{code: 0, stdout: "user-help []\n"},
		},
		{
			name: "user subcommand named version wins over the builtin",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:    "app",
						Version: "1.2.3",
						Commands: []*cli.Command{
							{Name: "version", Run: execEcho(out, "user-version")},
						},
					}
				},
				args: []string{"version"},
			},
			expected: execExpected{code: 0, stdout: "user-version []\n"},
		},
		{
			name: "plain flag parse error echoes the value",
			input: execInput{
				build: func(out io.Writer) *cli.Command {
					return &cli.Command{
						Name:  "app",
						Flags: func(fs *cli.FlagSet) { fs.Int("port", 0, "listen port") },
						Run:   execEcho(out, "root"),
					}
				},
				args: []string{"-port", "abc"},
			},
			expected: execExpected{
				code:   2,
				stderr: "invalid value \"abc\" for flag -port: parse error\nRun 'app --help' for usage.\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFunc != nil {
				tt.mockFunc(t)
			}
			var stdout, stderr bytes.Buffer
			cmd := tt.input.build(&stdout)
			cli.SetIO(cmd, &stdout, &stderr)
			code := cli.ExecuteArgs(context.Background(), cmd, tt.input.args)
			if code != tt.expected.code {
				t.Errorf("exit code = %d, want %d", code, tt.expected.code)
			}
			if got := stdout.String(); got != tt.expected.stdout {
				t.Errorf("stdout = %q, want %q", got, tt.expected.stdout)
			}
			if got := stderr.String(); got != tt.expected.stderr {
				t.Errorf("stderr = %q, want %q", got, tt.expected.stderr)
			}
		})
	}

	t.Run("redeclared inherited flag panics", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		cmd := &cli.Command{
			Name:  "app",
			Flags: func(fs *cli.FlagSet) { fs.String("log-level", "info", "log level") },
			Commands: []*cli.Command{{
				Name:  "serve",
				Flags: func(fs *cli.FlagSet) { fs.String("log-level", "info", "log level") },
				Run:   func(_ context.Context, _ []string) error { return nil },
			}},
		}
		cli.SetIO(cmd, &stdout, &stderr)
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("ExecuteArgs did not panic on redeclared inherited flag")
			}
			const want = "cli: flag redeclared in app serve: -log-level"
			if r != want {
				t.Fatalf("panic = %v, want %v", r, want)
			}
		}()
		cli.ExecuteArgs(context.Background(), cmd, []string{"serve"})
	})

	t.Run("Execute reads os.Args and defaults to the process streams", func(t *testing.T) {
		saved := os.Args
		defer func() { os.Args = saved }()
		os.Args = []string{"app"}

		ran := false
		cmd := &cli.Command{
			Name: "app",
			Run: func(_ context.Context, args []string) error {
				ran = len(args) == 0
				return nil
			},
		}
		if got := cmd.Execute(context.Background()); got != 0 {
			t.Fatalf("Execute = %d, want 0", got)
		}
		if !ran {
			t.Fatal("Run was not called with empty operands")
		}
	})
}

// execLevelTree builds an app with a root log-level flag and a serve
// subcommand whose Run reports the resolved value.
func execLevelTree(out io.Writer) *cli.Command {
	var level string
	return &cli.Command{
		Name:  "app",
		Flags: func(fs *cli.FlagSet) { fs.StringVar(&level, "log-level", "info", "log level") },
		Commands: []*cli.Command{{
			Name: "serve",
			Run: func(_ context.Context, _ []string) error {
				execPrintf(out, "log-level=%s\n", level)
				return nil
			},
		}},
	}
}
