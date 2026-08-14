package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/cli"
)

// cfgWriteFile writes content under dir and returns the file path.
func cfgWriteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// cfgRootInput shapes the root cfgRun builds: Config wiring, an optional
// user-declared "config" flag default, and the command line.
type cfgRootInput struct {
	configFlag    string
	configDefault string
	decoder       cli.Decoder
	args          []string
}

// cfgRun executes a root with an int -port (default 1000) and a string
// -host (default "h") and returns the exit code, the "port host" value Run
// observed, and stderr.
func cfgRun(t *testing.T, in cfgRootInput) (int, string, string) {
	t.Helper()
	var port int
	var host, got string
	root := &cli.Command{
		Name:   "app",
		Config: cli.ConfigFile{Flag: in.configFlag, Decoder: in.decoder},
		Flags: func(fs *cli.FlagSet) {
			fs.IntVar(&port, "port", 1000, "")
			fs.StringVar(&host, "host", "h", "")
			if in.configDefault != "" {
				fs.String("config", in.configDefault, "")
			}
		},
		Run: func(context.Context, []string) error {
			got = fmt.Sprintf("%d %s", port, host)
			return nil
		},
	}
	var out, errw bytes.Buffer
	cli.SetIO(root, &out, &errw)
	code := cli.ExecuteArgs(context.Background(), root, in.args)
	return code, got, errw.String()
}

var (
	errCfgBadLine = errors.New("bad line")
	errCfgBoom    = errors.New("boom")
)

// cfgKVDecoder is a minimal key=value line Decoder for the custom-decoder
// cases.
func cfgKVDecoder(data []byte) (map[string]string, error) {
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%w %q", errCfgBadLine, line)
		}
		values[k] = v
	}
	return values, nil
}

// cfgExpected is the outcome of one TestLoadConfig case.
type cfgExpected struct {
	code   int
	value  string
	stderr string
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	good := cfgWriteFile(t, dir, "good.json", `{"port": 4000, "host": "cfg"}`)
	unknown := cfgWriteFile(t, dir, "unknown.json", `{"nope": 1}`)
	selfRef := cfgWriteFile(t, dir, "self.json", `{"config": "elsewhere.json"}`)
	kv := cfgWriteFile(t, dir, "kv.conf", "port=7\nhost=z")
	bad := cfgWriteFile(t, dir, "bad.json", `{"port":`)
	missing := filepath.Join(dir, "missing.json")

	tests := []struct {
		name     string
		mockFunc func(t *testing.T)
		input    cfgRootInput
		expected cfgExpected
	}{
		{
			name:     "file applies to unset flags",
			input:    cfgRootInput{configFlag: "config", args: []string{"-config", good}},
			expected: cfgExpected{code: 0, value: "4000 cfg"},
		},
		{
			name:     "explicit flag beats config file",
			input:    cfgRootInput{configFlag: "config", args: []string{"-config", good, "-port", "9"}},
			expected: cfgExpected{code: 0, value: "9 cfg"},
		},
		{
			name: "environment-set flag beats config file",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_HOST", "envhost")
			},
			input:    cfgRootInput{configFlag: "config", args: []string{"-config", good}},
			expected: cfgExpected{code: 0, value: "4000 envhost"},
		},
		{
			name: "path resolved from the environment",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_CONFIG", good)
			},
			input:    cfgRootInput{configFlag: "config"},
			expected: cfgExpected{code: 0, value: "4000 cfg"},
		},
		{
			name:     "no config flag disables file logic",
			input:    cfgRootInput{configDefault: bad},
			expected: cfgExpected{code: 0, value: "1000 h"},
		},
		{
			name:     "empty path is skipped",
			input:    cfgRootInput{configFlag: "config"},
			expected: cfgExpected{code: 0, value: "1000 h"},
		},
		{
			name:     "missing default path is skipped",
			input:    cfgRootInput{configFlag: "config", configDefault: missing},
			expected: cfgExpected{code: 0, value: "1000 h"},
		},
		{
			name:     "declared config flag default is respected",
			input:    cfgRootInput{configFlag: "config", configDefault: good},
			expected: cfgExpected{code: 0, value: "4000 cfg"},
		},
		{
			name:     "custom decoder plugs in",
			input:    cfgRootInput{configFlag: "config", decoder: cfgKVDecoder, args: []string{"-config", kv}},
			expected: cfgExpected{code: 0, value: "7 z"},
		},
		{
			name:  "explicit path to a missing file",
			input: cfgRootInput{configFlag: "config", args: []string{"-config", missing}},
			expected: cfgExpected{
				code:   2,
				stderr: "cli: config file: open " + missing + ": no such file or directory\n",
			},
		},
		{
			name: "environment path to a missing file",
			mockFunc: func(t *testing.T) {
				t.Helper()
				t.Setenv("APP_CONFIG", missing)
			},
			input: cfgRootInput{configFlag: "config"},
			expected: cfgExpected{
				code:   2,
				stderr: "cli: config file: open " + missing + ": no such file or directory\n",
			},
		},
		{
			name:  "unreadable default path",
			input: cfgRootInput{configFlag: "config", configDefault: dir},
			expected: cfgExpected{
				code:   2,
				stderr: "cli: config file: read " + dir + ": is a directory\n",
			},
		},
		{
			name:     "unknown keys are ignored",
			input:    cfgRootInput{configFlag: "config", args: []string{"-config", unknown}},
			expected: cfgExpected{code: 0, value: "1000 h"},
		},
		{
			name:  "config-path key inside the file is rejected",
			input: cfgRootInput{configFlag: "config", args: []string{"-config", selfRef}},
			expected: cfgExpected{
				code:   2,
				stderr: "cli: config file " + selfRef + ": key \"config\": the config-path flag cannot be set from the config file\n",
			},
		},
		{
			name: "decoder error wrapped with the file name",
			input: cfgRootInput{
				configFlag: "config",
				decoder:    func([]byte) (map[string]string, error) { return nil, errCfgBoom },
				args:       []string{"-config", kv},
			},
			expected: cfgExpected{
				code:   2,
				stderr: "cli: config file " + kv + ": boom\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockFunc != nil {
				tt.mockFunc(t)
			}
			code, value, stderr := cfgRun(t, tt.input)
			if code != tt.expected.code {
				t.Fatalf("exit code = %d, expected %d; stderr %q", code, tt.expected.code, stderr)
			}
			if value != tt.expected.value {
				t.Errorf("value = %q, expected %q", value, tt.expected.value)
			}
			if stderr != tt.expected.stderr {
				t.Errorf("stderr = %q, expected %q", stderr, tt.expected.stderr)
			}
		})
	}
}

