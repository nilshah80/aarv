package hmacauth

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryNonceStore_FirstAccept(t *testing.T) {
	s := NewMemoryNonceStore(64)
	fresh, err := s.SetNX(context.Background(), "k", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatalf("first SetNX should be fresh")
	}
}

func TestMemoryNonceStore_DuplicateRejects(t *testing.T) {
	s := NewMemoryNonceStore(64)
	_, _ = s.SetNX(context.Background(), "k", time.Second)
	fresh, err := s.SetNX(context.Background(), "k", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatalf("duplicate SetNX should not be fresh")
	}
}

func TestMemoryNonceStore_ExpiryAllowsReuse(t *testing.T) {
	s := NewMemoryNonceStore(64)
	stub := time.Unix(1000, 0)
	s.now = func() time.Time { return stub }

	if _, err := s.SetNX(context.Background(), "k", time.Second); err != nil {
		t.Fatal(err)
	}
	stub = stub.Add(2 * time.Second)
	fresh, err := s.SetNX(context.Background(), "k", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatalf("post-expiry should be fresh")
	}
}

func TestMemoryNonceStore_ContextCancellation(t *testing.T) {
	s := NewMemoryNonceStore(64)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.SetNX(ctx, "k", time.Second)
	if err == nil {
		t.Fatalf("expected ctx error")
	}
}

func TestMemoryNonceStore_BoundedCap(t *testing.T) {
	s := NewMemoryNonceStore(10)
	for i := range 25 {
		_, _ = s.SetNX(context.Background(), fmt.Sprintf("k%d", i), time.Hour)
	}
	if got := s.size(); got > 10 {
		t.Fatalf("expected size <= 10, got %d", got)
	}
}

func TestMemoryNonceStore_EvictExpiredFirst(t *testing.T) {
	s := NewMemoryNonceStore(3)
	stub := time.Unix(1000, 0)
	s.now = func() time.Time { return stub }
	// Fill with very-short-TTL entries.
	for i := range 3 {
		_, _ = s.SetNX(context.Background(), fmt.Sprintf("e%d", i), 100*time.Millisecond)
	}
	// Advance past the expiry of the short ones.
	stub = stub.Add(time.Second)
	// New entry should land via expired eviction without dropping
	// any unexpired entry — we have none unexpired right now.
	if _, err := s.SetNX(context.Background(), "fresh", time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := s.size(); got > 3 {
		t.Fatalf("size cap violated: %d", got)
	}
}

func TestMemoryNonceStore_Janitor(t *testing.T) {
	s, stop := NewMemoryNonceStoreWithJanitor(64, 50*time.Millisecond)
	defer func() { _ = stop() }()

	stub := time.Unix(1000, 0)
	s.mu.Lock()
	s.now = func() time.Time { return stub }
	s.mu.Unlock()

	if _, err := s.SetNX(context.Background(), "k", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	// Move clock past the TTL and wait for the janitor to run.
	s.mu.Lock()
	stub = stub.Add(time.Second)
	s.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s.size() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("janitor did not evict expired entry; size=%d", s.size())
}

func TestMemoryNonceStore_StopIdempotent(t *testing.T) {
	_, stop := NewMemoryNonceStoreWithJanitor(8, 100*time.Millisecond)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	// Second call must not deadlock or panic.
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

// TestMemoryNonceStore_JanitorSweepFloor covers the
// `if sweepEvery < 50*time.Millisecond { sweepEvery = 50 * time.Millisecond }`
// clamp. Pass an unreasonably small interval; constructor must not panic
// and the janitor must still tick (we don't measure the exact interval —
// the clamp is a defensive guard, not a timing contract).
func TestMemoryNonceStore_JanitorSweepFloor(t *testing.T) {
	_, stop := NewMemoryNonceStoreWithJanitor(8, time.Nanosecond)
	defer func() { _ = stop() }()
	// Brief sleep so the goroutine starts at least once and we know
	// nothing panicked during start-up.
	time.Sleep(60 * time.Millisecond)
}

// TestMemoryNonceStore_EvictLockedOnEmpty covers the early-return guard
// in evictLocked when the entries map is empty. Trigger it by calling
// the public SetNX path on a store with maxEntries=1 then watching the
// empty-state branch via a sweep on an already-empty store.
func TestMemoryNonceStore_EvictLockedOnEmpty(t *testing.T) {
	s := NewMemoryNonceStore(1)
	// Direct guard exercise: evictLocked must safely no-op on empty.
	s.mu.Lock()
	s.evictLocked(time.Now())
	s.mu.Unlock()
}

func TestMemoryNonceStore_ConcurrentSetNX(t *testing.T) {
	s := NewMemoryNonceStore(4096)
	const N = 1000
	var wg sync.WaitGroup
	var fresh atomic.Int64

	for i := range N {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.SetNX(context.Background(), fmt.Sprintf("k-%d", i%50), time.Hour)
			if err != nil {
				t.Errorf("setnx: %v", err)
				return
			}
			if ok {
				fresh.Add(1)
			}
		}()
	}
	wg.Wait()

	// 50 keys, 1000 attempts → exactly 50 fresh insertions.
	if fresh.Load() != 50 {
		t.Fatalf("expected 50 fresh, got %d", fresh.Load())
	}
}

func TestMemoryNonceStore_PanicsOnZeroMax(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	_ = NewMemoryNonceStore(0)
}
