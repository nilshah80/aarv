package session

import (
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	s := NewMemoryStore()
	st := &Stored{Data: map[string]any{"k": "v"}, CSRF: "tok"}
	if err := s.Save("id", st, time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("id")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Data["k"] != "v" || got.CSRF != "tok" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestMemoryStoreMissing(t *testing.T) {
	s := NewMemoryStore()
	got, err := s.Get("nope")
	if err != nil || got != nil {
		t.Fatalf("missing key returned (%v, %v); want (nil, nil)", got, err)
	}
}

func TestMemoryStoreLazyExpiry(t *testing.T) {
	s := NewMemoryStore()
	if err := s.Save("id", &Stored{Data: map[string]any{"k": "v"}}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	got, err := s.Get("id")
	if err != nil || got != nil {
		t.Fatalf("expired entry returned (%v, %v)", got, err)
	}
	// Verify lazy eviction actually deleted the map entry.
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries["id"]; ok {
		t.Fatal("expired entry should be evicted from map after Get")
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	s := NewMemoryStore()
	_ = s.Save("id", &Stored{Data: map[string]any{"k": "v"}}, time.Hour)
	if err := s.Delete("id"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("id"); got != nil {
		t.Fatal("Delete did not remove entry")
	}
	// Delete on missing is a no-op.
	if err := s.Delete("never"); err != nil {
		t.Fatalf("Delete on missing returned %v; want nil", err)
	}
}

func TestMemoryStoreDeepCopyOnGet(t *testing.T) {
	s := NewMemoryStore()
	original := &Stored{Data: map[string]any{"k": "v"}, Flash: map[string]any{"f": "x"}}
	_ = s.Save("id", original, time.Hour)

	got, _ := s.Get("id")
	got.Data["k"] = "mutated"
	got.Flash["f"] = "mutated"

	got2, _ := s.Get("id")
	if got2.Data["k"] != "v" || got2.Flash["f"] != "x" {
		t.Fatal("Get must return a deep copy so callers can't mutate stored state")
	}
}

func TestMemoryStoreDeepCopyOnSave(t *testing.T) {
	s := NewMemoryStore()
	st := &Stored{Data: map[string]any{"k": "v"}}
	_ = s.Save("id", st, time.Hour)

	// Mutate the caller's map after Save — must not affect stored state.
	st.Data["k"] = "mutated"

	got, _ := s.Get("id")
	if got.Data["k"] != "v" {
		t.Fatal("Save must deep-copy so callers can't mutate stored state via aliased map")
	}
}

func TestMemoryStoreConcurrent(t *testing.T) {
	s := NewMemoryStore()
	var wg sync.WaitGroup
	const writers, readers, iters = 10, 10, 100
	for i := range writers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range iters {
				_ = s.Save("k", &Stored{Data: map[string]any{"i": id, "j": j}}, time.Hour)
			}
		}(i)
	}
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iters {
				if got, _ := s.Get("k"); got != nil {
					// Read both fields to ensure the race detector sees access.
					_ = got.Data["i"]
					_ = got.Data["j"]
				}
			}
		}()
	}
	wg.Wait()
}

func TestMemoryStoreJanitor(t *testing.T) {
	s, stop := NewMemoryStoreWithJanitor(2 * time.Millisecond)
	t.Cleanup(func() {
		_ = stop()
		// Idempotent.
		_ = stop()
	})
	_ = s.Save("k", &Stored{Data: map[string]any{"x": 1}}, time.Millisecond)
	// Wait long enough for at least two sweep ticks after expiry.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, present := s.entries["k"]
		s.mu.Unlock()
		if !present {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("janitor did not sweep expired entry")
}

func TestMemoryStoreJanitorPanicOnNonPositiveSweep(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on sweep <= 0")
		}
	}()
	_, _ = NewMemoryStoreWithJanitor(0)
}

func TestMemoryStoreZeroTTL(t *testing.T) {
	s := NewMemoryStore()
	_ = s.Save("id", &Stored{Data: map[string]any{"k": "v"}}, 0)
	// Zero TTL means no expiry — entry should remain.
	time.Sleep(5 * time.Millisecond)
	if got, _ := s.Get("id"); got == nil {
		t.Fatal("entry with zero TTL should not expire")
	}
}
