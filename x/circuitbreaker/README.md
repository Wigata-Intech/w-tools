# x/circuitbreaker

> When an upstream gets sick, stop calling it — fail in nanoseconds instead of burning timeouts.

[![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/w-tools/x/circuitbreaker.svg)](https://pkg.go.dev/github.com/Wigata-Intech/w-tools/x/circuitbreaker)

**Status: experimental, under `x/`.** The `x/` contract applies in full: the API may break between minors, the experiment may fail, and the package may be **deleted outright**. Nothing at the w-tools root depends on it — never build anything load-bearing on an `x/` package. Graduation to the root, under a new import path, is the only way it earns stability.

## TL;DR

```bash
go get github.com/Wigata-Intech/w-tools/x/circuitbreaker
```

- Classic three-state breaker: closed → open (fail fast) → half-open (probe) → closed
- Trips on failure ratio over a sliding window, with a minimum sample size so cold starts can't trip on one bad call
- Guards anything: `Allow`/`Record` around any operation, `RoundTripper` for a native `*http.Client`, or plug straight into `httpx/client`'s `Breaker` hook — satisfied structurally, no import edge
- Bring your own observability: `State()` to poll, `OnStateChange` to push — fired outside the lock, safe to log from
- Zero dependencies, per-process, fixed memory, no background goroutines

## What problem this solves

A failing upstream is expensive twice: every call burns its full timeout before erroring, and the sick service gets hammered while trying to recover. Goroutines pile up, latency propagates to your own callers, and one bad dependency degrades the whole service. A circuit breaker converts that slow bleeding into a fast, bounded failure — and then probes carefully until the upstream proves healthy.

## How it solves it

```go
br := circuitbreaker.New(circuitbreaker.Config{}) // sane defaults throughout

// Native net/http — no httpx anywhere:
hc := &http.Client{Transport: br.RoundTripper(nil)}

// httpx/client — the Breaker hook, satisfied structurally:
c := client.New(client.Config{Breaker: br})

// Anything else — a DB call, a gRPC stream:
if err := br.Allow(); err != nil {
    return err // circuit open: fail fast
}
err := doTheCall()
br.Record(err)
```

One breaker guards one upstream — a service talking to three APIs creates three breakers.

## Why it matters

Failure classification stays with the caller: `Record(err)` counts nil as success and anything else as failure, so *you* decide whether a 5xx trips the circuit (the `RoundTripper` records any received response as success — status policy is yours). Transitions are observable the moment they happen (`OnStateChange` — the instant the circuit opens for a payments API is the instant someone should know), and the whole state machine is deterministic under test: the clock is driven, never slept on.

## What it costs

Measured with `go test -bench=. -benchmem -cpu=1,2,4,8` (Apple Silicon; `Allow`+`Record` closed path, one shared breaker). Read the parallel rows as a traffic curve — every caller hammering the same mutex:

| Situation | ns/op | allocs/op | Meaning for you |
| --------- | ----- | --------- | --------------- |
| Single caller | ~55 | 0 | The floor: a mutex over integer math |
| 2 concurrent callers | ~123 | 0 | Contention appears — still sub-microsecond |
| 4 concurrent callers | ~219 | 0 | ~4.5M guarded calls/sec through one breaker |
| 8 concurrent callers | ~247 | 0 | Contention flattens: ~4M calls/sec, zero allocations throughout |

The takeaway: even at the worst measured contention, the breaker adds a quarter of a microsecond to calls that cost milliseconds on the network — three to four orders of magnitude below the thing it guards. Zero allocations at every level means no GC pressure, ever. Memory is fixed at construction: one ring of `WindowBuckets` counters, no goroutines, no timers.

## The promises

- **Per-process, honestly.** Replicas trip independently; cluster-wide breaking needs shared state and is a different project
- **Cold starts don't trip.** The ratio is judged only at `MinRequests` samples and above
- **Recovery is earned.** Half-open admits `HalfOpenProbes` requests; one failure re-opens with a restarted timer, full success closes with a fresh window — sick-period history never re-trips a healthy circuit

Two honest limits, stated because they bite: the token-less `Allow`/`Record` pairing cannot tell a result from before the last transition apart from a current one, so **keep operation timeouts below `OpenFor`** and the ambiguity never arises (stale results with no probe in flight are ignored regardless). And through `RoundTripper`, caller-side cancellations count as failures — a burst of them can trip the circuit against a healthy upstream.
