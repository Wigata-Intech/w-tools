package middleware

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// errStoreFull reports a MemoryStore at capacity with nothing expired
// to evict. Live records are never evicted; the write is refused and
// the middleware fails closed.
var errStoreFull = errors.New("middleware: idempotency store full")

// MemoryStore is a bounded in-process key-value store with per-key TTL —
// the in-package Store implementation, usable by anything needing a
// local TTL'd KV. Expired entries are dropped lazily. Suitable for
// single-instance services and tests; replicas need a shared backend
// (see Store).
type MemoryStore struct {
	maxRecords int

	mu        sync.Mutex
	entries   map[string]*memEntry
	lastSweep time.Time
}

type memEntry struct {
	value   []byte
	expires time.Time
}

// NewMemoryStore builds a MemoryStore holding at most maxRecords keys
// (0 or negative means DefaultMaxRecords).
func NewMemoryStore(maxRecords int) *MemoryStore {
	if maxRecords <= 0 {
		maxRecords = DefaultMaxRecords
	}

	return &MemoryStore{maxRecords: maxRecords, entries: make(map[string]*memEntry)}
}

// SetNX implements Store.
func (s *MemoryStore) SetNX(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	now := timeNow()

	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.entries[key]; ok {
		if now.Before(e.expires) {
			return false, nil
		}

		delete(s.entries, key)
	}

	if len(s.entries) >= s.maxRecords {
		if now.Sub(s.lastSweep) >= sweepThrottle {
			s.lastSweep = now
			for k, e := range s.entries {
				if !now.Before(e.expires) {
					delete(s.entries, k)
				}
			}
		}
		if len(s.entries) >= s.maxRecords {
			return false, errStoreFull
		}
	}

	s.entries[key] = &memEntry{value: slices.Clone(value), expires: now.Add(ttl)}

	return true, nil
}

// Get implements Store.
func (s *MemoryStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[key]
	if !ok || !timeNow().Before(e.expires) {
		return nil, false, nil
	}

	return slices.Clone(e.value), true, nil
}

// Set implements Store. Setting an absent or expired key is a no-op
// rather than a resurrection.
func (s *MemoryStore) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.entries[key]; ok && timeNow().Before(e.expires) {
		e.value = slices.Clone(value)
	}

	return nil
}

// Delete implements Store.
func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.entries, key)

	return nil
}
