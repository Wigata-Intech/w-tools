# Changelog

All notable changes to `x/hasher` are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is semver under the `x/` contract — v0 forever until graduation, deletion is a legitimate outcome. Tags: `x/hasher/vX.Y.Z`.

## [Unreleased]

### Changed

- Minimum Go version raised to 1.26.8 (`go.mod` directive); Go 1.25 and earlier are out of upstream support, and `golang.org/x/crypto` v0.56.0+ requires Go 1.26
- `golang.org/x/crypto` v0.55.0 → v0.57.0 (`golang.org/x/sys` v0.47.0 → v0.48.0)

## [0.1.0] - 2026-08-18

### Added

- Initial implementation: argon2id password hashing on the RFC 9106 / OWASP default profile, PHC string encoding, constant-time `Verify` with parameters read from the stored hash, `NeedsRehash` for transparent parameter upgrades, opt-in bcrypt legacy verification (`Config.Legacy`) for store migrations
- Typed errors: `ErrMismatch`, `ErrUnsupportedScheme`, `ErrMalformed` — a corrupt column is never reported as a wrong password
- Verification bounds: stored-hash parameters are capped before the KDF runs, so a poisoned column cannot panic the process or demand unbounded memory
- `FuzzParseArgon2id` on the PHC parser; pinned-fixture tests guaranteeing hashes minted today verify forever
- Runnable example (`examples/migration`): the fresh-service and bcrypt-migration setups side by side
- Module dependency: `golang.org/x/crypto` (argon2, bcrypt) — the second entry on the `x/` allowlist, approved with the design 2026-08-18