// cfgDecodeExpected is the outcome of one TestDecodeJSON case; errTail is
// the message after "cli: config file <path>: ", empty on success.
type cfgDecodeExpected struct {
	code    int
	value   string
	errTail string
}

func TestDecodeJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected cfgDecodeExpected
	}{
		{
			name:     "string value",
			input:    `{"s": "hello"}`,
			expected: cfgDecodeExpected{code: 0, value: `"hello" "" ""`},
		},
		{
			name:     "number value",
			input:    `{"n": 8080}`,
			expected: cfgDecodeExpected{code: 0, value: `"" "8080" ""`},
		},
		{
			name:     "bool value",
			input:    `{"b": true}`,
			expected: cfgDecodeExpected{code: 0, value: `"" "" "true"`},
		},
		{
			name:     "object value",
			input:    `{"s": {"a": 1}}`,
			expected: cfgDecodeExpected{code: 2, errTail: `key "s": value must be a string, number, or bool`},
		},
		{
			name:     "array value",
			input:    `{"s": [1]}`,
			expected: cfgDecodeExpected{code: 2, errTail: `key "s": value must be a string, number, or bool`},
		},
		{
			name:     "null value",
			input:    `{"s": null}`,
			expected: cfgDecodeExpected{code: 2, errTail: `key "s": value must be a string, number, or bool`},
		},
		{
			name:     "invalid JSON",
			input:    `{`,
			expected: cfgDecodeExpected{code: 2, errTail: "unexpected EOF"},
		},
		{
			name:     "top-level null",
			input:    `null`,
			expected: cfgDecodeExpected{code: 2, errTail: "config must be a JSON object"},
		},
		{
			name:     "trailing data after the object",
			input:    `{}{}`,
			expected: cfgDecodeExpected{code: 2, errTail: "trailing data after config object"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := cfgWriteFile(t, t.TempDir(), "config.json", tt.input)
			var s, n, b, got string
			root := &cli.Command{
				Name:   "app",
				Config: cli.ConfigFile{Flag: "config"},
				Flags: func(fs *cli.FlagSet) {
					fs.StringVar(&s, "s", "", "")
					fs.StringVar(&n, "n", "", "")
					fs.StringVar(&b, "b", "", "")
				},
				Run: func(context.Context, []string) error {
					got = fmt.Sprintf("%q %q %q", s, n, b)
					return nil
				},
			}
			var out, errw bytes.Buffer
			cli.SetIO(root, &out, &errw)
			code := cli.ExecuteArgs(context.Background(), root, []string{"-config", path})
			if code != tt.expected.code {
				t.Fatalf("exit code = %d, expected %d; stderr %q", code, tt.expected.code, errw.String())
			}
			if got != tt.expected.value {
				t.Errorf("value = %q, expected %q", got, tt.expected.value)
			}
			wantStderr := ""
			if tt.expected.errTail != "" {
				wantStderr = "cli: config file " + path + ": " + tt.expected.errTail + "\n"
			}
			if stderr := errw.String(); stderr != wantStderr {
				t.Errorf("stderr = %q, expected %q", stderr, wantStderr)
			}
		})
	}
}
