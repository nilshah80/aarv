package idempotency

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Response is the captured shape persisted by Store.Save and replayed on
// retry. Hop-by-hop headers (Connection, Keep-Alive, Transfer-Encoding,
// Upgrade) are filtered before persistence in writer.go's snapshot path.
type Response struct {
	StatusCode  int
	Headers     http.Header
	Body        []byte
	PayloadHash [32]byte // sha256 of the request body; zero = unset
}

// Store is the persistence contract for cached responses. The minimal
// surface (Lock / Unlock / Get / Save) is what every backend MUST
// implement. ConflictWait support is provided through the optional
// WaitableStore interface — backends that don't implement WaitableStore
// behave exactly as ConflictReject (no polling, no busy wait).
type Store interface {
	// Lock attempts to claim the key for the calling goroutine. Returns
	// (true, nil) on success; (false, nil) when another goroutine is
	// already holding the key. Errors short-circuit the request to 500.
	Lock(key string) (acquired bool, err error)

	// Unlock releases a previously held key. Always paired with a
	// successful Lock; safe to call after Save.
	Unlock(key string) error

	// Get returns the persisted response for key, or (nil, nil) when no
	// entry exists or the entry has expired. Errors short-circuit the
	// request to 500.
	Get(key string) (*Response, error)

	// Save persists resp under key with the given TTL. Implementations
	// must allow Save without a prior Lock (e.g. the request handler
	// completes after Lock has already been Unlocked due to the request
	// path being structured as Lock → run → Save → Unlock).
	Save(key string, resp *Response, ttl time.Duration) error
}

// WaitableStore is the optional extension that ConflictWait requires.
// Stores that implement it can block a second request until the holding
// goroutine completes its Save (or until ctx fires). Stores that do NOT
// implement it cause the middleware to fall back to ConflictReject —
// returning 409 immediately on contention, no polling.
type WaitableStore interface {
	Store

	// Wait blocks until the response for key is available or ctx fires.
	// Returns (resp, nil) when the holding goroutine called Save;
	// (nil, ctx.Err()) on context cancellation; (nil, err) on backend
	// failure.
	Wait(ctx context.Context, key string) (*Response, error)
}

// ErrAlreadySaved is returned by Save when an entry for key already
// exists. The middleware does not currently treat this specially — it
// is exposed for backends to use if they want stricter semantics.
var ErrAlreadySaved = errors.New("idempotency: entry already exists")

// MemoryStore is the default in-process Store. It is also a WaitableStore.
//
// Lifecycle: NewMemoryStore returns a store with lazy TTL eviction (no
// goroutine — entries are removed in-line during Get when expired).
// NewMemoryStoreWithJanitor additionally runs a periodic sweep goroutine
// and returns a stop function for clean shutdown.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]*memEntry
	stopCh  chan struct{}
	doneCh  chan struct{}
}

type memEntry struct {
	resp     *Response
	expiry   time.Time
	holder   bool       // true while a request goroutine holds the key
	cond     *sync.Cond // signaled when resp transitions from nil to non-nil
	released bool       // true once Save has populated resp
}

// NewMemoryStore returns a MemoryStore with lazy TTL eviction.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]*memEntry)}
}

