# x/sshx

> Persistent SSH connections that manage themselves — dial once, run many, heal on failure.

**Status: experimental, under `x/`, at `v0.1.0`.** The `x/` contract applies in full: the API may break between minors, the experiment may fail, and the package may be **deleted outright**. Nothing at the w-tools root depends on it — never build anything load-bearing on an `x/` package. Graduation to the root, under a new import path, is the only way it earns stability.

## TL;DR

```bash
go get github.com/Wigata-Intech/w-tools/x/sshx@latest
```

- One self-healing connection per host: automatic reconnect with jittered exponential backoff, health probes, and a pool-wide cap on concurrent dials so a fleet cold-start can't trip `sshd`'s throttle
- Never blocks on a dead host: a not-ready connection fails in nanoseconds, so polling fifty hosts survives ten being down
- `context.Context` end to end — dial, command, session, ping all cancel cleanly
- Fail-closed host keys: strict pinning or pin-on-first-use with *your* confirmation callback; accepting an unknown host silently requires calling something named `InsecureAcceptAny`
- One-shot commands with `os/exec`-familiar shapes: output is returned even when the command fails, exit codes included
- Interactive shells as pure `io` streams with PTY and live resize — wire them to a terminal, a websocket, or a recorder; the library never touches yours
- Typed errors throughout (`errors.As`-able), and a `keys` subpackage that parses, loads (passphrase via callback), and generates keys
- One dependency, deliberately: `golang.org/x/crypto` — the Go team's SSH implementation. Nothing else, ever

## What problem this solves

Anything that manages machines over SSH — a fleet dashboard, a deploy job, an ops bot — hits the same wall: connections die, and naive code either redials per command (paying the key-exchange handshake every time), blocks a whole refresh loop on one dead host, or reconnects twenty hosts in lockstep after a network blip and trips the server's connection throttle. Add the interactive parts — host-key trust prompts, passphrases, terminal resizing — and the SSH plumbing ends up welded to one UI, unusable anywhere else.

sshx is that plumbing, extracted and made self-managing: connections that keep themselves alive and multiplex everything over one handshake, with every interactive decision handed back to you as a callback or a stream.

## How it solves it

```go
hostKey, err := sshx.TOFU(knownHostsPath, confirmOnMyUI) // or sshx.KnownHosts(path) for automation
if err != nil { /* ... */ }
signer, err := keys.Load(keyPath, promptOnMyUI) // prompt runs only if the key is encrypted
if err != nil { /* ... */ }

cfg := sshx.Config{User: "deploy", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKey: hostKey}

pool := sshx.NewPool(16) // at most 16 handshakes in flight across the fleet
defer pool.Close()

web := pool.Add(sshx.ManagedConfig{
    Dial: func(ctx context.Context) (*sshx.Client, error) {
        return sshx.Dial(ctx, "10.0.0.7:22", cfg)
    },
})

// Poll it forever; a dead host answers ErrNotReady instantly and heals itself.
out, err := web.CombinedOutput(ctx, "uptime")
```

One-shot against a single host, no pool:

```go
c, err := sshx.Dial(ctx, "10.0.0.7:22", cfg)
if err != nil { /* stages: network / hostkey / handshake — errors.As for the details */ }
defer c.Close()
res, err := c.Output(ctx, "systemctl is-active my-service") // res.Stdout, res.Stderr, res.ExitCode — populated even on failure
```

An interactive shell, streams only — the consumer owns the terminal:

```go
sess, err := c.Shell(ctx, sshx.SessionConfig{
    Stdin: myIn, Stdout: myOut, Stderr: myErr,
    TTY: &sshx.TTYConfig{Term: "xterm-256color", Cols: w, Rows: h},
})
// on window change: sess.Resize(w, h)
err = sess.Wait()
```

## Why it matters

