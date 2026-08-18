package middleware_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wigata-Intech/w-tools/httpx/middleware"
)

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()

	t.Run("SetNX claims an absent key", func(t *testing.T) {
		s := middleware.NewMemoryStore(0)

		stored, err := s.SetNX(ctx, "k", []byte("claim"), time.Minute)
		if err != nil || !stored {
			t.Fatalf("SetNX(absent) = (%v, %v), want (true, nil)", stored, err)
		}
	})

	t.Run("SetNX on an existing key reports false", func(t *testing.T) {
		s := middleware.NewMemoryStore(0)
		mustSetNX(ctx, t, s, "k", "first")

		stored, err := s.SetNX(ctx, "k", []byte("second"), time.Minute)
		if err != nil || stored {
			t.Fatalf("SetNX(existing) = (%v, %v), want (false, nil)", stored, err)
		}
		if got := mustGet(ctx, t, s, "k"); got != "first" {
			t.Fatalf("value = %q, want the first write", got)
		}
	})

	t.Run("Get reports absent keys", func(t *testing.T) {
		s := middleware.NewMemoryStore(0)

		if _, ok, err := s.Get(ctx, "nope"); ok || err != nil {
			t.Fatalf("Get(absent) = (ok %v, err %v), want (false, nil)", ok, err)
		}
	})

	t.Run("Set replaces the value and keeps the TTL", func(t *testing.T) {
		now := time.Unix(1000, 0)
		restore := middleware.SetTimeNow(func() time.Time { return now })
		defer restore()

		s := middleware.NewMemoryStore(0)
		mustSetNX(ctx, t, s, "k", "claim")

		if err := s.Set(ctx, "k", []byte("done")); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if got := mustGet(ctx, t, s, "k"); got != "done" {
			t.Fatalf("value = %q, want the replacement", got)
		}

		now = now.Add(61 * time.Second)
		if _, ok, _ := s.Get(ctx, "k"); ok {
			t.Fatal("Set must keep the original TTL, not extend it")
		}
	})

	t.Run("Set on an absent key is a no-op", func(t *testing.T) {
		s := middleware.NewMemoryStore(0)

		if err := s.Set(ctx, "k", []byte("ghost")); err != nil {
			t.Fatalf("Set(absent): %v", err)
		}
		if _, ok, _ := s.Get(ctx, "k"); ok {
			t.Fatal("Set must not create keys")
		}
	})

	t.Run("Delete frees the key for a new claim", func(t *testing.T) {
		s := middleware.NewMemoryStore(0)
		mustSetNX(ctx, t, s, "k", "claim")

		if err := s.Delete(ctx, "k"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if stored, _ := s.SetNX(ctx, "k", []byte("again"), time.Minute); !stored {
			t.Fatal("SetNX after Delete must claim")
		}
	})

	t.Run("expired keys are reclaimed", func(t *testing.T) {
		now := time.Unix(1000, 0)
		restore := middleware.SetTimeNow(func() time.Time { return now })
		defer restore()

		s := middleware.NewMemoryStore(0)
		mustSetNX(ctx, t, s, "k", "claim")

		now = now.Add(61 * time.Second)
		if _, ok, _ := s.Get(ctx, "k"); ok {
			t.Fatal("Get after expiry must miss")
		}
		if stored, _ := s.SetNX(ctx, "k", []byte("fresh"), time.Minute); !stored {
			t.Fatal("SetNX after expiry must claim")
		}
	})

	t.Run("at capacity expired keys are swept", testMemoryStoreCapacitySweepsExpired)

	t.Run("at capacity live keys are refused not evicted", testMemoryStoreCapacityRefusesNotEvicts)

	t.Run("at-capacity sweeps are throttled", testMemoryStoreSweepThrottle)

	t.Run("values are copies not aliases", func(t *testing.T) {
		s := middleware.NewMemoryStore(0)
		in := []byte("claim")
		if _, err := s.SetNX(ctx, "k", in, time.Minute); err != nil {
			t.Fatalf("SetNX: %v", err)
		}
		in[0] = 'X'

		out, _, _ := s.Get(ctx, "k")
		if string(out) != "claim" {
			t.Fatalf("stored value = %q, mutated through the caller's slice", out)
		}
		out[0] = 'Y'
		if got := mustGet(ctx, t, s, "k"); got != "claim" {
			t.Fatalf("stored value = %q, mutated through Get's return", got)
		}
	})

	t.Run("concurrent SetNX claims exactly once", testMemoryStoreConcurrentSetNX)
}

