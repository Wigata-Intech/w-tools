// Package hasher stores and verifies passwords with argon2id.
//
// Hash emits the PHC string format, so cost parameters travel inside
// each hash and can be raised later without breaking stored ones;
// Verify recomputes with the stored string's own parameters and
// compares in constant time; NeedsRehash reports hashes below current
// policy so a service can transparently re-hash at login. Existing
// bcrypt credential stores migrate by opting in via Config.Legacy —
// bcrypt hashes then verify but always report NeedsRehash, and Hash
// never emits them.
package hasher
