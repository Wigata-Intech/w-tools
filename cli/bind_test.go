package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/cli"
)

// bindAll is the every-supported-type struct for the positive cases.
type bindAll struct {
	Name     string        `cli:"name"  usage:"a string"`
	Loud     bool          `cli:"loud"`
	Count    int           `cli:"count"`
	Big      int64         `cli:"big"`
	Ratio    float64       `cli:"ratio"`
	Wait     time.Duration `cli:"wait"`
	hidden   string        //nolint:unused // proves unexported fields are skipped
	Skipped  string        `cli:"-"`
	Custom   bindUpper     `cli:"custom"`
	HTTPAddr string        // no tag: name derives to http-addr
}

// BindRegion is an exported section type, embedded to prove embedded
// sections recurse like named ones.
type BindRegion struct {
	Region string
}

// bindNever is a flag.Value field whose Set always fails.
type bindNever struct{}

func (bindNever) String() string   { return "" }
func (bindNever) Set(string) error { return errExecBoom }

// bindUpper is a flag.Value field: stores its input uppercased.
type bindUpper struct{ v string }

func (u *bindUpper) String() string { return u.v }
func (u *bindUpper) Set(s string) error {
	u.v = strings.ToUpper(s)
	return nil
}

// bindRun executes a root whose flags come from cli.Bind over cfg and
// returns the exit code, Run's observation via observe, and stderr.
func bindRun(t *testing.T, cfg any, observe func() string, args []string) (int, string, string) {
	t.Helper()
	var got string
	root := &cli.Command{
		Name:  "app",
		Flags: func(fs *cli.FlagSet) { fs.Bind(cfg) },
		Run: func(context.Context, []string) error {
			got = observe()
			return nil
		},
	}
	var out, errw bytes.Buffer
	cli.SetIO(root, &out, &errw)
	code := cli.ExecuteArgs(context.Background(), root, args)
	return code, got, errw.String()
}

