package hasher_test

import (
	"testing"

	"github.com/Wigata-Intech/w-tools/x/hasher"
)

// BenchmarkHash: one hash at the default RFC 9106 parameters is
// deliberately tens of milliseconds.
func BenchmarkHash(b *testing.B) {
	h := hasher.New(hasher.Config{})
	b.ReportAllocs()
	for range b.N {
		if _, err := h.Hash(password); err != nil {
			b.Fatalf("Hash: %v", err)
		}
	}
}

// BenchmarkVerify pays the same KDF cost as Hash plus the parse.
func BenchmarkVerify(b *testing.B) {
	h := hasher.New(hasher.Config{})
	encoded, err := h.Hash(password)
	if err != nil {
		b.Fatalf("Hash: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := h.Verify(password, encoded); err != nil {
			b.Fatalf("Verify: %v", err)
		}
	}
}
