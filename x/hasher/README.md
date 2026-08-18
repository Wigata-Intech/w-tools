# x/hasher

> Store passwords so that a stolen database still doesn't give up the passwords.

[![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/w-tools/x/hasher.svg)](https://pkg.go.dev/github.com/Wigata-Intech/w-tools/x/hasher)

**Status: experimental, under `x/`, at `v0.1.0`.** The `x/` contract applies in full: the API may break between minors, the experiment may fail, and the package may be **deleted outright**. Never build anything load-bearing on an `x/` package.

## TL;DR

```bash
go get github.com/Wigata-Intech/w-tools/x/hasher
```

```go
h := hasher.New(hasher.Config{})

// Register: turn the password into a hash, store the hash.
encoded, err := h.Hash(req.Password)

// Login: check the submitted password against the stored hash.
err = h.Verify(req.Password, user.PasswordHash) // nil = correct password

// After a successful login: upgrade old hashes transparently.
if h.NeedsRehash(user.PasswordHash) { /* Hash again, save the new one */ }
```

## What problem this solves

You must never store a password itself — when (not if) the users table leaks, every password leaks with it, and people reuse passwords everywhere. So you store a **hash**: the result of a one-way function. At login you hash what the user typed and compare. The original password is never on disk.

Plain hashes (SHA-256 etc.) are not enough: they run in nanoseconds, so an attacker with the leaked table can try billions of guesses per second on a GPU. A **password hash** is deliberately slow and memory-hungry, turning "billions per second" into "dozens per second". Getting this right means choosing the algorithm, its cost settings, a storage format, and a comparison method — four decisions this module makes once, correctly, for every Wigata service.

## How it solves it

**`Hash(password)`** runs argon2id — the algorithm current OWASP/RFC 9106 guidance recommends, chosen because it resists both GPU cracking (it needs 19 MiB of RAM per attempt) and timing side channels. It returns one self-describing string:

```text
$argon2id$v=19$m=19456,t=2,p=1$fkG4MaN0...$PcyP1G2C...
 └───┬───┘ └─┬─┘ └─────┬─────┘ └───┬────┘ └───┬────┘
 algorithm version cost settings   salt      the hash
```

Everything needed to check the password later travels *inside* the string — that's the point. The **salt** is fresh random bytes per hash, so two users with the same password get different hashes. The **cost settings** (19 MiB memory, 2 passes, 1 lane) are recorded per hash, so you can raise them later without breaking anything already stored.

**`Verify(password, encoded)`** reads the settings out of the stored string, recomputes, and compares in constant time (comparison speed reveals nothing). Its errors are typed and deliberately distinct:

- `ErrMismatch` — well-formed hash, wrong password. Show the user "invalid credentials".
- `ErrUnsupportedScheme` — the column holds a scheme you haven't enabled. Operational problem, not a wrong password.
- `ErrMalformed` — the column is corrupt. Also an incident; never report it as a failed login.

**`NeedsRehash(encoded)`** answers "is this stored hash below my current policy?" — an old bcrypt hash, or argon2id at weaker settings than today's config. The only moment you can fix an old hash is right after a successful login, because that's the only time the plaintext is legitimately in hand:

```go
if err := h.Verify(password, user.PasswordHash); err != nil {
    return ErrBadCredentials // same answer for unknown user and wrong password
}
if h.NeedsRehash(user.PasswordHash) {
    if newHash, err := h.Hash(password); err == nil {
        _ = repo.UpdatePasswordHash(ctx, user.ID, newHash) // best effort; login already succeeded
    }
}
```

### Migrating an existing bcrypt store

Turn on legacy verification and the loop above migrates the whole credential store one login at a time — no big-bang migration, no password resets:

```go
h := hasher.New(hasher.Config{Legacy: []hasher.Scheme{hasher.Bcrypt}})
```

`Verify` then also accepts `$2a$…` bcrypt hashes; `NeedsRehash` reports true for every one of them; `Hash` still only ever writes argon2id. With `Legacy` unset (the default), bcrypt hashes are rejected as unsupported.

One caveat: stores originating from pre-2011 PHP `crypt_blowfish` (`$2x$` hashes) had a bug for passwords containing non-ASCII bytes — those specific users may see a mismatch during migration and need a password reset.

Both setups run in [`examples/migration`](examples/migration/main.go) — `go run ./examples/migration` and read the output.

## Why it matters

The defaults are pinned by test to RFC 9106's recommended profile, the same one OWASP's password-storage cheat sheet points at. There is no algorithm knob to set wrong, verification parameters always come from the stored hash (old hashes verify forever), and stored-hash parameters are bounds-checked before the KDF runs — a poisoned credential column can't panic the process or demand gigabytes per login attempt.

## What it costs

One hash is deliberately expensive — **the cost is the defense**. Measured on a MacBook Pro — Apple M2 Pro (10 cores), 16 GB RAM, go1.26.6:

```text
$ go test -run=NONE -bench=. -benchtime=20x .
BenchmarkHash-10      20   23208873 ns/op   19926944 B/op   34 allocs/op
BenchmarkVerify-10    20   24637658 ns/op   19925629 B/op   27 allocs/op
```

~23ms and ~19.9 MiB per operation: budget roughly 40 logins/second/core, and put a rate limit in front of any endpoint that calls it (`httpx/middleware.RateLimit` exists for exactly this).

## The promises

As of `v0.1.0`:

- **argon2id only on write.** Legacy schemes are verify-only, opt-in, and always report `NeedsRehash`.
- **Old hashes verify forever.** Verification parameters come from the stored string, never from config.
- **A corrupt column is never a "wrong password".** `ErrMalformed`, `ErrMismatch`, and `ErrUnsupportedScheme` are distinct and `errors.Is`-able.
- **Bounded verification.** Stored-hash parameters are capped (2 GiB memory, t≤512, p≤64) before the KDF runs.
- **The `x/` contract.** v0 while experimental: the API may break between minors, and graduation to root re-opens the dependency question explicitly.
