# Security Policy

This policy covers **every module in this repository** — current and future.

## Reporting a vulnerability

**Do not open a public issue for security problems.** Use GitHub's private reporting: **Security → Report a vulnerability** on this repository. You'll get an acknowledgment within a few days, and we'll work the issue with you privately until a fix ships.

Reports must include a reproduction a human has verified. Auto-generated report volume is closed without investigation — see [AI_POLICY.md](AI_POLICY.md).

## What counts

For any module in this repo:

- Behavior that is documented as fail-closed failing open
- Panics, memory exhaustion, or unbounded work reachable through untrusted input
- Anything that silently violates a guarantee the module's README states

Modules whose *purpose* is a security property hold the highest bar — for example, `logger`'s redaction: any input that ships a configured key unredacted, or a mask that reveals more than its configuration allows, is a vulnerability there, not a bug. The same applies to `x/sshx`'s host-key verification: any path that accepts an unknown or changed host key without the explicit insecure opt-in or the consumer's confirmation is a vulnerability. And to `x/hasher`: a `Verify` that accepts a wrong password, a non-constant-time comparison of derived keys, or a stored hash whose parameters can panic or resource-exhaust the process is a vulnerability there.

## Supported versions

Fixes land on the latest tagged version of the affected module. Pre-1.0 modules receive fixes on their newest v0.x line only.
