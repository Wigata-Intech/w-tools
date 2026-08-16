# Changelog

All notable changes to `cli`. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are per-module tags (`cli/vX.Y.Z`), semver, v0 until proven in production.

## [Unreleased]

### Added

- `migrationx`: dirty-state tracking for no-transaction migrations on mysql — the history table's `dirty` column marks a version before its statements run and clears it on success, `Up`/`Down` refuse to rerun while any version is dirty, `Status` reports it inline via `Migration.Dirty` instead of failing, and `New` heals a history table created before this column existed by adding it automatically

## [0.1.0] - 2026-08-14

### Added

- `migrationx` subpackage: SQL migration engine — timestamped up/down files, sha256-verified history table, per-migration transactions with a no-transaction annotation, statement scanner (quotes, comments, regions, mysql backslash and # comments), out-of-order refusal with explicit override, database-side migration locks, concurrent-runner skip protection, cancellation-safe cleanup, audited rollbacks, `Create` scaffolding

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
- Benchmark suite pricing the boot cost; measured binary-size comparison vs cobra+viper in the README — benchmark and fuzz sections carry device specs, exact commands, and raw output logs