// bindFail runs bindRun over cfg and args, asserting exit 2 with exactly
// want on stderr.
func bindFail(t *testing.T, cfg any, args []string, want string) {
	t.Helper()
	code, _, stderr := bindRun(t, cfg, func() string { return "" }, args)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr %q)", code, stderr)
	}
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestBind(t *testing.T) {
	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		args     []string
		expected string
	}{
		{
			name: "every supported type set from the command line",
			args: []string{
				"-name", "x", "-loud", "-count", "3", "-big", "9000000000",
				"-ratio", "1.5", "-wait", "2s", "-custom", "up", "-http-addr", ":1",
			},
			expected: "x true 3 9000000000 1.5 2s UP :1",
		},
		{
			name: "derived kebab name binds its environment variable",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_HTTP_ADDR", ":7070")
			},
			expected: "d false 0 0 0 0s  :7070",
		},
		{
			name:     "defaults hold when nothing is set",
			expected: "d false 0 0 0 0s  ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFunc != nil {
				tt.mockFunc(t)
			}
			cfg := bindAll{Name: "d"}
			code, got, stderr := bindRun(t, &cfg, func() string {
				return fmt.Sprintf("%s %t %d %d %g %s %s %s",
					cfg.Name, cfg.Loud, cfg.Count, cfg.Big, cfg.Ratio, cfg.Wait, cfg.Custom.v, cfg.HTTPAddr)
			}, tt.args)
			if code != 0 {
				t.Fatalf("exit code = %d, stderr %q", code, stderr)
			}
			if got != tt.expected {
				t.Errorf("observed = %q, want %q", got, tt.expected)
			}
		})
	}

	t.Run("section field resolves from every layer under its leaf name", func(t *testing.T) {
		type cfgT struct {
			HTTP struct {
				Addr string `cli:"http-addr"`
			}
		}
		layers := []struct {
			name  string
			setup func(t *testing.T) []string
		}{
			{"command line", func(t *testing.T) []string { t.Helper(); return []string{"--http-addr", ":1"} }},
			{"environment", func(t *testing.T) []string {
				t.Helper()
				t.Setenv("APP_HTTP_ADDR", ":1")
				return nil
			}},
			{"config file", func(t *testing.T) []string {
				t.Helper()
				path := filepath.Join(t.TempDir(), "c.json")
				if err := os.WriteFile(path, []byte(`{"http-addr": ":1"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				return []string{"-config", path}
			}},
		}
		for _, layer := range layers {
			t.Run(layer.name, func(t *testing.T) {
				args := layer.setup(t)
				var c cfgT
				var got string
				root := &cli.Command{
					Name:   "app",
					Config: cli.ConfigFile{Flag: "config"},
					Flags:  func(fs *cli.FlagSet) { fs.Bind(&c) },
					Run: func(context.Context, []string) error {
						got = c.HTTP.Addr
						return nil
					},
				}
				var out, errw bytes.Buffer
				cli.SetIO(root, &out, &errw)
				if code := cli.ExecuteArgs(context.Background(), root, args); code != 0 {
					t.Fatalf("exit code = %d, stderr %q", code, errw.String())
				}
				if got != ":1" {
					t.Errorf("HTTP.Addr = %q, want %q", got, ":1")
				}
			})
		}
	})

	t.Run("sections recurse at any depth and leave leaf structs alone", func(t *testing.T) {
		type cfgT struct {
			BindRegion

			Log struct {
				Level string `default:"info"`
			}
			Outer struct {
				Inner struct {
					Deep int
				}
				Custom bindUpper
			}
			Skipped struct {
				Never string
			} `cli:"-"`
			private struct { //nolint:unused // proves unexported sections are skipped
				Hidden string
			}
		}
		var c cfgT
		code, got, stderr := bindRun(t, &c, func() string {
			return fmt.Sprintf("%s %s %d %s", c.Region, c.Log.Level, c.Outer.Inner.Deep, c.Outer.Custom.v)
		}, []string{"-region", "eu", "-deep", "3", "-custom", "up"})
		if code != 0 {
			t.Fatalf("exit code = %d, stderr %q", code, stderr)
		}
		const want = "eu info 3 UP"
		if got != want {
			t.Errorf("observed = %q, want %q", got, want)
		}
		bindFail(t, &cfgT{}, []string{"-never", "x"},
			"flag provided but not defined: -never\nRun 'app --help' for usage.\n")
		bindFail(t, &cfgT{}, []string{"-hidden", "x"},
			"flag provided but not defined: -hidden\nRun 'app --help' for usage.\n")
	})

	t.Run("required inside a section fails startup by flag name", func(t *testing.T) {
		type cfgT struct {
			DB struct {
				Password string `cli:"db-password,secret,required"`
			}
		}
		bindFail(t, &cfgT{}, nil, "cli: missing required configuration: -db-password (env APP_DB_PASSWORD)\n")
	})

	t.Run("list fields bind from default tags and the command line", func(t *testing.T) {
		type cfgT struct {
			Hosts   []string       `default:"a, b"`
			Trusted []netip.Prefix `cli:"trusted"  default:"10.0.0.0/8"`
		}
		for _, run := range []struct {
			args []string
			want string
		}{
			{nil, "[a b] [10.0.0.0/8]"},
			{[]string{"-hosts", "c", "-trusted", "192.168.0.0/16, ::1/128"}, "[c] [192.168.0.0/16 ::1/128]"},
		} {
			var c cfgT
			code, got, stderr := bindRun(t, &c, func() string {
				return fmt.Sprintf("%v %v", c.Hosts, c.Trusted)
			}, run.args)
			if code != 0 {
				t.Fatalf("exit code = %d, stderr %q", code, stderr)
			}
			if got != run.want {
				t.Errorf("args %q: observed = %q, want %q", run.args, got, run.want)
			}
		}
	})

	t.Run("malformed prefix entry fails resolution with exit 2", func(t *testing.T) {
		type cfgT struct {
			HTTP struct {
				Trusted []netip.Prefix `cli:"trusted"`
			}
		}
		t.Run("command line", func(t *testing.T) {
			bindFail(t, &cfgT{}, []string{"--trusted", "10.0.0.0/8,bad"},
				"invalid value \"10.0.0.0/8,bad\" for flag -trusted: netip.ParsePrefix(\"bad\"): no '/'\n"+
					"Run 'app --help' for usage.\n")
		})
		t.Run("environment", func(t *testing.T) {
			t.Setenv("APP_TRUSTED", "10.0.0.0/8,bad")
			bindFail(t, &cfgT{}, nil, "cli: invalid value \"10.0.0.0/8,bad\" for flag -trusted from APP_TRUSTED\n")
		})
	})

	t.Run("default tags apply per type", func(t *testing.T) {
		type cfgT struct {
			Mode string        `cli:"mode" default:"fast"`
			On   bool          `cli:"on"   default:"true"`
			N    int           `cli:"n"    default:"7"`
			B    int64         `cli:"b"    default:"8"`
			R    float64       `cli:"r"    default:"2.5"`
			W    time.Duration `cli:"w"    default:"3s"`
			C    bindUpper     `cli:"c"    default:"low"`
		}
		var c cfgT
		code, got, stderr := bindRun(t, &c, func() string {
			return fmt.Sprintf("%s %t %d %d %g %s %s", c.Mode, c.On, c.N, c.B, c.R, c.W, c.C.v)
		}, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, stderr %q", code, stderr)
		}
		const want = "fast true 7 8 2.5 3s LOW"
		if got != want {
			t.Errorf("observed = %q, want %q", got, want)
		}
	})

	t.Run("skipped and unexported fields declare no flags", func(t *testing.T) {
		var c bindAll
		code, _, stderr := bindRun(t, &c, func() string { return "" }, []string{"-skipped", "x"})
		if code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		const want = "flag provided but not defined: -skipped\nRun 'app --help' for usage.\n"
		if stderr != want {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
	})

	t.Run("secret option sanitizes parse errors", func(t *testing.T) {
		type cfgT struct {
			Token int `cli:"token,secret"`
		}
		var c cfgT
		code, _, stderr := bindRun(t, &c, func() string { return "" }, []string{"-token", "hunter2"})
		if code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		const want = "invalid value for flag -token\nRun 'app --help' for usage.\n"
		if stderr != want {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
	})

	t.Run("required satisfied by each layer", func(t *testing.T) {
		type cfgT struct {
			Key string `cli:"key,required"`
		}
		layers := []struct {
			name  string
			setup func(t *testing.T) []string
		}{
			{"command line", func(t *testing.T) []string { t.Helper(); return []string{"-key", "v"} }},
			{"environment", func(t *testing.T) []string {
				t.Helper()
				t.Setenv("APP_KEY", "v")
				return nil
			}},
			{"config file", func(t *testing.T) []string {
				t.Helper()
				path := filepath.Join(t.TempDir(), "c.json")
				if err := os.WriteFile(path, []byte(`{"key": "v"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				return []string{"-config", path}
			}},
		}
		for _, layer := range layers {
			t.Run(layer.name, func(t *testing.T) {
				args := layer.setup(t)
				var c cfgT
				var got string
				root := &cli.Command{
					Name:   "app",
					Config: cli.ConfigFile{Flag: "config"},
					Flags:  func(fs *cli.FlagSet) { fs.Bind(&c) },
					Run: func(context.Context, []string) error {
						got = c.Key
						return nil
					},
				}
				var out, errw bytes.Buffer
				cli.SetIO(root, &out, &errw)
				if code := cli.ExecuteArgs(context.Background(), root, args); code != 0 {
					t.Fatalf("exit code = %d, stderr %q", code, errw.String())
				}
				if got != "v" {
					t.Errorf("Key = %q, want %q", got, "v")
				}
			})
		}
	})

	t.Run("required missing fails startup listing every gap", func(t *testing.T) {
		type cfgT struct {
			Key    string `cli:"key,required"`
			Second string `cli:"second,required"`
		}
		var c cfgT
		code, _, stderr := bindRun(t, &c, func() string { return "" }, nil)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		const want = "cli: missing required configuration: -key (env APP_KEY), -second (env APP_SECOND)\n"
		if stderr != want {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
	})

	panics := []struct {
		name string
		cfg  any
		want string
	}{
		{"non-pointer", struct{}{}, "cli: Bind requires a non-nil pointer to a struct"},
		{"nil pointer", (*bindAll)(nil), "cli: Bind requires a non-nil pointer to a struct"},
		{"pointer to non-struct", new(int), "cli: Bind requires a non-nil pointer to a struct"},
		{"text unmarshaler struct is a leaf, not a section", &struct {
			At time.Time
		}{}, "cli: Bind field At: unsupported type time.Time"},
		{"tagged struct is a leaf, not a section", &struct {
			S struct{ A string } `cli:"s"`
		}{}, "cli: Bind field S: unsupported type struct { A string }"},
		{"pointer to struct is a leaf, not a section", &struct {
			P *struct{ A string }
		}{}, "cli: Bind field P: unsupported type *struct { A string }"},
		{"unsupported field type", &struct {
			M map[string]string `cli:"m"`
		}{}, "cli: Bind field M: unsupported type map[string]string"},
		{"required with default", &struct {
			K string `cli:"k,required" default:"x"`
		}{}, "cli: Bind field K: required and default are contradictory"},
		{"unknown option", &struct {
			K string `cli:"k,nope"`
		}{}, `cli: Bind field K: unknown option "nope"`},
		{"unknown option inside a section names the field path", &struct {
			HTTP struct {
				K string `cli:"k,nope"`
			}
		}{}, `cli: Bind field HTTP.K: unknown option "nope"`},
		{"two fields naming one flag", &struct {
			HTTPAddr string
			Addr     string `cli:"http-addr"`
		}{}, "cli: Bind field Addr: flag -http-addr already declared"},
		{"two sections naming one flag", &struct {
			HTTP    struct{ Addr string }
			Metrics struct{ Addr string }
		}{}, "cli: Bind field Metrics.Addr: flag -addr already declared"},
		{"bad default", &struct {
			N int `cli:"n" default:"abc"`
		}{}, `cli: Bind field N: bad default "abc": strconv.Atoi: parsing "abc": invalid syntax`},
		{"bad duration default", &struct {
			W time.Duration `cli:"w" default:"abc"`
		}{}, `cli: Bind field W: bad default "abc": time: invalid duration "abc"`},
		{"bad bool default", &struct {
			B bool `cli:"b" default:"abc"`
		}{}, `cli: Bind field B: bad default "abc": strconv.ParseBool: parsing "abc": invalid syntax`},
		{"bad int64 default", &struct {
			B int64 `cli:"b" default:"abc"`
		}{}, `cli: Bind field B: bad default "abc": strconv.ParseInt: parsing "abc": invalid syntax`},
		{"bad float64 default", &struct {
			R float64 `cli:"r" default:"abc"`
		}{}, `cli: Bind field R: bad default "abc": strconv.ParseFloat: parsing "abc": invalid syntax`},
		{"bad string list default", &struct {
			L []string `cli:"l" default:"a,,b"`
		}{}, `cli: Bind field L: bad default "a,,b": empty list entry`},
		{"bad prefix list default", &struct {
			P []netip.Prefix `cli:"p" default:"10.0.0.0/8,bad"`
		}{}, `cli: Bind field P: bad default "10.0.0.0/8,bad": netip.ParsePrefix("bad"): no '/'`},
		{"flag value rejecting its default", &struct {
			V bindNever `cli:"v" default:"x"`
		}{}, `cli: Bind field V: bad default "x": boom`},
	}
	for _, tt := range panics {
		t.Run("panic on "+tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("Bind did not panic")
				}
				if r != tt.want {
					t.Fatalf("panic = %v, want %v", r, tt.want)
				}
			}()
			cmd := &cli.Command{
				Name:  "app",
				Flags: func(fs *cli.FlagSet) { fs.Bind(tt.cfg) },
				Run:   func(context.Context, []string) error { return nil },
			}
			cli.SetIO(cmd, io.Discard, io.Discard)
			cli.ExecuteArgs(context.Background(), cmd, nil)
		})
	}
}

