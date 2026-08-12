# Changelog

All notable changes to `x/circuitbreaker` are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is semver under the `x/` contract — v0 forever until graduation, deletion is a legitimate outcome. Tags: `x/circuitbreaker/vX.Y.Z`.

## [Unreleased]



## [0.1.0] - 2026-08-12

### Added

- Three-state breaker (`Closed`/`Open`/`HalfOpen`) tripping on failure ratio over a bucketed sliding window with a minimum sample size; recovery through capped half-open probes, full success closing with a fresh window
- `Allow`/`Record` for guarding any operation, `RoundTripper` for native `*http.Client` use, structural compatibility with `httpx/client.Breaker`
- `State()` and `OnStateChange` (fired outside the lock) for bring-your-own observability
- Driven-clock test seam; 100% coverage; `BenchmarkAllowRecord` pricing the hot path
