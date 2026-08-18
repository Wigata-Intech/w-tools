package hasher_test

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/w-tools/x/hasher"
)

// FuzzParseArgon2id feeds arbitrary stored-credential bytes through the
// PHC parser — the attacker-adjacent surface, since Verify trusts the
// column's own parameters. Invariants: never panic, and anything the
// parser accepts is within the verification caps, so the KDF can
// neither be panicked nor turned into a resource amplifier. The KDF
// itself deliberately never runs here — fuzz-chosen costs would make
// the run arbitrarily slow.
func FuzzParseArgon2id(f *testing.F) {
	f.Add(fixtureFast)
	f.Add(fixtureDefault)
	f.Add("$argon2id$v=19$m=8,t=1,p=1$AAAA$AAAA")
	f.Add("$argon2id$v=19$m=64,t=1,p=1$$")
	f.Add("$argon2id$v=19$m=-1,t=1,p=1$AAAA$AAAA")
	f.Add("$argon2id$v=19$m=999999999999,t=1,p=1$AAAA$AAAA")
	f.Add("$argon2id$")
	f.Add("")

	f.Fuzz(func(t *testing.T, encoded string) {
		p, salt, key, err := hasher.ParseArgon2id(encoded)
		if err != nil {
			return
		}

		if !strings.HasPrefix(encoded, "$argon2id$") {
			t.Fatalf("parser accepted a non-argon2id string %q", encoded)
		}
		if p.TimeOf() < 1 || p.ParallelismOf() < 1 || p.MemoryOf() < 8*uint32(p.ParallelismOf()) {
			t.Fatalf("accepted parameters below argon2 minimums: %+v", p)
		}
		if len(salt) == 0 || len(salt) > 256 || len(key) == 0 || len(key) > 512 {
			t.Fatalf("accepted salt/key lengths out of bounds: %d/%d", len(salt), len(key))
		}
	})
}