func TestRequired(t *testing.T) {
	t.Run("manually marked flag enforces like a bound one", func(t *testing.T) {
		var v string
		cmd := &cli.Command{
			Name: "app",
			Flags: func(fs *cli.FlagSet) {
				fs.StringVar(&v, "key", "", "")
				fs.Required("key")
			},
			Run: func(context.Context, []string) error { return nil },
		}
		var out, errw bytes.Buffer
		cli.SetIO(cmd, &out, &errw)
		if code := cli.ExecuteArgs(context.Background(), cmd, nil); code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		const want = "cli: missing required configuration: -key (env APP_KEY)\n"
		if got := errw.String(); got != want {
			t.Errorf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("non-zero default panics", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("Required did not panic")
			}
			const want = "cli: Required on a flag with a non-zero default: -key"
			if r != want {
				t.Fatalf("panic = %v, want %v", r, want)
			}
		}()
		cmd := &cli.Command{
			Name: "app",
			Flags: func(fs *cli.FlagSet) {
				fs.String("key", "fallback", "")
				fs.Required("key")
			},
			Run: func(context.Context, []string) error { return nil },
		}
		cli.SetIO(cmd, io.Discard, io.Discard)
		cli.ExecuteArgs(context.Background(), cmd, nil)
	})

	t.Run("undeclared flag panics", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("Required did not panic")
			}
			const want = "cli: Required on undeclared flag: -nope"
			if r != want {
				t.Fatalf("panic = %v, want %v", r, want)
			}
		}()
		cmd := &cli.Command{
			Name:  "app",
			Flags: func(fs *cli.FlagSet) { fs.Required("nope") },
			Run:   func(context.Context, []string) error { return nil },
		}
		cli.SetIO(cmd, io.Discard, io.Discard)
		cli.ExecuteArgs(context.Background(), cmd, nil)
	})
}
