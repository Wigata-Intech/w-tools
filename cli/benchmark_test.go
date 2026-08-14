// Benchmarks price the one-time cost of Execute: tree dispatch, flag
// parsing, and the precedence resolution. Everything here happens once
// per process start — there is no request path — so the numbers answer
// "what does boot cost", not "what does traffic cost".
package cli_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wigata-Intech/w-tools/cli"
)

func benchTree() *cli.Command {
	var logLevel, addr, key string
	root := &cli.Command{
		Name: "bench",
		Flags: func(fs *cli.FlagSet) {
			fs.StringVar(&logLevel, "log-level", "info", "minimum log level")
			fs.StringVar(&key, "api-key", "", "upstream API key")
			fs.Secret("api-key")
		},
		Commands: []*cli.Command{{
			Name: "serve",
			Flags: func(fs *cli.FlagSet) {
				fs.StringVar(&addr, "http-addr", ":8080", "listen address")
			},
			Run: func(_ context.Context, _ []string) error { return nil },
		}},
	}
	cli.SetIO(root, io.Discard, io.Discard)
	return root
}

// BenchmarkExecute is the plain boot: dispatch one subcommand with one
// explicit flag, no environment, no config file.
func BenchmarkExecute(b *testing.B) {
	ctx := context.Background()
	args := []string{"serve", "-http-addr", ":9090"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if code := cli.ExecuteArgs(ctx, benchTree(), args); code != 0 {
			b.Fatalf("exit code = %d", code)
		}
	}
}

// BenchmarkExecuteEnvAndConfig is the loaded boot: every layer active —
// an env-bound flag, and a config file read and decoded per execution.
func BenchmarkExecuteEnvAndConfig(b *testing.B) {
	b.Setenv("BENCH_LOG_LEVEL", "debug")
	cfg := filepath.Join(b.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"http-addr": ":7070"}`), 0o600); err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	args := []string{"-config", cfg, "serve"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		root := benchTree()
		root.Config = cli.ConfigFile{Flag: "config"}
		if code := cli.ExecuteArgs(ctx, root, args); code != 0 {
			b.Fatalf("exit code = %d", code)
		}
	}
}
