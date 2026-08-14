# Changelog

All notable changes to `x/sshx` are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is semver under the `x/` contract — v0 forever until graduation, deletion is a legitimate outcome. Tags: `x/sshx/vX.Y.Z`.

## [Unreleased]

### Added

- `Dial` with context cancellation and staged typed errors (`network`/`hostkey`/`handshake`); host-key policy errors retrievable with `errors.As` from a failed dial
- Host-key policies: `KnownHosts` (strict pinning), `TOFU` (pin-on-consent via caller callback, single confirmation under concurrent first-contact dials), `InsecureAcceptAny` (explicit opt-out); OpenSSH `known_hosts` format, files created `0600`
- One-shot execution over the multiplexed transport: `Output` (separate streams + exit code) and `CombinedOutput` (interleaving preserved); output returned even on failure; ctx cancellation abandons the in-flight session
- Interactive sessions as pure streams: `Shell` with optional PTY, live `Resize`, ctx-bound lifetime — no terminal state touched by the library
- `Pool`/`Managed` persistent connections: self-healing with jittered exponential backoff, pool-wide dial-concurrency cap, periodic health probes, non-blocking `ErrNotReady`, `OnStateChange` observability hook with loop-ordered transitions carrying their driving error, and a straggler guard (client-identity check plus stale-token drain before Ready) so a failure from an already-replaced connection can't tear down its healthy successor
- Terminal `StateClosed` and a wired `ErrClosed`: operations on a closed client, session, or `Managed` fail typed instead of raw
- Documented defaults exported as constants (`DefaultDialTimeout`, `DefaultPingTimeout`, `DefaultTerm`/`DefaultCols`/`DefaultRows`, `DefaultMaxDials`; `keys.MinRSABits`/`keys.DefaultRSABits`) — every zero-value fallback a caller controls is referenceable
- Deadline-guarded `Ping` (OpenSSH keepalive request) and a background keepalive per connection that closes the client on a failed ping — a black-holed transport can't strand an in-flight command or `Wait` past its keepalive window
- `keys` subpackage: `ParsePrivate`/`ParsePrivateWithPassphrase`/`Load` (passphrase via callback, invoked only when needed) and `Generate` (Ed25519 default; RSA ≥ 2048 via `ErrRSATooWeak`, default 3072; comments with newlines or edge whitespace refused via `ErrInvalidComment` — a newline would forge authorized_keys entries, and edge whitespace does not survive a parse round-trip)
- Fuzzers on the package's own invariants: `FuzzParsePrivate` (arbitrary input never panics) and `FuzzGenerateComment` (accepted comments round-trip through one parseable authorized_keys line — this fuzzer caught the edge-whitespace loss before first release)
