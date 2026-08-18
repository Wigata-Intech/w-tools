// The two hasher setups side by side: a fresh service (argon2id only)
// and a service migrating an existing bcrypt credential store. Run it
// and read the output — each hash costs ~23ms by design.
package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Wigata-Intech/w-tools/x/hasher"
)

// bcryptRow is a pre-migration credential row, as an old PHP or Go
// service would have stored it (bcrypt of "correct horse battery staple").
const bcryptRow = "$2a$04$MNh1npW4Iaqa1qcLgewgweE6Xqu6gGBvhak75G0PLZPd6eUN.Rjou"

func main() {
	password := "correct horse battery staple"

	// A fresh service: zero config, argon2id at the RFC 9106 profile.
	fresh := hasher.New(hasher.Config{})

	stored, err := fresh.Hash(password)
	if err != nil {
		panic(err)
	}
	fmt.Println("register stores:", stored[:44]+"…")

	fmt.Println("login with the right password:", fresh.Verify(password, stored)) // <nil>
	fmt.Println("login with the wrong password:", fresh.Verify("guess", stored))  // hasher: password mismatch
	fmt.Println("current hash needs rehash?", fresh.NeedsRehash(stored))          // false

	// Without Legacy, a bcrypt row is refused — the fresh service never
	// silently accepts a scheme it did not choose.
	fmt.Println("bcrypt on the fresh service:", errors.Is(fresh.Verify(password, bcryptRow), hasher.ErrUnsupportedScheme)) // true

	// A migrating service: same hasher, bcrypt accepted verify-only.
	migrating := hasher.New(hasher.Config{Legacy: []hasher.Scheme{hasher.Bcrypt}})

	fmt.Println("bcrypt login while migrating:", migrating.Verify(password, bcryptRow)) // <nil>
	fmt.Println("bcrypt needs rehash?", migrating.NeedsRehash(bcryptRow))               // true — always, for legacy

	// The login handler's upgrade step: re-hash while the plaintext is
	// in hand and persist. The row is now argon2id; next login skips this.
	upgraded, err := migrating.Hash(password)
	if err != nil {
		panic(err)
	}
	fmt.Println("row upgraded to:", strings.SplitAfterN(upgraded, "$", 3)[1]+"…") // argon2id$…
	fmt.Println("upgraded needs rehash?", migrating.NeedsRehash(upgraded))        // false
}
