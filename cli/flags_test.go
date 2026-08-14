package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/cli"
)

// flagsRun executes root with args and returns the exit code and stderr.
func flagsRun(t *testing.T, root *cli.Command, args []string) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	cli.SetIO(root, &out, &errw)
	code := cli.ExecuteArgs(context.Background(), root, args)
	return code, errw.String()
}

// flagsWriteFile writes content to a fresh temp file and returns its path.
func flagsWriteFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "value")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// flagsUpperValue is a flag.Value that uppercases everything set on it.
type flagsUpperValue struct{ v string }

func (u *flagsUpperValue) String() string { return u.v }

func (u *flagsUpperValue) Set(s string) error {
	u.v = strings.ToUpper(s)
	return nil
}

// flagsMirrorInput pairs a flag declaration with the command line that
// sets it; declare returns an observer read inside Run.
type flagsMirrorInput struct {
	declare func(fs *cli.FlagSet) func() string
	args    []string
}

func TestFlagSetMirrors(t *testing.T) {
	tests := []struct {
		name     string
		input    flagsMirrorInput
		expected string
	}{
		{
			name: "Bool",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					p := fs.Bool("b", false, "")
					return func() string { return strconv.FormatBool(*p) }
				},
				args: []string{"-b"},
			},
			expected: "true",
		},
		{
			name: "BoolVar",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					var v bool
					fs.BoolVar(&v, "b", false, "")
					return func() string { return strconv.FormatBool(v) }
				},
				args: []string{"-b"},
			},
			expected: "true",
		},
		{
			name: "Duration",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					p := fs.Duration("d", 0, "")
					return func() string { return p.String() }
				},
				args: []string{"-d", "1500ms"},
			},
			expected: "1.5s",
		},
		{
			name: "DurationVar",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					var v time.Duration
					fs.DurationVar(&v, "d", 0, "")
					return func() string { return v.String() }
				},
				args: []string{"-d", "1500ms"},
			},
			expected: "1.5s",
		},
		{
			name: "Float64",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					p := fs.Float64("f", 0, "")
					return func() string { return strconv.FormatFloat(*p, 'g', -1, 64) }
				},
				args: []string{"-f", "2.5"},
			},
			expected: "2.5",
		},
		{
			name: "Float64Var",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					var v float64
					fs.Float64Var(&v, "f", 0, "")
					return func() string { return strconv.FormatFloat(v, 'g', -1, 64) }
				},
				args: []string{"-f", "2.5"},
			},
			expected: "2.5",
		},
		{
			name: "Int",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					p := fs.Int("i", 0, "")
					return func() string { return strconv.Itoa(*p) }
				},
				args: []string{"-i", "42"},
			},
			expected: "42",
		},
		{
			name: "IntVar",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					var v int
					fs.IntVar(&v, "i", 0, "")
					return func() string { return strconv.Itoa(v) }
				},
				args: []string{"-i", "42"},
			},
			expected: "42",
		},
		{
			name: "Int64",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					p := fs.Int64("i", 0, "")
					return func() string { return strconv.FormatInt(*p, 10) }
				},
				args: []string{"-i", "9000000000"},
			},
			expected: "9000000000",
		},
		{
			name: "Int64Var",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					var v int64
					fs.Int64Var(&v, "i", 0, "")
					return func() string { return strconv.FormatInt(v, 10) }
				},
				args: []string{"-i", "9000000000"},
			},
			expected: "9000000000",
		},
		{
			name: "String",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					p := fs.String("s", "", "")
					return func() string { return *p }
				},
				args: []string{"-s", "hello"},
			},
			expected: "hello",
		},
		{
			name: "StringVar",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					var v string
					fs.StringVar(&v, "s", "", "")
					return func() string { return v }
				},
				args: []string{"-s", "hello"},
			},
			expected: "hello",
		},
		{
			name: "Var",
			input: flagsMirrorInput{
				declare: func(fs *cli.FlagSet) func() string {
					u := &flagsUpperValue{}
					fs.Var(u, "u", "")
					return u.String
				},
				args: []string{"-u", "abc"},
			},
			expected: "ABC",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var observe func() string
			var got string
			root := &cli.Command{
				Name:  "app",
				Flags: func(fs *cli.FlagSet) { observe = tt.input.declare(fs) },
				Run: func(context.Context, []string) error {
					got = observe()
					return nil
				},
			}
			code, stderr := flagsRun(t, root, tt.input.args)
			if code != 0 {
				t.Fatalf("exit code = %d, expected 0; stderr %q", code, stderr)
			}
			if got != tt.expected {
				t.Errorf("value = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestSecret(t *testing.T) {
	t.Run("panic on undeclared flag", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic, got none")
			}
			expected := "cli: Secret on undeclared flag: -x"
			if r != expected {
				t.Fatalf("panic = %v, expected %q", r, expected)
			}
		}()
		root := &cli.Command{
			Name:  "app",
			Flags: func(fs *cli.FlagSet) { fs.Secret("x") },
			Run:   func(context.Context, []string) error { return nil },
		}
		cli.SetIO(root, io.Discard, io.Discard)
		cli.ExecuteArgs(context.Background(), root, nil)
	})
}