// NewMemoryStoreWithJanitor returns a MemoryStore plus a stop function
// that gracefully terminates the periodic sweep goroutine. Panics on
// sweep <= 0 (time.NewTicker would otherwise panic at runtime, which
// would surface as an unhelpful indirect crash). Callers wire stop into
// app.OnShutdown:
//
//	store, stop := idempotency.NewMemoryStoreWithJanitor(time.Minute)
//	app.OnShutdown(func(ctx interface{ Done() <-chan struct{} }) error {
//	    return stop()
//	})
//
// The returned stop function is safe to call from multiple goroutines
// and safe to call more than once (sync.Once gates the close-and-join).
func NewMemoryStoreWithJanitor(sweep time.Duration) (*MemoryStore, func() error) {
	if sweep <= 0 {
		panic("idempotency: NewMemoryStoreWithJanitor sweep must be > 0")
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

// Lock implements Store.
func (s *MemoryStore) Lock(key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if e, ok := s.entries[key]; ok {
		// Lazy expiry: a request whose entry has expired starts fresh.
		if !e.expiry.IsZero() && e.expiry.Before(now) {
			delete(s.entries, key)
		} else {
			// Existing live entry: holder still active or response
			// already saved → contention.
			return false, nil
		}
	}
	e := &memEntry{holder: true}
	e.cond = sync.NewCond(&s.mu)
	s.entries[key] = e
	return true, nil
}

// Unlock implements Store. If Save was called the entry remains and the
// cond is broadcast so any waiters can pick up the response. If Save was
// not called (handler errored before saving), the entry is dropped so
// subsequent requests can try again.
func (s *MemoryStore) Unlock(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return nil
	}
	e.holder = false
	if e.resp == nil {
		// Handler did not Save. Drop the shell so a retry isn't blocked
		// by a stale empty entry.
		delete(s.entries, key)
		// Wake any waiters; they'll observe the missing entry and reject.
		if e.cond != nil {
			e.cond.Broadcast()
		}
		return nil
	}
	// Save already populated resp; wake waiters.
	e.released = true
	if e.cond != nil {
		e.cond.Broadcast()
	}
	return nil
}

// Get implements Store.
func (s *MemoryStore) Get(key string) (*Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return nil, nil
	}
	if !e.expiry.IsZero() && e.expiry.Before(time.Now()) {
		delete(s.entries, key)
		return nil, nil
	}
	if e.resp == nil {
		return nil, nil
	}
	return e.resp, nil
}

// Save implements Store.
func (s *MemoryStore) Save(key string, resp *Response, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		// Holder unlocked already (handler error) or shell evicted by
		// the janitor; recreate without holder.
		e = &memEntry{cond: sync.NewCond(&s.mu)}
		s.entries[key] = e
	}
	e.resp = resp
	if ttl > 0 {
		e.expiry = time.Now().Add(ttl)
	}
	// Don't broadcast here — Unlock is the contract point that signals
	// "request finished" to waiters. Broadcasting from Save would let
	// waiters race ahead before the handler's deferred cleanup runs.
	return nil
}

// Wait implements WaitableStore. Blocks until the holder calls Unlock
// (which broadcasts), the context fires, or the entry's TTL expires.
func (s *MemoryStore) Wait(ctx context.Context, key string) (*Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		// Caller saw contention earlier but the holder has already
		// finished and the entry was evicted. Return nil so the caller
		// re-runs the request from scratch.
		return nil, nil
	}
	// e.cond is guaranteed non-nil — Lock and Save both initialize it
	// when creating an entry, and there is no other entry-creation
	// path.

	// Honor ctx cancellation. cond.Wait can't be channel-selected, so
	// fan out a goroutine that wakes the waiter on ctx.Done and
	// arbitrates via released / cancelled flags.
	cancelled := false
	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			s.mu.Lock()
			cancelled = true
			e.cond.Broadcast()
			s.mu.Unlock()
		}()
	}

	for !e.released && !cancelled {
		if !e.holder && e.resp == nil {
			// Holder unlocked without saving. Caller should retry.
			return nil, nil
		}
		e.cond.Wait()
	}
	if cancelled && !e.released {
		return nil, ctx.Err()
	}
	return e.resp, nil
}

func (s *MemoryStore) sweepExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.entries {
		if e.holder {
			continue
		}
		if !e.expiry.IsZero() && e.expiry.Before(now) {
			delete(s.entries, k)
		}
	}
}

// Compile-time guarantee that MemoryStore implements both interfaces.
var (
	_ Store         = (*MemoryStore)(nil)
	_ WaitableStore = (*MemoryStore)(nil)
)
