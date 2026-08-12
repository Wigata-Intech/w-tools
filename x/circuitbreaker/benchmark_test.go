package circuitbreaker_test

import (
	"testing"

	"github.com/Wigata-Intech/w-tools/x/circuitbreaker"
)

// BenchmarkAllowRecord prices the closed-state hot path — the overhead
// every guarded call pays. The claim it guards: nanoseconds against a
// network call measured in milliseconds.
func BenchmarkAllowRecord(b *testing.B) {
	br := circuitbreaker.New(circuitbreaker.Config{})

	b.ReportAllocs()

	for range b.N {
		if err := br.Allow(); err != nil {
			b.Fatal(err)
		}
		br.Record(nil)
	}
}

// BenchmarkAllowRecordParallel prices the same path under concurrent
// callers hammering one breaker — run with -cpu 1,2,4,8 for the traffic
// curve. Contention on the single mutex is the number under test.
func BenchmarkAllowRecordParallel(b *testing.B) {
	br := circuitbreaker.New(circuitbreaker.Config{})

	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := br.Allow(); err == nil {
				br.Record(nil)
			}
		}
	})
}
