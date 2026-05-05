package session

import (
	"sync"
	"time"
)

// MemoryStore is the default in-process Store. Lifecycle:
//
//	NewMemoryStore               → lazy TTL eviction (no goroutine).
//	NewMemoryStoreWithJanitor(d) → adds a periodic sweep goroutine; the
//	                               returned stop function is idempotent
//	                               and joins the sweeper. Wire stop into
//	                               app.OnShutdown for clean teardown.
//
// All boundary methods clone Stored.Data and Stored.Flash so the
// caller and the backend never share live map instances. Callers can
// therefore mutate returned maps without affecting stored state, and
// stored state cannot be mutated through a previously-returned
// reference. Concurrent map access is serialized on the store mutex.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]*memEntry
	stopCh  chan struct{}
	doneCh  chan struct{}
}

type memEntry struct {
	stored *Stored
	expiry time.Time // zero = no expiry
}

// NewMemoryStore returns a MemoryStore with lazy TTL eviction. Expired
// entries are dropped in-line during Get; there is no background
// goroutine. Suitable for single-process deployments where periodic
// sweeping is not required.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]*memEntry)}
}

// NewMemoryStoreWithJanitor returns a MemoryStore plus a stop function
// that gracefully terminates the periodic sweep goroutine. Panics on
// sweep <= 0.
//
// Wire the stop function into app.OnShutdown so it joins the sweeper
// before the process exits:
//
//	store, stop := session.NewMemoryStoreWithJanitor(time.Minute)
//	app.OnShutdown(func(ctx interface{ Done() <-chan struct{} }) error {
//	    return stop()
//	})
func NewMemoryStoreWithJanitor(sweep time.Duration) (*MemoryStore, func() error) {
	if sweep <= 0 {
		panic("session: NewMemoryStoreWithJanitor sweep must be > 0")
	}
	s := NewMemoryStore()
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	go func() {
		defer close(s.doneCh)
		t := time.NewTicker(sweep)
		defer t.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-t.C:
				s.sweepExpired()
			}
		}
	}()
	var once sync.Once
	stop := func() error {
		once.Do(func() {
			close(s.stopCh)
			<-s.doneCh
		})
		return nil
	}
	return s, stop
}

// Get implements Store. Returns (nil, nil) for missing or expired
// entries; expired entries are evicted in-line.
func (s *MemoryStore) Get(id string) (*Stored, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return nil, nil
	}
	if !e.expiry.IsZero() && e.expiry.Before(time.Now()) {
		delete(s.entries, id)
		return nil, nil
	}
	return cloneStored(e.stored), nil
}

// Save implements Store. The provided *Stored is cloned before being
// stored so the caller cannot mutate the backend's copy via a retained
// reference.
func (s *MemoryStore) Save(id string, st *Stored, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := &memEntry{stored: cloneStored(st)}
	if ttl > 0 {
		e.expiry = time.Now().Add(ttl)
	}
	s.entries[id] = e
	return nil
}

// Delete implements Store. Deleting a missing key is a no-op.
func (s *MemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
	return nil
}

func (s *MemoryStore) sweepExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.entries {
		if !e.expiry.IsZero() && e.expiry.Before(now) {
			delete(s.entries, k)
		}
	}
}

// cloneStored is a defensive deep-ish copy: maps are cloned so callers
// and the store don't share live instances. Map *values* remain shared
// references — sessions should treat values as immutable once Set, as
// documented in package docs.
func cloneStored(s *Stored) *Stored {
	if s == nil {
		return nil
	}
	out := &Stored{CSRF: s.CSRF}
	out.Data = cloneMap(s.Data)
	out.Flash = cloneMap(s.Flash)
	return out
}

// Compile-time guarantee that MemoryStore satisfies Store.
var _ Store = (*MemoryStore)(nil)
