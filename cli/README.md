# cli

> A command tree, flags that read the environment, and generated help — the cobra+viper subset a container-deployed service actually uses, on the standard library alone.

**Status:** 🚧 unreleased, pre-`v0.1.0`. The API may still move; `cli/migrationx` (SQL migrations) lands in this module before the first tag. Nothing here is stable until `cli/v0.1.0` exists.

## TL;DR

- `Command` tree dispatched from `os.Args`, context-first, plain-func handlers — exit codes 0/1/2
- Every flag settable four ways with one fixed precedence: **flag > env > `*_FILE` > config file > default**
- Env names derived mechanically (`--http-addr` → `MY_SERVICE_HTTP_ADDR`) — the mapping is never documented by hand, and `--help` prints it
- `Secret` flags: masked defaults in help, error messages that never echo the value
- Struct binding: `fs.Bind(&cfg)` declares flags from your config struct's tags — `required` fields fail at startup, not mid-run — and `LoadDotEnv` feeds a `.env` file into the environment layer
- JSON config built in; other formats plug in through a `Decoder` seam — this module never grows parsers
- Zero dependencies, zero `require` lines, CGO-free

## What problem this solves

A real service entrypoint is never just `main()`: it is `serve`, `migrate`, `version` — subcommands with flags, most of which must also come from environment variables, because that is how containers configure things. The stdlib `flag` package covers a third of that and stops: no subcommands, no env binding, no unified help.

The ecosystem's answer, cobra + viper, works — and carries remote config backends, live reload, YAML parsing, and shell completion into every binary that just wanted three subcommands and env-settable flags. `cli` is the missing floor without the freight: the tree, the precedence chain, the generated help, and nothing else.

## How it solves it

```go
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(root().Execute(ctx))
}

func root() *cli.Command {
	var addr string
	return &cli.Command{
		Name:    "my-service",
		Short:   "example service",
		Version: version, // stamped via -ldflags "-X main.version=..."
		Config:  cli.ConfigFile{Flag: "config"},
		Commands: []*cli.Command{{
			Name:  "serve",
			Short: "start the HTTP server",
			Flags: func(fs *cli.FlagSet) {
				fs.StringVar(&addr, "http-addr", ":8080", "listen address")
			},
			Run: func(ctx context.Context, args []string) error {
				return serve(ctx, addr)
			},
		}},
	}
}
```

`--http-addr :9090` on the command line, `MY_SERVICE_HTTP_ADDR` in the environment, `MY_SERVICE_HTTP_ADDR_FILE` naming a mounted secret file, or `{"http-addr": ":9090"}` in the config file — first one present wins, in exactly that order. Flags declared on a command are visible to all its descendants; `FlagSet` mirrors the `flag.FlagSet` constructors exactly, so nothing new to learn there.

Or skip individual declarations and make your config struct the schema:

```go
type Config struct {
	HTTPAddr   string        `cli:"http-addr" default:":8080" usage:"listen address"`
	DBPassword string        `cli:"db-password,secret,required" usage:"database password"`
	Timeout    time.Duration `default:"5s" usage:"request timeout"` // name derives: timeout
}

var cfg Config
// inside Flags: fs.Bind(&cfg)
```

Every layer resolves into `cfg` before `Run` executes. A `required` field no layer supplied stops the process at startup — exit 2, naming the flag and its env var — never mid-request. `cli.LoadDotEnv(".env")` before `Execute` feeds a dotenv file into the environment layer (real environment wins), for bare-machine runs without an orchestrator.

Secrets travel by env or `*_FILE`, never by flag (argv is world-readable via `ps`). Mark one with `fs.Secret("db-password")` and its default renders as `<secret>` in help while parse errors carry the flag name only — a credential never reaches stderr or CI logs.

## Why it matters

- **Conventions you already know:** POSIX/GNU flag syntax, `--` terminator, help to stdout, errors to stderr, exit codes 0/1/2 — script-friendly by construction
- **Library, never a binary:** each deployable assembles its own tree in its own `package main`, so the linker's dead-code elimination keeps every binary to exactly what it imports
- **No second source of truth:** the env mapping is derived, printed in `--help`, and covered by the same precedence tests that gate every release
- **No lock-in:** handlers are `func(ctx, args) error`; leaving cli is deleting the tree, not rewriting the service

## What it costs

Everything happens once at process start — there is no hot path. Measured on Apple M2 Pro, Go stable:

| Measure | Result |
| ------- | ------ |
| Full dispatch (subcommand + flag), `BenchmarkExecute` | ~2.4 µs, 61 allocs — once per process |
| Every layer active (env + config file read/decoded), `BenchmarkExecuteEnvAndConfig` | ~17 µs, 87 allocs — once per process |
| Binary size: hello-world `main` | 1.6 MB (`-trimpath -ldflags "-s -w"`) |
| the same `main` on **cli** | 2.1 MB — **+0.5 MB** |
| the same `main` on cobra + viper | 4.7 MB — **+3.1 MB**, 6× the cli delta |

Structural notes, honest ones:

- Parsing re-registers visible flags per tree level during dispatch — that is the ~2 µs above, zero afterward
- JSON is the only built-in config format; YAML/TOML mean waiting for (or writing) a `Decoder`
- No shell completion, no live config reload, no remote config — deliberate omissions, documented in the design, revisited only on evidence

## The promises

As of unreleased main, holding from the first tag onward:

- The precedence order — flag > env > `*_FILE` > config file > default — is contract, not implementation detail
- A `Secret`-marked flag's value never appears in help or error output
- Conflicting configuration fails closed: an env var and its `_FILE` variant both set, or a config file that sets its own path flag, abort with exit 2 — while config keys other subcommands own are simply ignored, so one file serves the whole binary
- Zero third-party dependencies, permanently; the `go.mod` stays empty and dependabot exists to scream if it doesn't
