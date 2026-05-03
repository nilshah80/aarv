package hmacauth

import (
	"context"
	"sync"
	"time"
)

// NonceStore tracks per-client nonces so a captured signed request
// cannot be replayed within the configured window.
//
// SetNX MUST be atomic: simultaneous calls with the same key from
// multiple goroutines or processes return fresh=true at most once.
// On a fresh insert the implementation MUST also set an expiry of at
// least ttl so the key is reclaimed automatically.
//
// The fresh return value distinguishes "first time we have seen this
// nonce" (fresh=true) from "we saw it earlier" (fresh=false). Errors
// short-circuit the request to a generic 401 — the middleware does not
// distinguish backend failure from replay externally.
type NonceStore interface {
	SetNX(ctx context.Context, key string, ttl time.Duration) (fresh bool, err error)
}

// MemoryNonceStore is the default in-process NonceStore. It is intended
// for development, tests, and single-process deployments. Production
// multi-instance deployments should use a Redis-backed store
// (plugins/hmacauth-redis) so replay protection is shared.
//
// MemoryNonceStore enforces a maximum entry count to bound memory in
// the face of attacker-controlled nonces. When the limit is reached,
// the oldest expired entries are evicted first; if none are expired,
// the oldest entry by insertion time is evicted (this last-resort
// eviction is rare in normal operation because TTLs are short).
type MemoryNonceStore struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	max     int
	stopCh  chan struct{}
	doneCh  chan struct{}
	now     func() time.Time
}

type memoryEntry struct {
	expires time.Time
	added   time.Time
}

// NewMemoryNonceStore creates a MemoryNonceStore with the given maximum
// entry count. maxEntries <= 0 panics — an unbounded in-process map is
// a memory exhaustion vector.
func NewMemoryNonceStore(maxEntries int) *MemoryNonceStore {
	if maxEntries <= 0 {
		panic("hmacauth: NewMemoryNonceStore requires maxEntries > 0")
	}
	return &MemoryNonceStore{
		entries: make(map[string]memoryEntry),
		max:     maxEntries,
		now:     time.Now,
	}
}

// NewMemoryNonceStoreWithJanitor returns a MemoryNonceStore plus a stop
// function that terminates the periodic eviction goroutine. Wire the
// stop function via app.OnShutdown so the goroutine does not leak when
// the process restarts.
//
// sweepEvery controls the eviction cadence. Values < 50ms are clamped
// to 50ms — anything tighter wastes CPU without measurable benefit.
func NewMemoryNonceStoreWithJanitor(maxEntries int, sweepEvery time.Duration) (*MemoryNonceStore, func() error) {
	s := NewMemoryNonceStore(maxEntries)
	if sweepEvery < 50*time.Millisecond {
		sweepEvery = 50 * time.Millisecond
	}
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	go s.runJanitor(sweepEvery)
	stop := func() error {
		select {
		case <-s.stopCh:
			// Already stopped — Stop is idempotent so subsequent
			// app.OnShutdown invocations during process teardown do
			// not panic.
			return nil
		default:
		}
		close(s.stopCh)
		<-s.doneCh
		return nil
	}
	return s, stop
}

// SetNX implements NonceStore.SetNX.
func (s *MemoryNonceStore) SetNX(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ctx != nil {
		// Honor cancellation before taking the lock so a cancelled
		// caller does not block other goroutines on this mutex.
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if e, ok := s.entries[key]; ok {
		if now.Before(e.expires) {
			return false, nil
		}
		// Stale — fall through to insert.
	}

	if len(s.entries) >= s.max {
		s.evictLocked(now)
	}

	s.entries[key] = memoryEntry{
		expires: now.Add(ttl),
		added:   now,
	}
	return true, nil
}

// evictLocked removes expired entries. If none were expired, removes
// the single oldest entry by insertion time so the cap holds.
//
// This is intentionally O(n) — the eviction path is exercised only
// when the store is at capacity, which should be rare in normal
// operation. If profile data later shows this is a hot path, replace
// with a heap or LRU list.
func (s *MemoryNonceStore) evictLocked(now time.Time) {
	if len(s.entries) == 0 {
		return
	}
	removed := false
	for k, e := range s.entries {
		if !now.Before(e.expires) {
			delete(s.entries, k)
			removed = true
		}
	}
	if removed {
		return
	}
	var oldestKey string
	var oldestAt time.Time
	first := true
	for k, e := range s.entries {
		if first || e.added.Before(oldestAt) {
			oldestKey = k
			oldestAt = e.added
			first = false
		}
	}
	delete(s.entries, oldestKey)
}

func (s *MemoryNonceStore) runJanitor(every time.Duration) {
	defer close(s.doneCh)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.mu.Lock()
			now := s.now()
			for k, e := range s.entries {
				if !now.Before(e.expires) {
					delete(s.entries, k)
				}
			}
			s.mu.Unlock()
		}
	}
}

// size returns the current entry count. Test-only.
func (s *MemoryNonceStore) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
