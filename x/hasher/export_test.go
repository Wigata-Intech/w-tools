package hasher

import "io"

// ParseArgon2id exposes the PHC parser for fuzzing: the parser is the
// attacker-adjacent surface, and the fuzzer must not run the KDF at
// fuzz-chosen cost parameters.
var ParseArgon2id = parseArgon2id //nolint:gochecknoglobals // test-only export shim

// SetRandSource swaps the salt entropy source for a test and returns a
// restore func.
func SetRandSource(r io.Reader) (restore func()) { //nolint:nonamedreturns // the name documents the contract: defer the restore
	prev := randSource
	randSource = r

	return func() { randSource = prev }
}

// Argon2Params mirrors the parsed parameter set for fuzz assertions.
type Argon2Params = argon2Params

func (p argon2Params) MemoryOf() uint32     { return p.memory }
func (p argon2Params) TimeOf() uint32       { return p.time }
func (p argon2Params) ParallelismOf() uint8 { return p.parallelism }
