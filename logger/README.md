# logger

> Set it up once, and sensitive data stops appearing in your logs. That's the whole pitch.

[![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/w-tools/logger.svg)](https://pkg.go.dev/github.com/Wigata-Intech/w-tools/logger)

**Status: v0, released.** Production-track at Wigata InTech. Semver v0 applies: the API can still move between minor versions until `v1.0.0`, which lands only after surviving production use.

## TL;DR

```bash
go get github.com/Wigata-Intech/w-tools/logger
```

- One call to a working logger: JSON output, your service's identity (`env`, `version`, `app`, `protocol`) on every line, level from config
- Redaction that can't be forgotten: keys you name are replaced or partially masked wherever they appear — top level, nested, or inside a struct someone passed whole
- Everything is still just slog: `Debug`/`Info`/`Warn`/`Error`/`Panic` with slog's own key-value args; your existing slog knowledge, attrs, and tooling all still work
- Zero dependencies, permanently — the `go.mod` requires Go 1.23.12 and nothing else

## What problem this solves

Every Go service does the same dance: wire up `log/slog`, pick a handler, parse a level, remember to add the service name to every logger… and then one day someone logs a `user` struct, and there's a password — or worse, a card number — sitting in production logs. Nobody did anything unusual. That's exactly the problem: keeping secrets out of logs usually depends on *every single log line being written carefully, forever*.

`logger` moves that responsibility into the pipeline. You say once, in config, "these keys are secret" — and then it doesn't matter who logs what, or how deeply the secret is buried inside a struct. The handler catches it before it's written.

```json
{"time":"2026-08-11T10:00:00+09:00","level":"INFO","msg":"payment created","env":"production","app":"wipays","card_number":"411111******1111","password":"[REDACTED]"}
```

## How it solves it

One `Config` covers the whole surface:

```go
log := logger.New(logger.Config{
    Env:      "production",
    Version:  "1.4.2",
    App:      "wipays",
    Protocol: logger.ProtocolHTTP,
    Level:    logger.ParseLevel(os.Getenv("LOG_LEVEL")),
    Redact: logger.RedactConfig{
        Redacted: []string{"password", "authorization", "cvv"},
        Masked: map[string]logger.Mask{
            "card_number": {ShowFirst: 6, ShowLast: 4},
        },
    },
    ContextAttrs: func(ctx context.Context) []slog.Attr {
        var a []slog.Attr
        if id := middleware.RequestIDFrom(ctx); id != "" {
            a = append(a, slog.String("request_id", id))
        }
        return a
    },
})

log.Info(ctx, "payment created", "order_id", "ord_123", "card_number", pan)
```

Reading it top to bottom:

- **Base fields** (`Env`/`Version`/`App`/`Protocol`) stamp every line; empty ones are omitted. `Protocol` takes the typed constants or any `Protocol("...")`.
- **`Level`** is the minimum logged; `ParseLevel` reads names from env vars, the zero value is Info.
- **`Redact`** declares sensitive keys once and they are caught at any depth — top level, nested structs, maps, `With`-bound args. `Masked` keeps only the configured edges.
- **`ContextAttrs`** is enrichment: every log method takes `ctx` first, and this func runs once per emitted record to turn context into attrs — so a repository log line deep below the handler still carries the request's identity, with no IDs threaded by hand. The logger never reads context keys itself; you wire your middleware's accessors (`RequestIDFrom`, `TraceIDFrom`, …). Extracted attrs pass through redaction like any other, a call-site attr with the same key wins (a line never repeats a key), and leaving it nil disables enrichment on the zero-allocation path.

Already have a `*slog.Logger` you like? Keep it — `Wrap` adds the same layers over any handler; zero-value fields add nothing:

```go
log := slog.New(logger.Wrap(existingHandler, logger.WrapConfig{
    Redact:       redactCfg, // or just one of the two
    ContextAttrs: fromCtx,
}))
```

Runnable programs live in [`examples/`](examples/): [`payment`](examples/payment/main.go) shows PCI-style card masking on a whole struct, [`login`](examples/login/main.go) shows credentials caught in maps, `With`-bound args, and headers, [`enrichment`](examples/enrichment/main.go) shows request identity flowing from ctx onto every line. `go run ./examples/payment` and read the output.

## Why it matters

Compliance standards treat log output as data storage: PCI DSS caps card-number display at first 6 + last 4 — `Mask{ShowFirst: 6, ShowLast: 4}` is that rule verbatim, and it's the example above — while GDPR's data-minimization and OWASP's logging guidance both point to the same conclusion: the only reliable place to enforce "never log secrets" is inside the pipeline, not at every call site. That's the difference between a convention your team remembers and a guarantee your config enforces.

And the guarantee is fuzz-tested, not asserted: 10M+ generated inputs across development — broken UTF-8, CJK, extreme masking bounds, JSON metacharacters — produced zero panics and zero records where a redacted value survived.

<details>
<summary>Fuzzing — commands and raw output (10s smoke; release runs are longer)</summary>

```text
$ go test -run='^$' -fuzz=FuzzMaskString -fuzztime=10s .
$ go test -run='^$' -fuzz=FuzzRedact -fuzztime=10s .
```

</details>

## What it costs

Read it as a price list — each row is a situation you might be in (6-attr record, output discarded). Measured on a MacBook Pro — Apple M2 Pro (10 cores), 16 GB RAM, macOS 26.5.2, go1.26.6.

```bash
cd logger && go test -run='^$' -bench=. -benchmem ./...
```

<details>
<summary>Raw output</summary>

```text
goos: darwin
goarch: arm64
pkg: github.com/Wigata-Intech/w-tools/logger
cpu: Apple M2 Pro
BenchmarkRawSlog-10                 	 1780122	       708.2 ns/op	       0 B/op	       0 allocs/op
BenchmarkPassThrough-10             	 1743573	       704.1 ns/op	       0 B/op	       0 allocs/op
BenchmarkContextAttrs-10            	 1000000	      1065 ns/op	     288 B/op	       4 allocs/op
BenchmarkRulesNoMatch-10            	 1329769	       951.7 ns/op	     208 B/op	       1 allocs/op
BenchmarkRedactTopLevel-10          	 1000000	      1005 ns/op	     192 B/op	       4 allocs/op
BenchmarkRedactStruct-10            	  560204	      2120 ns/op	    1474 B/op	      28 allocs/op
BenchmarkPassThroughParallel-10     	 3889734	       350.5 ns/op	       0 B/op	       0 allocs/op
BenchmarkRedactStructParallel-10    	  533304	      2319 ns/op	    1556 B/op	      29 allocs/op
PASS
ok  	github.com/Wigata-Intech/w-tools/logger	12.532s
```

</details>

| Situation | ns/op | allocs/op | Meaning for you |
| --------- | ----- | --------- | --------------- |
| Raw `log/slog`, no wrapper | ~710 | 0 | The baseline |
| `logger`, no redaction rules | ~700 | 0 | **The wrapper is free** — parity within run-to-run variance (±5%), zero allocations |
| `ContextAttrs` set, two attrs appended | ~1070 | 4 | Enrichment: extractor call, duplicate-key scan, record clone |
| Rules configured, record has no sensitive keys | ~950 | 1 | Your normal traffic with redaction armed: ~+30% |
| Redacting/masking top-level keys | ~1000 | 4 | A protected log line costs about 1µs |
| Sensitive struct, nested two deep | ~2100 | 28 | Reflection is the expensive path — ~3× baseline |

The practical takeaway from the last two rows: on your hottest code paths, pass sensitive values as top-level keys (`"card_number", pan`) rather than logging whole structs — identical protection, a third of the cost. Struct logging is fine everywhere else.

Under concurrency it scales rather than queues: eight goroutines sharing one logger push the pass-through per-line cost *down* (~350 ns/op at `-cpu 8`, still zero allocations) and hold the struct path essentially flat (~2.3µs) — the handler and the reflection plan cache are read-shared, so more cores mean more throughput, not a lock convoy.

## The promises

As of `v0.1.0`:

- **When in doubt, it hides.** A value that can't be processed logs as `[UNLOGGABLE]` instead of leaking raw. Short values mask entirely rather than revealing "most" of themselves.
- **A rule means everywhere.** `password` matches at any depth, any casing, inside any struct — deliberately no "only redact it here" scoping, because a rule with blind spots is worse than none.
- **You don't pay for what you don't use.** No redaction rules configured → the handler is a pass-through, benchmarked against raw slog to prove it.
