# Changelog

All notable changes to `cli`. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are per-module tags (`cli/vX.Y.Z`), semver, v0 until proven in production.

## [Unreleased]

### Added

- Command tree: `Command` with subcommands, context-first `Run`, dispatch from `os.Args`, exit codes 0/1/2
- Generated help (`-h`/`--help`, `help [command]`) and root-only `version`/`--version`
- `FlagSet` mirroring the stdlib `flag` constructors, with flag inheritance down the tree
- Configuration precedence: flag > environment variable > `*_FILE` indirection > config file > default
- Mechanical env naming with a per-binary prefix derived from the root command's name (`EnvPrefix` override, `NoPrefix` sentinel)
- JSON config file support behind the `Decoder` seam for future format packages
- `Secret` flag marking: masked defaults in help, value-free error messages
- Struct binding: `FlagSet.Bind` declares flags from a config struct's `cli`/`default`/`usage` tags; `FlagSet.Required` and the `required` tag option fail startup (exit 2) when no layer supplies a value; help marks required flags
- `LoadDotEnv`: minimal dotenv loader into the process environment, real environment winning
- Config file keys are validated loosely: keys no visible flag declares are ignored (one file serves every subcommand); setting the config-path flag from the file remains an error
- Examples module: `demo` (every feature on one screen) and `service` (register-based layout — rest, cron, consumer, migrate constructed in their own packages), with `.env.example` and `config.json` samples
- Benchmark suite pricing the boot cost; measured binary-size comparison vs cobra+viper in the README
