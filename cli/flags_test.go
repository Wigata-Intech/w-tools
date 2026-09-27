package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/netip"
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

// flagsListInput is a list flag's default and the command line that
// sets it.
type flagsListInput[T any] struct {
	def  []T
	args []string
}

// flagsListExpected is the exit code and, on exit 0, the observed list
// ("%v nil=%t"), otherwise stderr.
type flagsListExpected struct {
	code int
	out  string
}

// flagsListRun declares a list flag via declare, executes with args, and
// returns what flagsListExpected describes.
func flagsListRun[T any](t *testing.T, declare func(fs *cli.FlagSet) *[]T, args []string) flagsListExpected {
	t.Helper()
	var p *[]T
	var got string
	root := &cli.Command{
		Name:  "app",
		Flags: func(fs *cli.FlagSet) { p = declare(fs) },
		Run: func(context.Context, []string) error {
			got = fmt.Sprintf("%v nil=%t", *p, *p == nil)
			return nil
		},
	}
	code, stderr := flagsRun(t, root, args)
	if code != 0 {
		return flagsListExpected{code: code, out: stderr}
	}
	return flagsListExpected{out: got}
}

func TestPrefixList(t *testing.T) {
	def := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	tests := []struct {
		name     string
		input    flagsListInput[netip.Prefix]
		expected flagsListExpected
	}{
		{
			name:     "default holds when unset",
			input:    flagsListInput[netip.Prefix]{def: def},
			expected: flagsListExpected{out: "[10.0.0.0/8] nil=false"},
		},
		{
			name:     "blank value sets an empty non-nil list",
			input:    flagsListInput[netip.Prefix]{def: def, args: []string{"-p", " "}},
			expected: flagsListExpected{out: "[] nil=false"},
		},
		{
			name:     "entries parse with surrounding whitespace trimmed",
			input:    flagsListInput[netip.Prefix]{args: []string{"-p", " 10.0.0.0/8 , 2001:db8::/32"}},
			expected: flagsListExpected{out: "[10.0.0.0/8 2001:db8::/32] nil=false"},
		},
		{
			name:  "empty entry is rejected",
			input: flagsListInput[netip.Prefix]{args: []string{"-p", "10.0.0.0/8,"}},
			expected: flagsListExpected{
				code: 2,
				out:  "invalid value \"10.0.0.0/8,\" for flag -p: empty list entry\nRun 'app --help' for usage.\n",
			},
		},
		{
			name:  "one malformed entry rejects the whole value",
			input: flagsListInput[netip.Prefix]{args: []string{"-p", "10.0.0.0/8,bad"}},
			expected: flagsListExpected{
				code: 2,
				out:  "invalid value \"10.0.0.0/8,bad\" for flag -p: netip.ParsePrefix(\"bad\"): no '/'\nRun 'app --help' for usage.\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flagsListRun(t, func(fs *cli.FlagSet) *[]netip.Prefix {
				return fs.PrefixList("p", tt.input.def, "")
			}, tt.input.args)
			if got != tt.expected {
				t.Errorf("got %+v, expected %+v", got, tt.expected)
			}
		})
	}
}

func TestPrefixListVar(t *testing.T) {
	tests := []struct {
		name     string
		input    flagsListInput[netip.Prefix]
		expected flagsListExpected
	}{
		{
			name:     "default stored in the variable",
			input:    flagsListInput[netip.Prefix]{def: []netip.Prefix{netip.MustParsePrefix("::1/128")}},
			expected: flagsListExpected{out: "[::1/128] nil=false"},
		},
		{
			name:     "command line replaces the default",
			input:    flagsListInput[netip.Prefix]{args: []string{"-p", "192.168.0.0/16"}},
			expected: flagsListExpected{out: "[192.168.0.0/16] nil=false"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flagsListRun(t, func(fs *cli.FlagSet) *[]netip.Prefix {
				var v []netip.Prefix
				fs.PrefixListVar(&v, "p", tt.input.def, "")
				return &v
			}, tt.input.args)
			if got != tt.expected {
				t.Errorf("got %+v, expected %+v", got, tt.expected)
			}
		})
	}
}

func TestStringList(t *testing.T) {
	tests := []struct {
		name     string
		input    flagsListInput[string]
		expected flagsListExpected
	}{
		{
			name:     "default holds when unset",
			input:    flagsListInput[string]{def: []string{"x"}},
			expected: flagsListExpected{out: "[x] nil=false"},
		},
		{
			name:     "blank value sets an empty non-nil list",
			input:    flagsListInput[string]{def: []string{"x"}, args: []string{"-l", "  "}},
			expected: flagsListExpected{out: "[] nil=false"},
		},
		{
			name:     "entries split on commas with surrounding whitespace trimmed",
			input:    flagsListInput[string]{args: []string{"-l", " a , b ,c"}},
			expected: flagsListExpected{out: "[a b c] nil=false"},
		},
		{
			name:     "interior whitespace is kept",
			input:    flagsListInput[string]{args: []string{"-l", "a  b,c"}},
			expected: flagsListExpected{out: "[a  b c] nil=false"},
		},
		{
			name:     "repeated flag keeps the last value",
			input:    flagsListInput[string]{args: []string{"-l", "a,b", "-l", "c"}},
			expected: flagsListExpected{out: "[c] nil=false"},
		},
		{
			name:  "empty entry is rejected",
			input: flagsListInput[string]{args: []string{"-l", "a,,b"}},
			expected: flagsListExpected{
				code: 2,
				out:  "invalid value \"a,,b\" for flag -l: empty list entry\nRun 'app --help' for usage.\n",
			},
		},
		{
			name:  "trailing comma is rejected",
			input: flagsListInput[string]{args: []string{"-l", "a,"}},
			expected: flagsListExpected{
				code: 2,
				out:  "invalid value \"a,\" for flag -l: empty list entry\nRun 'app --help' for usage.\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flagsListRun(t, func(fs *cli.FlagSet) *[]string {
				return fs.StringList("l", tt.input.def, "")
			}, tt.input.args)
			if got != tt.expected {
				t.Errorf("got %+v, expected %+v", got, tt.expected)
			}
		})
	}
}

func TestStringListVar(t *testing.T) {
	tests := []struct {
		name     string
		input    flagsListInput[string]
		expected flagsListExpected
	}{
		{
			name:     "default stored in the variable",
			input:    flagsListInput[string]{def: []string{"a", "b"}},
			expected: flagsListExpected{out: "[a b] nil=false"},
		},
		{
			name:     "command line replaces the default",
			input:    flagsListInput[string]{def: []string{"a"}, args: []string{"-l", "c"}},
			expected: flagsListExpected{out: "[c] nil=false"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flagsListRun(t, func(fs *cli.FlagSet) *[]string {
				var v []string
				fs.StringListVar(&v, "l", tt.input.def, "")
				return &v
			}, tt.input.args)
			if got != tt.expected {
				t.Errorf("got %+v, expected %+v", got, tt.expected)
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
