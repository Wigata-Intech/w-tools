package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/Wigata-Intech/w-tools/cli"
)

// fuzzMap reimplements the documented env-name mapping — uppercase, every
// non-alphanumeric rune to underscore — as the oracle FuzzEnvName binds
// against. If the implementation ever diverges from this spec, the
// binding proof below fails.
func fuzzMap(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - ('a' - 'A'))
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// FuzzEnvName proves the mapping end to end for arbitrary prefixes and
// flag names: the env var the documentation says binds a flag really does
// bind it.
func FuzzEnvName(f *testing.F) {
	f.Add("my-service", "http-addr")
	f.Add("", "x")
	f.Add(cli.NoPrefix, "http-addr")
	f.Add("custom", "log.level")
	f.Add("Ωmega", "π-flag")
	f.Fuzz(func(t *testing.T, prefix, flagName string) {
		if flagName == "" || strings.HasPrefix(flagName, "-") ||
			strings.ContainsAny(flagName, "= \x00") {
			t.Skip("name rejected by the flag package")
		}

		var expected string
		switch prefix {
		case cli.NoPrefix:
			expected = fuzzMap(flagName)
		case "":
			expected = "APP_" + fuzzMap(flagName)
		default:
			expected = fuzzMap(prefix) + "_" + fuzzMap(flagName)
		}
		if os.Getenv(expected) != "" || os.Getenv(expected+"_FILE") != "" {
			t.Skip("collides with the real environment")
		}
		t.Setenv(expected, "bound-from-env")

		var got string
		root := &cli.Command{
			Name:      "app",
			EnvPrefix: prefix,
			Flags:     func(fs *cli.FlagSet) { fs.StringVar(&got, flagName, "", "") },
			Run:       func(context.Context, []string) error { return nil },
		}
		cli.SetIO(root, io.Discard, io.Discard)
		if code := cli.ExecuteArgs(context.Background(), root, nil); code != 0 {
			t.Fatalf("exit code = %d, expected 0 (prefix %q, flag %q)", code, prefix, flagName)
		}
		if got != "bound-from-env" {
			t.Fatalf("env %s did not bind flag %q (prefix %q): got %q", expected, flagName, prefix, got)
		}
	})
}

// FuzzDecodeJSON feeds raw bytes through the config file path: no input
// may panic, the exit code is always 0 or 2, Run executes exactly when
// the exit code is 0 — and, differentially against the stdlib, bytes that
// encoding/json rejects can never exit 0.
func FuzzDecodeJSON(f *testing.F) {
	f.Add([]byte(`{"s": "hello", "n": 8080}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"s": true}`))
	f.Add([]byte(`{"s": {"nested": 1}}`))
	f.Add([]byte(`{"s": null}`))
	f.Add([]byte(`{"n": "abc"}`))
	f.Add([]byte(`{"unknown": "k"}`))
	f.Add([]byte(`{`))
	f.Add([]byte(`{}{}`))
	f.Add([]byte(``))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		ran := false
		root := &cli.Command{
			Name:   "app",
			Config: cli.ConfigFile{Flag: "config"},
			Flags: func(fs *cli.FlagSet) {
				fs.String("s", "", "")
				fs.Int("n", 0, "")
			},
			Run: func(context.Context, []string) error {
				ran = true
				return nil
			},
		}
		cli.SetIO(root, io.Discard, io.Discard)
		code := cli.ExecuteArgs(context.Background(), root, []string{"-config", path})
		switch code {
		case 0:
			if !ran {
				t.Fatal("exit code 0 but Run never executed")
			}
			if !json.Valid(data) {
				t.Fatalf("exit code 0 on bytes encoding/json rejects: %q", data)
			}
		case 2:
			if ran {
				t.Fatal("exit code 2 but Run executed")
			}
		default:
			t.Fatalf("exit code = %d, expected 0 or 2", code)
		}
	})
}

// fuzzDotEnvMirror reimplements the documented dotenv subset as the
// oracle FuzzLoadDotEnv compares against: pairs in order, or a nil map
// when any line is malformed.
func fuzzDotEnvMirror(data []byte) map[string]string {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	pairs := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsFunc(key, unicode.IsSpace) {
			return nil
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if q := value[0]; (q == '"' || q == '\'') && value[len(value)-1] == q {
				value = value[1 : len(value)-1]
			}
		}
		if strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
			return nil // the OS rejects NUL; the loader surfaces its error
		}
		if _, seen := pairs[key]; !seen {
			pairs[key] = value
		}
	}
	return pairs
}