func mustSetNX(ctx context.Context, t *testing.T, s *middleware.MemoryStore, key, value string) {
	t.Helper()
	stored, err := s.SetNX(ctx, key, []byte(value), time.Minute)
	if err != nil || !stored {
		t.Fatalf("SetNX(%q) = (%v, %v), want (true, nil)", key, stored, err)
	}
}

func mustGet(ctx context.Context, t *testing.T, s *middleware.MemoryStore, key string) string {
	t.Helper()
	v, ok, err := s.Get(ctx, key)
	if err != nil || !ok {
		t.Fatalf("Get(%q) = (ok %v, err %v), want a value", key, ok, err)
	}

	return string(v)
}

func testMemoryStoreCapacitySweepsExpired(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1000, 0)
	restore := middleware.SetTimeNow(func() time.Time { return now })
	defer restore()

	s := middleware.NewMemoryStore(4)
	for i := range 4 {
		mustSetNX(ctx, t, s, fmt.Sprintf("old-%d", i), "v")
	}

	now = now.Add(2 * time.Minute)
	if stored, err := s.SetNX(ctx, "fresh", []byte("v"), time.Minute); err != nil || !stored {
		t.Fatalf("SetNX at capacity with everything expired = (%v, %v), want (true, nil)", stored, err)
	}
}

func testMemoryStoreCapacityRefusesNotEvicts(t *testing.T) {
	ctx := context.Background()
	s := middleware.NewMemoryStore(4)
	for i := range 4 {
		mustSetNX(ctx, t, s, fmt.Sprintf("live-%d", i), "v")
	}

	if stored, err := s.SetNX(ctx, "fresh", []byte("v"), time.Minute); err == nil || stored {
		t.Fatalf("SetNX over a full store = (%v, %v), want an error — evicting a live record could drop a claim", stored, err)
	}
	for i := range 4 {
		if _, ok, _ := s.Get(ctx, fmt.Sprintf("live-%d", i)); !ok {
			t.Fatalf("live-%d was evicted", i)
		}
	}
}

func testMemoryStoreConcurrentSetNX(t *testing.T) {
	ctx := context.Background()
	s := middleware.NewMemoryStore(0)

	const n = 50
	var wg sync.WaitGroup
	claims := make([]bool, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claims[i], _ = s.SetNX(ctx, "k", []byte("v"), time.Minute)
		}()
	}
	wg.Wait()

	var won int
	for _, c := range claims {
		if c {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("concurrent SetNX stored %d times, want exactly 1", won)
	}
}

func testMemoryStoreSweepThrottle(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1000, 0)
	restore := middleware.SetTimeNow(func() time.Time { return now })
	defer restore()

	s := middleware.NewMemoryStore(2)
	mustSetNX(ctx, t, s, "a", "v")
	mustSetNX(ctx, t, s, "b", "v")

	// First at-capacity sweep runs and reclaims the expired pair.
	now = now.Add(2 * time.Minute)
	if stored, _ := s.SetNX(ctx, "c", []byte("v"), 100*time.Millisecond); !stored {
		t.Fatal("first at-capacity SetNX must sweep and claim")
	}
	if stored, _ := s.SetNX(ctx, "d", []byte("v"), 100*time.Millisecond); !stored {
		t.Fatal("second SetNX must claim the freed slot")
	}

	// Both expired again, but within the throttle window the sweep is
	// skipped and the write refused.
	now = now.Add(200 * time.Millisecond)
	if stored, err := s.SetNX(ctx, "e", []byte("v"), time.Minute); err == nil || stored {
		t.Fatalf("SetNX inside the throttle window = (%v, %v), want refusal", stored, err)
	}

	// Past the window the sweep runs again.
	now = now.Add(time.Second)
	if stored, _ := s.SetNX(ctx, "e", []byte("v"), time.Minute); !stored {
		t.Fatal("SetNX past the throttle window must sweep and claim")
	}
}
