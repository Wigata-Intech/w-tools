# logger

> Set it up once, and sensitive data stops appearing in your logs. That's the whole pitch.

**Status: 🚧 pre-release.** The design is settled, the code is landing. Until the `logger/v0.1.0` tag exists, anything here may still shift.

## The problem this solves

Every Go service does the same dance: wire up `log/slog`, pick a handler, parse a level, remember to add the service name to every logger… and then one day someone logs a `user` struct, and there's a password — or worse, a card number — sitting in production logs. Nobody did anything unusual. That's exactly the problem: keeping secrets out of logs usually depends on *every single log line being written carefully, forever*.

`logger` moves that responsibility into the pipeline. You say once, in config, "these keys are secret" — and then it doesn't matter who logs what, or how deeply the secret is buried inside a struct. The handler catches it before it's written.

```json
{"time":"2026-08-11T10:00:00+09:00","level":"INFO","msg":"payment created","env":"production","app":"wipays","card_number":"411111******1111","password":"[REDACTED]"}
```

The `card_number` above shows first 6 and last 4 — which happens to be exactly what PCI DSS permits for card numbers. That's not a coincidence; it's the example the masking config was designed around.

## What you get

- **One call to a working logger.** JSON output, your service's identity (`env`, `version`, `app`, `protocol`) on every line, level from config.
- **Redaction that can't be forgotten.** Keys you name are replaced (`[REDACTED]` — or your own wording) or partially masked, wherever they appear: top level, nested, or inside a struct someone passed whole.
- **Everything is still just slog.** No new logging API to learn — `Info`, `Warn`, `Error` with the same key-value args, plus `Debug` and `Panic`. Your existing slog knowledge, attrs, and tooling all still work.
- **Nothing else.** Zero dependencies. The `go.mod` requires Go 1.23.12 and is otherwise empty — that's a feature, and it's permanent.

## How it will look

```go
log := logger.New(logger.Config{
    Env:      "production",
    Version:  "1.4.2",
    App:      "wipays",
    Protocol: logger.ProtocolHTTP,                    // typed constants; any Protocol("...") works
    Level:    logger.ParseLevel(os.Getenv("LOG_LEVEL")), // or a slog.Level; zero value is Info
    Redact: logger.RedactConfig{
        Redacted: []string{"password", "authorization", "cvv"},
        Masked: map[string]logger.Mask{
            "card_number": {ShowFirst: 6, ShowLast: 4},
        },
    },
})

log.Info(ctx, "payment created", "order_id", "ord_123", "card_number", pan)
```

Every log method takes a `context.Context` first — today it flows to the underlying handler; automatic enrichment from ctx (trace ids and friends) is planned, see the [roadmap](../ROADMAP.md).

Already have a `*slog.Logger` you like? Keep it — add only the redaction layer:

```go
log := slog.New(logger.Wrap(existingHandler, redactCfg))
```

Runnable programs live in [`examples/`](examples/): [`payment`](examples/payment/main.go) shows PCI-style card masking on a whole struct, [`login`](examples/login/main.go) shows credentials caught in maps, `With`-bound args, and headers. `go run ./examples/payment` and read the output.

## What logging costs

Measured with `make bench` (Apple Silicon, 6-attr record, output discarded). Read it as a price list — each row is a situation you might be in:

| Situation | ns/op | allocs/op | Meaning for you |
| --------- | ----- | --------- | --------------- |
| Raw `log/slog`, no wrapper | ~690 | 0 | The baseline |
| `logger`, no redaction rules | ~700 | 0 | **The wrapper is free** — parity within run-to-run variance (±5%), zero allocations |
| Rules configured, record has no sensitive keys | ~900 | 1 | Your normal traffic with redaction armed: ~+25% |
| Redacting/masking top-level keys | ~980 | 4 | A protected log line costs about 1µs |
| Sensitive struct, nested two deep | ~2250 | 29 | Reflection is the expensive path — 3× baseline |

The practical takeaway from the last two rows: on your hottest code paths, pass sensitive values as top-level keys (`"card_number", pan`) rather than logging whole structs — identical protection, a third of the cost. Struct logging is fine everywhere else.

And the security claim is fuzz-tested, not asserted: 10M+ generated inputs — broken UTF-8, CJK, extreme masking bounds, JSON metacharacters — produced zero panics and zero records where a redacted value survived.

## The promises (as of v0.1.0)

- **When in doubt, it hides.** A value that can't be processed logs as `[UNLOGGABLE]` instead of leaking raw. Short values mask entirely rather than revealing "most" of themselves.
- **A rule means everywhere.** `password` matches at any depth, any casing, inside any struct — deliberately no "only redact it here" scoping, because a rule with blind spots is worse than none.
- **You don't pay for what you don't use.** No redaction rules configured → the handler is a pass-through, benchmarked against raw slog to prove it.

## License

[Apache-2.0](../LICENSE), like everything in [w-tools](../README.md).