// FuzzLoadDotEnv feeds raw bytes through the dotenv loader against the
// mirror oracle: never panics, accepts exactly what the documented
// subset accepts, and every first-occurrence pair lands in the
// environment unless the environment already had the key. Keys the fuzz
// run creates are cleaned up afterwards.
func FuzzLoadDotEnv(f *testing.F) {
	f.Add([]byte("KEY=value\n"))
	f.Add([]byte("# comment\n\nA=1\r\nB='two'\nC=\"three\"\n"))
	f.Add([]byte("\xef\xbb\xbfBOM=1\n"))
	f.Add([]byte("export KEY=v\n"))
	f.Add([]byte("=nokey\n"))
	f.Add([]byte("A=a=b\nA=second\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}

		expected := fuzzDotEnvMirror(data)
		preset := map[string]bool{}
		for key := range expected {
			_, preset[key] = os.LookupEnv(key)
		}
		t.Cleanup(func() {
			for key := range expected {
				if !preset[key] {
					_ = os.Unsetenv(key)
				}
			}
		})

		err := cli.LoadDotEnv(path)
		if (err == nil) != (expected != nil) {
			t.Fatalf("LoadDotEnv err = %v, mirror accepts = %t (input %q)", err, expected != nil, data)
		}
		if err != nil {
			return
		}
		for key, want := range expected {
			if preset[key] {
				continue
			}
			if got := os.Getenv(key); got != want {
				t.Fatalf("env %q = %q, want %q (input %q)", key, got, want, data)
			}
		}
	})
}

// fuzzStaticText is every fixed string FuzzExecute's tree can legally
// print; a fuzzed secret value that happens to be a substring of it
// cannot be distinguished from legitimate output, so those inputs skip.
const fuzzStaticText = "app serve token help version usage " +
	"invalid value for flag -token\n" +
	"Run 'app --help' for usage.\n" +
	"Run 'app serve --help' for usage.\n" +
	"flag provided but not defined\n" +
	"flag needs an argument: -token\n" +
	"app: unknown command:\n" +
	"minimum log level (env APP_TOKEN) (default <secret>) [command] [flags] Commands: Flags: -token int api token print the"

// FuzzExecute drives the whole entry point with arbitrary argv: no input
// may panic, the exit code stays in {0, 2}, and a value handed to the
// secret-marked flag never appears in any output — the Secret contract,
// fuzz-checked end to end.
func FuzzExecute(f *testing.F) {
	f.Add("serve", "-token", "hunter2")
	f.Add("-token=hunter2", "serve", "x")
	f.Add("--", "-token", "secret")
	f.Add("-token", "9", "serve")
	f.Add("help", "serve", "")
	f.Add("-h", "", "")
	f.Add("version", "--", "")
	f.Fuzz(func(t *testing.T, a, b, c string) {
		args := []string{a, b, c}

		// A secret value and the arg index it came from. Non-flag output
		// (unknown-command and unknown-flag errors) legally echoes OTHER
		// args, so a value duplicated in another arg cannot be attributed
		// and skips below.
		type secretValue struct {
			v   string
			src int
		}
		var secretValues []secretValue
		for i, s := range args {
			if (s == "-token" || s == "--token") && i+1 < len(args) {
				secretValues = append(secretValues, secretValue{args[i+1], i + 1})
			}
			if v, ok := strings.CutPrefix(s, "-token="); ok {
				secretValues = append(secretValues, secretValue{v, i})
			}
			if v, ok := strings.CutPrefix(s, "--token="); ok {
				secretValues = append(secretValues, secretValue{v, i})
			}
		}

		var stdout, stderr bytes.Buffer
		root := &cli.Command{
			Name:    "app",
			Version: "1.0.0",
			Flags: func(fs *cli.FlagSet) {
				fs.Int("token", 0, "api token")
				fs.Secret("token")
			},
			Commands: []*cli.Command{{
				Name:  "serve",
				Short: "serve",
				Run:   func(context.Context, []string) error { return nil },
			}},
		}
		cli.SetIO(root, &stdout, &stderr)
		code := cli.ExecuteArgs(context.Background(), root, args)
		if code != 0 && code != 2 {
			t.Fatalf("exit code = %d, expected 0 or 2 (args %q)", code, args)
		}

		output := stdout.String() + stderr.String()
	values:
		for _, sv := range secretValues {
			if sv.v == "" || strings.Contains(fuzzStaticText, sv.v) {
				continue
			}
			for j, s := range args {
				if j != sv.src && strings.Contains(s, sv.v) {
					continue values
				}
			}
			if strings.Contains(output, sv.v) {
				t.Fatalf("secret value %q leaked into output (args %q):\n%s", sv.v, args, output)
			}
		}
	})
}
