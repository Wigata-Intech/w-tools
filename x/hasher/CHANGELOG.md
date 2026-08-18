# Changelog

All notable changes to `x/hasher` are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is semver under the `x/` contract — v0 forever until graduation, deletion is a legitimate outcome. Tags: `x/hasher/vX.Y.Z`.

## [Unreleased]

## [0.1.0] - 2026-08-18

### Added

- Initial implementation: argon2id password hashing on the RFC 9106 / OWASP default profile, PHC string encoding, constant-time `Verify` with parameters read from the stored hash, `NeedsRehash` for transparent parameter upgrades, opt-in bcrypt legacy verification (`Config.Legacy`) for store migrations
- Typed errors: `ErrMismatch`, `ErrUnsupportedScheme`, `ErrMalformed` — a corrupt column is never reported as a wrong password
- Verification bounds: stored-hash parameters are capped before the KDF runs, so a poisoned column cannot panic the process or demand unbounded memory
- `FuzzParseArgon2id` on the PHC parser; pinned-fixture tests guaranteeing hashes minted today verify forever
- Runnable example (`examples/migration`): the fresh-service and bcrypt-migration setups side by side
- Module dependency: `golang.org/x/crypto` (argon2, bcrypt) — the second entry on the `x/` allowlist, approved with the design 2026-08-18