- **Fail-closed by construction.** There is no default that skips host-key verification, no way to store a plaintext password in a config struct, and a changed host key is never confirmable — it is always a hard error, because that's what a man-in-the-middle looks like. Unknown-host trust decisions and passphrases reach you as callbacks; what you do with them is your policy.
- **Standards underneath.** The SSH protocol itself (RFC 4251–4254) is `golang.org/x/crypto/ssh`, kept current by the Go team — sshx adds no crypto and exposes no algorithm-downgrade knobs. Key generation defaults to Ed25519 (RFC 8709) and refuses RSA below 2048 bits.
- **Built for many hosts.** Backoff jitter prevents reconnection stampedes, the dial cap respects `sshd MaxStartups`, and not-ready-never-blocks means one dead machine can't freeze a fleet view.
- **No UI opinions.** Everything interactive is a stream or a callback, so the same library serves a TUI, a web app, and an unattended job.

The `keys` invariants are fuzz-tested, not asserted: 20M+ generated inputs against `ParsePrivate` (arbitrary bytes never panic; a nil error always carries a usable signer) and ~700k comment round-trips against `Generate` (every accepted comment survives an authorized_keys parse round-trip intact, on exactly one line). The comment fuzzer caught a real bug before first release — whitespace-only comments silently vanished in the round-trip — which is why `Generate` refuses edge-whitespace comments and the crasher lives in the committed corpus as a permanent regression test.

<details>
<summary>Fuzzing — commands (30s smoke; release runs are longer)</summary>

```text
$ cd keys
$ go test -run='^$' -fuzz=FuzzParsePrivate -fuzztime=30s .
$ go test -run='^$' -fuzz=FuzzGenerateComment -fuzztime=30s .
```

</details>

## What it costs

Measured on a MacBook Pro — Apple M2 Pro (10 cores), 16 GB RAM, macOS 26.5.2, go1.26.6, against an in-process SSH server on loopback:

```bash
cd x/sshx && go test -run='^$' -bench=. -benchmem .
```

<details>
<summary>Raw output</summary>

```text
goos: darwin
goarch: arm64
pkg: github.com/Wigata-Intech/w-tools/x/sshx
cpu: Apple M2 Pro
BenchmarkManagedCombinedOutput-10       6848      169520 ns/op     71600 B/op      138 allocs/op
BenchmarkPoolColdStart-10                265     5118863 ns/op   1468634 B/op     9103 allocs/op
BenchmarkClientCombinedOutput-10        7852      136943 ns/op     71568 B/op      138 allocs/op
PASS
ok      github.com/Wigata-Intech/w-tools/x/sshx 3.996s
```

</details>

| Situation | Cost | Meaning for you |
| --------- | ---- | --------------- |
| One command, bare `Client` | ~140µs, 138 allocs | The floor: a full SSH channel open→exec→close round-trip on the multiplexed transport — protocol, not overhead added here |
| One command, pooled `Managed` | ~170µs, 138 allocs | The self-healing wrapper adds a mutex acquisition and error classification — identical allocations, round-trip dominated |
| 16-host fleet, cold start to all-Ready | ~5ms total | Sixteen full handshakes through the shared dial semaphore |

Structural costs: one background goroutine per live connection (keepalive) plus one per `Managed` (maintenance loop), both exiting on close; and the module requires Go 1.25+ with `golang.org/x/crypto` — the one dependency this repo's policy admits, scoped to `x/` modules implementing a protocol the standard library doesn't.

## The promises

As of `v0.1.0`:

- Host-key verification cannot be disabled by accident: every path is pinned unless you explicitly construct `InsecureAcceptAny`.
- Command output is never discarded on failure — whatever arrived is returned alongside the error.
- `Managed` execution methods return `ErrNotReady` immediately when the host is down; they never block waiting for a reconnect.
- A non-zero remote exit never tears down the connection; only transport-level failures trigger redial.
- All errors are typed; nothing in this module inspects error message text, and yours doesn't have to either.
- The `x/` contract: this API may change or vanish. Pin a tag.
