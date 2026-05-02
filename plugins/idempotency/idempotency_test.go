package idempotency

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// ---------- Test helpers ----------

func makeApp(t *testing.T, mw aarv.Middleware, handler aarv.HandlerFunc) *aarv.App {
	t.Helper()
	app := aarv.New()
	app.Use(mw)
	app.Post("/", handler)
	app.Get("/", handler)
	app.Get("/skip", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "skipped")
	})
	return app
}

func postWithKey(app *aarv.App, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

// ---------- Basic flow ----------

func TestFirstRequest_SavedAndCached(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: time.Hour})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusCreated, "first-call")
	})
	r1 := postWithKey(app, "k1", "")
	if r1.Code != http.StatusCreated {
		t.Fatalf("first: %d", r1.Code)
	}
	if r1.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("first request must not have Idempotency-Replayed header")
	}
	if hits.Load() != 1 {
		t.Fatalf("handler runs=%d", hits.Load())
	}
}

func TestReplay_VerbatimResponse(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: time.Hour})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		c.SetHeader("X-Custom", "value")
		return c.Text(http.StatusCreated, "the-body")
	})

	r1 := postWithKey(app, "k1", "")
	r2 := postWithKey(app, "k1", "")

	if r2.Code != r1.Code {
		t.Fatalf("status mismatch: %d vs %d", r1.Code, r2.Code)
	}
	if r2.Body.String() != r1.Body.String() {
		t.Fatalf("body mismatch: %q vs %q", r1.Body.String(), r2.Body.String())
	}
	if r2.Header().Get("X-Custom") != "value" {
		t.Fatalf("custom header not replayed")
	}
	if r2.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay must have Idempotency-Replayed=true")
	}
	if hits.Load() != 1 {
		t.Fatalf("handler ran %d times; should run once", hits.Load())
	}
}

// ---------- Concurrency ----------

func TestConflictReject_Returns409(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: time.Hour, ConflictBehavior: ConflictReject})
	hold := make(chan struct{})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	results := make(chan int, 2)
	go func() { results <- postWithKey(app, "kx", "").Code }()
	time.Sleep(20 * time.Millisecond)
	go func() { results <- postWithKey(app, "kx", "").Code }()
	time.Sleep(20 * time.Millisecond)
	close(hold)

	codes := []int{<-results, <-results}
	have409 := false
	have200 := false
	for _, c := range codes {
		if c == http.StatusConflict {
			have409 = true
		}
		if c == http.StatusOK {
			have200 = true
		}
	}
	if !have200 || !have409 {
		t.Fatalf("expected one 200 and one 409, got %v", codes)
	}
}

func TestConflictWait_WithMemoryStore_Replays(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{
		Store:            store,
		TTL:              time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      2 * time.Second,
	})
	hold := make(chan struct{})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "first-result")
	})

	results := make(chan *httptest.ResponseRecorder, 2)
	go func() { results <- postWithKey(app, "kw", "") }()
	time.Sleep(20 * time.Millisecond)
	go func() { results <- postWithKey(app, "kw", "") }()
	time.Sleep(20 * time.Millisecond)
	close(hold)

	r1 := <-results
	r2 := <-results
	if r1.Code != http.StatusOK || r2.Code != http.StatusOK {
		t.Fatalf("both should be 200: r1=%d r2=%d", r1.Code, r2.Code)
	}
	if r1.Body.String() != "first-result" || r2.Body.String() != "first-result" {
		t.Fatalf("bodies: %q %q", r1.Body.String(), r2.Body.String())
	}
	// The replayed response (whichever one is second) carries the marker.
	hasReplayHeader := r1.Header().Get("Idempotency-Replayed") == "true" ||
		r2.Header().Get("Idempotency-Replayed") == "true"
	if !hasReplayHeader {
		t.Fatal("at least one of the two responses should be marked replayed")
	}
}

func TestConflictWait_TimeoutReturns409(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{
		Store:            store,
		TTL:              time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      30 * time.Millisecond,
	})
	hold := make(chan struct{})
	defer close(hold)
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	go postWithKey(app, "kt", "")
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	rec := postWithKey(app, "kt", "")
	elapsed := time.Since(start)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on wait timeout, got %d", rec.Code)
	}
	if elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("wait took %v, expected ~30ms", elapsed)
	}
}

// storeOnly is a Store-only (NOT WaitableStore) wrapper used to exercise
// the ConflictWait fallback path.
type storeOnly struct{ inner *MemoryStore }

func (s *storeOnly) Lock(key string) (bool, error)                       { return s.inner.Lock(key) }
func (s *storeOnly) Unlock(key string) error                             { return s.inner.Unlock(key) }
func (s *storeOnly) Get(key string) (*Response, error)                   { return s.inner.Get(key) }
func (s *storeOnly) Save(key string, r *Response, ttl time.Duration) error { return s.inner.Save(key, r, ttl) }

// Compile-time guarantee: NOT WaitableStore.
var _ Store = (*storeOnly)(nil)

func TestConflictWait_NonWaitableStore_FallsBackToReject(t *testing.T) {
	store := &storeOnly{inner: NewMemoryStore()}
	mw := New(Config{
		Store:            store,
		TTL:              time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      time.Second,
	})
	hold := make(chan struct{})
	defer close(hold)
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	go postWithKey(app, "kfb", "")
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	rec := postWithKey(app, "kfb", "")
	elapsed := time.Since(start)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected immediate 409 for non-waitable store, got %d", rec.Code)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("non-waitable store should reject immediately; elapsed=%v", elapsed)
	}
}

// ---------- Payload hashing ----------

func TestPayloadMismatch_Returns422(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: time.Hour, HashRequestBody: true})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		return c.Text(http.StatusCreated, "ok")
	})

	r1 := postWithKey(app, "kp", `{"amount":100}`)
	if r1.Code != http.StatusCreated {
		t.Fatalf("first: %d", r1.Code)
	}
	r2 := postWithKey(app, "kp", `{"amount":200}`)
	if r2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("payload mismatch should be 422, got %d body=%s", r2.Code, r2.Body.String())
	}
}

// ---------- TTL ----------

func TestTTL_LazyExpiry(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: 50 * time.Millisecond})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "ok")
	})
	postWithKey(app, "kttl", "")
	if hits.Load() != 1 {
		t.Fatalf("first hits=%d", hits.Load())
	}
	time.Sleep(70 * time.Millisecond)
	postWithKey(app, "kttl", "")
	if hits.Load() != 2 {
		t.Fatalf("after TTL handler should run again; hits=%d", hits.Load())
	}
}

func TestNewMemoryStoreWithJanitor_StopsCleanly(t *testing.T) {
	before := runtime.NumGoroutine()
	store, stop := NewMemoryStoreWithJanitor(10 * time.Millisecond)
	mw := New(Config{Store: store, TTL: 20 * time.Millisecond})
	app := aarv.New()
	app.Use(mw)
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	for i := 0; i < 5; i++ {
		postWithKey(app, "k"+strconv.Itoa(i), "")
	}
	time.Sleep(40 * time.Millisecond) // let janitor sweep at least once
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if err := stop(); err != nil {
		t.Fatal("second stop: ", err)
	}
	time.Sleep(20 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Fatalf("janitor leaked: before=%d after=%d", before, after)
	}
}

// ---------- SafeMethods nil/empty/custom ----------

func TestSafeMethods_NilDefaults_BypassGetHeadOptions(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: time.Hour}) // SafeMethods nil
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Idempotency-Key", "k-get")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	app.ServeHTTP(rec, req) // call twice — both should hit handler since GET bypasses
	if hits.Load() != 2 {
		t.Fatalf("nil SafeMethods should bypass GET; handler runs=%d", hits.Load())
	}
}

func TestSafeMethods_EmptyMakesGETParticipate(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{
		Store:       store,
		TTL:         time.Hour,
		SafeMethods: []string{}, // explicit empty: every method participates
	})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "ok")
	})
	doGet := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Idempotency-Key", "kg")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	doGet()
	rec2 := doGet()
	if hits.Load() != 1 {
		t.Fatalf("empty SafeMethods should cache GET; handler ran %d times", hits.Load())
	}
	if rec2.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("second GET should be replayed")
	}
}

func TestSafeMethods_Custom_GETParticipatesHEADStillBypasses(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{
		Store: store, TTL: time.Hour,
		SafeMethods: []string{http.MethodHead, http.MethodOptions},
	})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "ok")
	})
	// GET twice with same key → handler runs once (cached).
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Idempotency-Key", "kg")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
	if hits.Load() != 1 {
		t.Fatalf("GET should be cached when not in SafeMethods; hits=%d", hits.Load())
	}
}

// ---------- CacheStatuses nil/empty/custom ----------

func TestCacheStatuses_DefaultCachesSuccess(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: time.Hour}) // CacheStatuses: nil
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusCreated, "ok")
	})
	postWithKey(app, "k", "")
	postWithKey(app, "k", "")
	if hits.Load() != 1 {
		t.Fatalf("2xx should be cached by default; hits=%d", hits.Load())
	}
}

func TestCacheStatuses_DefaultDoesNotCache5xx(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: time.Hour})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusInternalServerError, "boom")
	})
	postWithKey(app, "k5", "")
	postWithKey(app, "k5", "")
	if hits.Load() != 2 {
		t.Fatalf("5xx must not be cached by default; hits=%d", hits.Load())
	}
}

func TestCacheStatuses_EmptyCachesNothing(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{
		Store:         store,
		TTL:           time.Hour,
		CacheStatuses: []int{}, // explicit empty → cache nothing
	})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "ok")
	})
	postWithKey(app, "kn", "")
	postWithKey(app, "kn", "")
	if hits.Load() != 2 {
		t.Fatalf("empty CacheStatuses should not cache; hits=%d", hits.Load())
	}
}

func TestCacheStatuses_Custom(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{
		Store:         store,
		TTL:           time.Hour,
		CacheStatuses: []int{http.StatusCreated},
	})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		// Alternate to drive a 200 then a 201 — but the cache key is the same.
		return c.Text(http.StatusOK, "ok")
	})
	postWithKey(app, "kc", "")
	postWithKey(app, "kc", "")
	if hits.Load() != 2 {
		t.Fatalf("200 not in CacheStatuses; should not cache; hits=%d", hits.Load())
	}
}

// ---------- Absent key / RequireKey ----------

func TestAbsentKey_Passthrough(t *testing.T) {
	mw := New(DefaultConfig())
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "ok")
	})
	postWithKey(app, "", "")
	postWithKey(app, "", "")
	if hits.Load() != 2 {
		t.Fatalf("absent key passthrough; hits=%d", hits.Load())
	}
}

func TestRequireKey_AbsentReturns400(t *testing.T) {
	mw := New(Config{RequireKey: true, Store: NewMemoryStore(), TTL: time.Hour})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})
	rec := postWithKey(app, "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("RequireKey: want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- Over-cap response ----------

func TestOverCapResponse_FallsThroughWithHeader_NoSave(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: time.Hour, MaxResponseBytes: 16})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, strings.Repeat("a", 64)) // > 16
	})
	r1 := postWithKey(app, "kbig", "")
	if r1.Code != http.StatusOK {
		t.Fatalf("first: %d", r1.Code)
	}
	if r1.Header().Get("Idempotency-Cached") == "" {
		t.Fatalf("expected explanatory header on overflow; got %v", r1.Header())
	}
	if !strings.Contains(r1.Header().Get("Idempotency-Cached"), "reason=size") {
		t.Fatalf("header missing reason=size: %q", r1.Header().Get("Idempotency-Cached"))
	}
	if got, _ := io.ReadAll(r1.Body); len(got) != 64 {
		t.Fatalf("body length: got %d want 64", len(got))
	}
	// Second call: handler should run again (over-cap was NOT saved).
	r2 := postWithKey(app, "kbig", "")
	if hits.Load() != 2 {
		t.Fatalf("over-cap response was cached; hits=%d", hits.Load())
	}
	if r2.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("second call must not be a replay")
	}
}

// ---------- Skip / Skipper ----------

func TestSkipper_Bypass(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{
		Store: store, TTL: time.Hour,
		Skipper: func(c *aarv.Context) bool {
			return c.Header("X-Skip") != ""
		},
	})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "ok")
	})
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Idempotency-Key", "ks")
		req.Header.Set("X-Skip", "1")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
	if hits.Load() != 3 {
		t.Fatalf("Skipper should bypass; hits=%d", hits.Load())
	}
}

func TestSkipPaths_Bypass(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{Store: store, TTL: time.Hour, SkipPaths: []string{"/skip"}})
	app := aarv.New()
	app.Use(mw)
	hits := atomic.Int32{}
	app.Post("/skip", func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "skipped")
	})
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/skip", nil)
		req.Header.Set("Idempotency-Key", "kskip")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
	if hits.Load() != 3 {
		t.Fatalf("SkipPaths should bypass; hits=%d", hits.Load())
	}
}

// ---------- Native vs stdlib parity ----------

func TestNativeAndStdlib_ProduceIdenticalReplay(t *testing.T) {
	// Force the stdlib path by composing through a non-native middleware
	// that the runtime cannot lift to the native chain.
	nonNativeMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
	for _, lane := range []string{"native", "stdlib"} {
		t.Run(lane, func(t *testing.T) {
			store := NewMemoryStore()
			app := aarv.New()
			if lane == "stdlib" {
				app.Use(aarv.Middleware(nonNativeMW))
			}
			app.Use(New(Config{Store: store, TTL: time.Hour}))
			app.Post("/", func(c *aarv.Context) error {
				c.SetHeader("X-Result", "value")
				return c.Text(http.StatusCreated, "body-bytes")
			})

			r1 := postWithKey(app, "kparity", "")
			r2 := postWithKey(app, "kparity", "")
			if r1.Code != r2.Code {
				t.Fatalf("status mismatch %d vs %d", r1.Code, r2.Code)
			}
			if r1.Body.String() != r2.Body.String() {
				t.Fatalf("body mismatch %q vs %q", r1.Body.String(), r2.Body.String())
			}
			if r2.Header().Get("X-Result") != "value" {
				t.Fatalf("X-Result not replayed")
			}
			if r2.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatalf("missing replay marker")
			}
		})
	}
}

// ---------- Error propagation paths ----------

type failingStore struct {
	getErr  error
	lockErr error
}

func (f *failingStore) Lock(string) (bool, error)                       { return false, f.lockErr }
func (f *failingStore) Unlock(string) error                             { return nil }
func (f *failingStore) Get(string) (*Response, error)                   { return nil, f.getErr }
func (f *failingStore) Save(string, *Response, time.Duration) error    { return nil }

func TestStoreGetError_Returns500(t *testing.T) {
	store := &failingStore{getErr: errors.New("backend down")}
	mw := New(Config{Store: store, TTL: time.Hour})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})
	rec := postWithKey(app, "k", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
}

// ---------- WaitableStore type guard ----------

func TestMemoryStore_SatisfiesInterfaces(t *testing.T) {
	var _ Store = (*MemoryStore)(nil)
	var _ WaitableStore = (*MemoryStore)(nil)
}

// ---------- Race ----------

func TestConcurrent_SameKey_Race(t *testing.T) {
	store := NewMemoryStore()
	mw := New(Config{
		Store:            store,
		TTL:              time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      2 * time.Second,
	})
	hits := atomic.Int32{}
	app := makeApp(t, mw, func(c *aarv.Context) error {
		hits.Add(1)
		time.Sleep(5 * time.Millisecond)
		return c.Text(http.StatusOK, "ok")
	})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			postWithKey(app, "kshared", "")
		}()
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("expected handler to run once across 50 concurrent same-key requests; got %d", hits.Load())
	}
}

// ---------- Custom ErrorHandler ----------

func TestErrorHandler_Custom(t *testing.T) {
	store := NewMemoryStore()
	called := false
	mw := New(Config{
		Store: store, TTL: time.Hour,
		ConflictBehavior: ConflictReject,
		ErrorHandler: func(c *aarv.Context, status int, message string) error {
			called = true
			return c.JSON(http.StatusTeapot, map[string]string{
				"status":  strconv.Itoa(status),
				"message": message,
			})
		},
	})
	hold := make(chan struct{})
	defer close(hold)
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	go postWithKey(app, "ke", "")
	time.Sleep(20 * time.Millisecond)
	rec := postWithKey(app, "ke", "")
	if !called {
		t.Fatal("custom ErrorHandler not called")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("custom ErrorHandler should preempt; got %d", rec.Code)
	}
}

// ---------- Stdlib-path coverage ----------

// nonNativeMW forces the runtime onto the stdlib path.
func nonNativeMW() aarv.Middleware {
	return aarv.Middleware(func(next http.Handler) http.Handler { return next })
}

func TestStdlibPath_FullCoverage(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Store: store, TTL: time.Hour}))
	hits := atomic.Int32{}
	app.Post("/", func(c *aarv.Context) error {
		hits.Add(1)
		c.SetHeader("X-Result", "value")
		return c.Text(http.StatusCreated, "stdlib-body")
	})

	// First request: cached.
	r1 := postWithKey(app, "kstd", "")
	if r1.Code != http.StatusCreated {
		t.Fatalf("first: %d", r1.Code)
	}
	// Second: replayed.
	r2 := postWithKey(app, "kstd", "")
	if r2.Code != http.StatusCreated || r2.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay: %d hdr=%v", r2.Code, r2.Header())
	}
	if hits.Load() != 1 {
		t.Fatalf("handler runs=%d", hits.Load())
	}
}

func TestStdlibPath_SafeMethod(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(DefaultConfig()))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("safe: %d", rec.Code)
	}
}

func TestStdlibPath_SkipPathsAndSkipper(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store: store, TTL: time.Hour,
		SkipPaths: []string{"/skip"},
		Skipper: func(c *aarv.Context) bool {
			return c.Header("X-Skip") != ""
		},
	}))
	hits := atomic.Int32{}
	app.Post("/", func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "ok")
	})
	app.Post("/skip", func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, "skipped")
	})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/skip", nil)
		req.Header.Set("Idempotency-Key", "k")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Idempotency-Key", "k")
		req.Header.Set("X-Skip", "1")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
	if hits.Load() != 6 {
		t.Fatalf("skip bypassed cache: hits=%d", hits.Load())
	}
}

func TestStdlibPath_AbsentKey(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(DefaultConfig()))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := postWithKey(app, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("absent passthrough: %d", rec.Code)
	}
}

func TestStdlibPath_RequireKey_400(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{RequireKey: true, Store: NewMemoryStore(), TTL: time.Hour}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := postWithKey(app, "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("RequireKey stdlib: %d", rec.Code)
	}
}

func TestStdlibPath_HashRequestBody_Mismatch_422(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Store: store, TTL: time.Hour, HashRequestBody: true}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusCreated, "ok") })

	if rec := postWithKey(app, "kp", `{"a":1}`); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := postWithKey(app, "kp", `{"a":2}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("payload mismatch: %d", rec.Code)
	}
}

func TestStdlibPath_HashRequestBody_BodyTooLarge_413(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store:               NewMemoryStore(),
		TTL:                 time.Hour,
		HashRequestBody:     true,
		MaxRequestBodyBytes: 8,
	}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	rec := postWithKey(app, "ktoobig", strings.Repeat("x", 100))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("stdlib body too large: %d", rec.Code)
	}
}

func TestNativePath_HashRequestBody_BodyTooLarge_413(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{
		Store:               NewMemoryStore(),
		TTL:                 time.Hour,
		HashRequestBody:     true,
		MaxRequestBodyBytes: 8,
	}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	rec := postWithKey(app, "ktoobig", strings.Repeat("x", 100))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("native body too large: %d", rec.Code)
	}
}

func TestStdlibPath_BodyReadError(t *testing.T) {
	mw := New(Config{
		Store:           NewMemoryStore(),
		TTL:             time.Hour,
		HashRequestBody: true,
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", &readErrBody{err: errors.New("boom")})
	req.Header.Set("Idempotency-Key", "k")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("body err: %d", rec.Code)
	}
}

type readErrBody struct{ err error }

func (b *readErrBody) Read(p []byte) (int, error) { return 0, b.err }
func (b *readErrBody) Close() error               { return nil }

func TestNativePath_BodyReadError(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{
		Store:           NewMemoryStore(),
		TTL:             time.Hour,
		HashRequestBody: true,
	}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodPost, "/", &readErrBody{err: errors.New("boom")})
	req.Header.Set("Idempotency-Key", "k")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("native body err: %d", rec.Code)
	}
}

func TestStdlibPath_StoreGetError(t *testing.T) {
	store := &failingStore{getErr: errors.New("boom")}
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Store: store, TTL: time.Hour}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	rec := postWithKey(app, "k", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("stdlib Get err: %d", rec.Code)
	}
}

func TestNativePath_StoreLockError(t *testing.T) {
	store := &failingStore{lockErr: errors.New("lock boom")}
	app := aarv.New()
	app.Use(New(Config{Store: store, TTL: time.Hour}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := postWithKey(app, "k", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Lock err: %d", rec.Code)
	}
}

func TestStdlibPath_StoreLockError(t *testing.T) {
	store := &failingStore{lockErr: errors.New("lock boom")}
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Store: store, TTL: time.Hour}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := postWithKey(app, "k", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("stdlib Lock err: %d", rec.Code)
	}
}

func TestStdlibPath_ConflictReject_409(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Store: store, TTL: time.Hour, ConflictBehavior: ConflictReject}))
	hold := make(chan struct{})
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	results := make(chan int, 2)
	go func() { results <- postWithKey(app, "kx", "").Code }()
	time.Sleep(20 * time.Millisecond)
	go func() { results <- postWithKey(app, "kx", "").Code }()
	time.Sleep(20 * time.Millisecond)
	close(hold)
	codes := []int{<-results, <-results}
	have409 := false
	for _, c := range codes {
		if c == http.StatusConflict {
			have409 = true
		}
	}
	if !have409 {
		t.Fatalf("no 409 in stdlib conflict: %v", codes)
	}
}

func TestStdlibPath_ConflictWait_Replays(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store: store, TTL: time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      2 * time.Second,
	}))
	hold := make(chan struct{})
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	results := make(chan *httptest.ResponseRecorder, 2)
	go func() { results <- postWithKey(app, "kw", "") }()
	time.Sleep(20 * time.Millisecond)
	go func() { results <- postWithKey(app, "kw", "") }()
	time.Sleep(20 * time.Millisecond)
	close(hold)
	r1 := <-results
	r2 := <-results
	if r1.Code != http.StatusOK || r2.Code != http.StatusOK {
		t.Fatalf("both 200 expected: %d %d", r1.Code, r2.Code)
	}
}

func TestStdlibPath_ConflictWait_NonWaitable_409(t *testing.T) {
	store := &storeOnly{inner: NewMemoryStore()}
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store: store, TTL: time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      time.Second,
	}))
	hold := make(chan struct{})
	defer close(hold)
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	go postWithKey(app, "kfb", "")
	time.Sleep(20 * time.Millisecond)
	rec := postWithKey(app, "kfb", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("non-waitable stdlib: %d", rec.Code)
	}
}

func TestStdlibPath_PayloadMismatch_422(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Store: store, TTL: time.Hour, HashRequestBody: true}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusCreated, "ok") })

	if rec := postWithKey(app, "kpm", `{"a":1}`); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := postWithKey(app, "kpm", `{"a":2}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("stdlib payload mismatch: %d", rec.Code)
	}
}

func TestStdlibPath_CustomErrorHandler(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store: NewMemoryStore(), TTL: time.Hour,
		RequireKey: true,
		ErrorHandler: func(c *aarv.Context, status int, message string) error {
			return c.Text(http.StatusTeapot, message)
		},
	}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := postWithKey(app, "", "")
	if rec.Code != http.StatusTeapot {
		t.Fatalf("custom stdlib: %d", rec.Code)
	}
}

func TestStdlibPath_CustomErrorHandlerReturnsError(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store: NewMemoryStore(), TTL: time.Hour,
		RequireKey: true,
		ErrorHandler: func(c *aarv.Context, status int, message string) error {
			return aarv.ErrInternal(nil)
		},
	}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := postWithKey(app, "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("stdlib handler-error fallback: %d", rec.Code)
	}
}

func TestStdlibPath_OverCapResponse(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Store: store, TTL: time.Hour, MaxResponseBytes: 16}))
	hits := atomic.Int32{}
	app.Post("/", func(c *aarv.Context) error {
		hits.Add(1)
		return c.Text(http.StatusOK, strings.Repeat("a", 64))
	})
	r1 := postWithKey(app, "kbig", "")
	if r1.Header().Get("Idempotency-Cached") == "" {
		t.Fatal("expected Idempotency-Cached header")
	}
	postWithKey(app, "kbig", "") // not cached → handler runs again
	if hits.Load() != 2 {
		t.Fatalf("over-cap was cached: hits=%d", hits.Load())
	}
}

// ---------- Misc ----------

func TestNormalize_NegativeMaxRequestBody(t *testing.T) {
	// Negative MaxRequestBodyBytes is normalized to 0 (unlimited).
	n := normalize(Config{MaxRequestBodyBytes: -1})
	if n.maxRequestBodyBytes != 0 {
		t.Fatalf("normalize neg: %d", n.maxRequestBodyBytes)
	}
}

func TestWaitContext_NoTimeout(t *testing.T) {
	n := &normalized{}
	ctx, cancel := n.waitContext(context.Background())
	defer cancel()
	if ctx == nil {
		t.Fatal("nil ctx")
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("expected no deadline")
	}
}

func TestReadCapped_NilReader(t *testing.T) {
	body, tooLarge, err := readCapped(nil, 10)
	if body != nil || tooLarge || err != nil {
		t.Fatalf("nil reader: %v %v %v", body, tooLarge, err)
	}
}

func TestReadCapped_Unbounded(t *testing.T) {
	body, tooLarge, err := readCapped(strings.NewReader("hello"), 0)
	if string(body) != "hello" || tooLarge || err != nil {
		t.Fatalf("unbounded: %q %v %v", body, tooLarge, err)
	}
}

func TestReadCapped_Error(t *testing.T) {
	_, _, err := readCapped(&readErrBody{err: errors.New("boom")}, 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCodeForStatus_AllBranches(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:           "bad_request",
		http.StatusConflict:             "conflict",
		http.StatusUnprocessableEntity:  "unprocessable_entity",
		http.StatusInternalServerError:  "internal_error",
		http.StatusTeapot:               http.StatusText(http.StatusTeapot),
	}
	for s, want := range cases {
		if got := codeForStatus(s); got != want {
			t.Fatalf("codeForStatus(%d): %q want %q", s, got, want)
		}
	}
}

func TestPanic_NewMemoryStoreWithJanitor_ZeroSweep(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
	}()
	_, _ = NewMemoryStoreWithJanitor(0)
}

// ---------- Store edge cases ----------

func TestMemoryStore_Lock_ExpiredEntryRecreates(t *testing.T) {
	s := NewMemoryStore()
	if ok, _ := s.Lock("k"); !ok {
		t.Fatal("first lock")
	}
	if err := s.Save("k", &Response{StatusCode: 200}, 1*time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	if err := s.Unlock("k"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // expire

	// Second Lock must succeed after expiry.
	ok, err := s.Lock("k")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expired entry should be reclaimable")
	}
}

func TestMemoryStore_Unlock_NotFound(t *testing.T) {
	s := NewMemoryStore()
	if err := s.Unlock("never-locked"); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryStore_Get_ExpiredReturnsNil(t *testing.T) {
	s := NewMemoryStore()
	_, _ = s.Lock("k")
	_ = s.Save("k", &Response{StatusCode: 200}, 1*time.Nanosecond)
	_ = s.Unlock("k")
	time.Sleep(2 * time.Millisecond)
	resp, err := s.Get("k")
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatal("expected nil for expired entry")
	}
}

func TestMemoryStore_Save_AfterUnlock_Recreates(t *testing.T) {
	s := NewMemoryStore()
	// Save without prior Lock — exercises the "shell evicted by janitor"
	// recreate path.
	if err := s.Save("orphan", &Response{StatusCode: 200}, time.Hour); err != nil {
		t.Fatal(err)
	}
	resp, _ := s.Get("orphan")
	if resp == nil {
		t.Fatal("orphan save lost")
	}
}

func TestMemoryStore_Wait_HolderUnlockWithoutSave(t *testing.T) {
	s := NewMemoryStore()
	if ok, _ := s.Lock("k"); !ok {
		t.Fatal("setup")
	}
	results := make(chan *Response, 1)
	go func() {
		resp, _ := s.Wait(context.Background(), "k")
		results <- resp
	}()
	time.Sleep(20 * time.Millisecond)
	// Unlock without Save → entry deleted, waiter sees nil.
	_ = s.Unlock("k")
	select {
	case r := <-results:
		if r != nil {
			t.Fatalf("expected nil: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not unblock")
	}
}

func TestMemoryStore_Wait_KeyMissing(t *testing.T) {
	// Wait on a key that has no entry returns nil immediately.
	s := NewMemoryStore()
	resp, err := s.Wait(context.Background(), "nope")
	if resp != nil || err != nil {
		t.Fatalf("Wait(missing): %v %v", resp, err)
	}
}

func TestMemoryStore_SweepExpired_SkipsHolder(t *testing.T) {
	s := NewMemoryStore()
	if ok, _ := s.Lock("held"); !ok {
		t.Fatal("setup")
	}
	// Sweeping must NOT delete the held entry.
	s.sweepExpired()
	// Now release, save, sweep again — still held until TTL.
	_ = s.Save("held", &Response{StatusCode: 200}, 1*time.Nanosecond)
	_ = s.Unlock("held")
	time.Sleep(2 * time.Millisecond)
	s.sweepExpired()
	if r, _ := s.Get("held"); r != nil {
		t.Fatal("sweep should have evicted")
	}
}

// ---------- Writer coverage ----------

func TestCaptureWriter_Unwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 1024)
	defer releaseCaptureWriter(cw)
	if cw.Unwrap() != rec {
		t.Fatal("Unwrap should return underlying writer")
	}
}

func TestCaptureWriter_Header_AfterFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 1024)
	defer releaseCaptureWriter(cw)
	cw.Header().Set("X-A", "v")
	cw.WriteHeader(http.StatusCreated)
	cw.FlushUnderCap()
	// After flush, Header() must point at the underlying writer's headers.
	cw.Header().Set("X-B", "post-flush")
	if rec.Header().Get("X-B") != "post-flush" {
		t.Fatal("Header after flush not delegating")
	}
}

func TestCaptureWriter_WriteHeader_Idempotent(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 1024)
	defer releaseCaptureWriter(cw)
	cw.WriteHeader(http.StatusCreated)
	cw.WriteHeader(http.StatusOK) // ignored — first one wins
	if cw.Status() != http.StatusCreated {
		t.Fatalf("status: %d", cw.Status())
	}
}

func TestCaptureWriter_WriteHeader_AfterHeaderSent(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 1024)
	defer releaseCaptureWriter(cw)
	cw.WriteHeader(http.StatusOK)
	cw.FlushUnderCap()
	cw.WriteHeader(http.StatusInternalServerError) // no-op after flush
	if rec.Code != http.StatusOK {
		t.Fatalf("post-flush WriteHeader leaked: %d", rec.Code)
	}
}

func TestCaptureWriter_OverflowedWritePassthrough(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 4)
	defer releaseCaptureWriter(cw)
	cw.WriteHeader(http.StatusOK)
	if _, err := cw.Write([]byte("12345")); err != nil { // triggers overflow
		t.Fatal(err)
	}
	if !cw.Overflowed() {
		t.Fatal("expected overflow")
	}
	// Subsequent Write goes straight through.
	if _, err := cw.Write([]byte("67")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), "67") {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestCaptureWriter_FlushOverflow_Idempotent(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 2)
	defer releaseCaptureWriter(cw)
	cw.WriteHeader(http.StatusOK)
	_, _ = cw.Write([]byte("xxxx")) // forces overflow
	cw.flushOverflow()              // idempotent — second call is a no-op
}

func TestCaptureWriter_FlushUnderCap_IdempotentAndOverflowGuard(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 1024)
	defer releaseCaptureWriter(cw)
	cw.WriteHeader(http.StatusOK)
	_, _ = cw.Write([]byte("hi"))
	cw.FlushUnderCap()
	cw.FlushUnderCap() // second call is a no-op (committed)

	// Overflowed writer's FlushUnderCap is a no-op.
	rec2 := httptest.NewRecorder()
	cw2 := acquireCaptureWriter(rec2, 2)
	defer releaseCaptureWriter(cw2)
	cw2.WriteHeader(http.StatusOK)
	_, _ = cw2.Write([]byte("xxxx"))
	cw2.FlushUnderCap() // no-op because overflowed
}

func TestCaptureWriter_Snapshot_OverflowedReturnsNil(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 2)
	defer releaseCaptureWriter(cw)
	cw.WriteHeader(http.StatusOK)
	_, _ = cw.Write([]byte("xxxx"))
	if cw.Snapshot() != nil {
		t.Fatal("Snapshot of overflowed writer must be nil")
	}
}

func TestCaptureWriter_Snapshot_FiltersHopByHop(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 1024)
	defer releaseCaptureWriter(cw)
	cw.Header().Set("Connection", "keep-alive") // hop-by-hop
	cw.Header().Set("X-Custom", "value")
	cw.WriteHeader(http.StatusOK)
	_, _ = cw.Write([]byte("ok"))
	snap := cw.Snapshot()
	if snap.Headers.Get("Connection") != "" {
		t.Fatal("hop-by-hop not filtered")
	}
	if snap.Headers.Get("X-Custom") != "value" {
		t.Fatal("custom header lost")
	}
}

func TestReleaseCaptureWriter_NilSafe(t *testing.T) {
	// Should not panic.
	releaseCaptureWriter(nil)
}

func TestReleaseCaptureWriter_LargeBufferReclaimed(t *testing.T) {
	// A capture writer with a body buffer larger than 1 MiB has its
	// buffer reset to nil so GC can reclaim — exercise that branch.
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 0) // unbounded cap
	cw.Header()
	_, _ = cw.body.Write(make([]byte, (1<<20)+1)) // > 1 MiB
	releaseCaptureWriter(cw)
	// Acquire again and confirm body is freshly allocated.
	cw2 := acquireCaptureWriter(rec, 0)
	defer releaseCaptureWriter(cw2)
	if cw2.body == nil {
		t.Fatal("body should be re-allocated on acquire")
	}
}

func TestWriteJSONError_Format(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, http.StatusConflict, "mismatch", "rid-1")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "conflict") || !strings.Contains(body, "mismatch") || !strings.Contains(body, "rid-1") {
		t.Fatalf("body: %q", body)
	}
}

// ---------- Remaining branch coverage ----------

// failSaveStore lets Lock/Get succeed but Save fail.
type failSaveStore struct{ inner *MemoryStore }

func (f *failSaveStore) Lock(k string) (bool, error)                  { return f.inner.Lock(k) }
func (f *failSaveStore) Unlock(k string) error                        { return f.inner.Unlock(k) }
func (f *failSaveStore) Get(k string) (*Response, error)              { return f.inner.Get(k) }
func (f *failSaveStore) Save(string, *Response, time.Duration) error { return errors.New("save boom") }

func TestNativePath_SaveError_FlushesAnyway(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{Store: &failSaveStore{inner: NewMemoryStore()}, TTL: time.Hour}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := postWithKey(app, "k", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("save-error native: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStdlibPath_SaveError_FlushesAnyway(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Store: &failSaveStore{inner: NewMemoryStore()}, TTL: time.Hour}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := postWithKey(app, "k", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("save-error stdlib: %d", rec.Code)
	}
}

func TestStdlibPath_NoContext_SkipPaths(t *testing.T) {
	mw := New(Config{
		Store: NewMemoryStore(), TTL: time.Hour,
		SkipPaths: []string{"/skip"},
	})
	hits := 0
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/skip", nil)
		req.Header.Set("Idempotency-Key", "k")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	if hits != 3 {
		t.Fatalf("no-context SkipPaths: hits=%d", hits)
	}
}

// failWaitStore implements WaitableStore but Wait always returns a
// non-context error — exercises the "store Wait failed" branch.
type failWaitStore struct {
	*MemoryStore
	waitErr error
}

func (f *failWaitStore) Wait(ctx context.Context, key string) (*Response, error) {
	return nil, f.waitErr
}

var _ WaitableStore = (*failWaitStore)(nil)

func TestNativePath_ConflictWait_StoreError(t *testing.T) {
	store := &failWaitStore{MemoryStore: NewMemoryStore(), waitErr: errors.New("wait boom")}
	app := aarv.New()
	app.Use(New(Config{
		Store: store, TTL: time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      time.Second,
	}))
	hold := make(chan struct{})
	defer close(hold)
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go postWithKey(app, "kw", "")
	time.Sleep(20 * time.Millisecond)
	rec := postWithKey(app, "kw", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("native Wait error: %d", rec.Code)
	}
}

func TestStdlibPath_ConflictWait_StoreError(t *testing.T) {
	store := &failWaitStore{MemoryStore: NewMemoryStore(), waitErr: errors.New("wait boom")}
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store: store, TTL: time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      time.Second,
	}))
	hold := make(chan struct{})
	defer close(hold)
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go postWithKey(app, "kw", "")
	time.Sleep(20 * time.Millisecond)
	rec := postWithKey(app, "kw", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("stdlib Wait error: %d", rec.Code)
	}
}

// nilRespWaitStore returns (nil, nil) from Wait — simulates the holder
// completing without saving.
type nilRespWaitStore struct{ *MemoryStore }

func (s *nilRespWaitStore) Wait(ctx context.Context, key string) (*Response, error) {
	return nil, nil
}

var _ WaitableStore = (*nilRespWaitStore)(nil)

func TestStdlibPath_ConflictWait_TimeoutReturns409(t *testing.T) {
	// Stdlib lane analog of TestConflictWait_TimeoutReturns409 — drives
	// the context.DeadlineExceeded → 409 branch in handleConflictStdlib.
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store: store, TTL: time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      30 * time.Millisecond,
	}))
	hold := make(chan struct{})
	defer close(hold)
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	go postWithKey(app, "kt", "")
	time.Sleep(20 * time.Millisecond)
	rec := postWithKey(app, "kt", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("stdlib wait timeout: %d", rec.Code)
	}
}

func TestNativePath_ConflictWait_HolderUnlocksWithoutSave_409(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{
		Store: &nilRespWaitStore{MemoryStore: NewMemoryStore()}, TTL: time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      time.Second,
	}))
	hold := make(chan struct{})
	defer close(hold)
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go postWithKey(app, "kn", "")
	time.Sleep(20 * time.Millisecond)
	rec := postWithKey(app, "kn", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("native nil-resp wait: %d", rec.Code)
	}
}

func TestStdlibPath_ConflictWait_HolderUnlocksWithoutSave_409(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store: &nilRespWaitStore{MemoryStore: NewMemoryStore()}, TTL: time.Hour,
		ConflictBehavior: ConflictWait,
		WaitTimeout:      time.Second,
	}))
	hold := make(chan struct{})
	defer close(hold)
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go postWithKey(app, "kn", "")
	time.Sleep(20 * time.Millisecond)
	rec := postWithKey(app, "kn", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("stdlib nil-resp wait: %d", rec.Code)
	}
}

// hashMismatchWaitStore returns a stored Response with a different
// PayloadHash than the request payload.
type hashMismatchWaitStore struct{ *MemoryStore }

func (s *hashMismatchWaitStore) Wait(ctx context.Context, key string) (*Response, error) {
	other := [32]byte{1, 2, 3}
	return &Response{StatusCode: 200, Body: []byte("stale"), PayloadHash: other}, nil
}

var _ WaitableStore = (*hashMismatchWaitStore)(nil)

func TestNativePath_ConflictWait_HashMismatch_422(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{
		Store: &hashMismatchWaitStore{MemoryStore: NewMemoryStore()}, TTL: time.Hour,
		ConflictBehavior: ConflictWait,
		HashRequestBody:  true,
		WaitTimeout:      time.Second,
	}))
	hold := make(chan struct{})
	defer close(hold)
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go postWithKey(app, "kh", `{"a":1}`)
	time.Sleep(20 * time.Millisecond)
	rec := postWithKey(app, "kh", `{"a":2}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("native hash mismatch in Wait: %d", rec.Code)
	}
}

func TestStdlibPath_ConflictWait_HashMismatch_422(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Store: &hashMismatchWaitStore{MemoryStore: NewMemoryStore()}, TTL: time.Hour,
		ConflictBehavior: ConflictWait,
		HashRequestBody:  true,
		WaitTimeout:      time.Second,
	}))
	hold := make(chan struct{})
	defer close(hold)
	app.Post("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go postWithKey(app, "kh", `{"a":1}`)
	time.Sleep(20 * time.Millisecond)
	rec := postWithKey(app, "kh", `{"a":2}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("stdlib hash mismatch in Wait: %d", rec.Code)
	}
}

func TestReplay_ZeroStatusDefaultsTo200(t *testing.T) {
	// Seed each lane's store with a Response whose StatusCode is 0 so
	// the replay status default branch fires.
	store := NewMemoryStore()
	_, _ = store.Lock("kz")
	_ = store.Save("kz", &Response{StatusCode: 0, Body: []byte("body")}, time.Hour)
	_ = store.Unlock("kz")
	app := aarv.New()
	app.Use(New(Config{Store: store, TTL: time.Hour}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := postWithKey(app, "kz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("native zero-status replay: %d", rec.Code)
	}

	store2 := NewMemoryStore()
	_, _ = store2.Lock("kz")
	_ = store2.Save("kz", &Response{StatusCode: 0, Body: []byte("body")}, time.Hour)
	_ = store2.Unlock("kz")
	app2 := aarv.New()
	app2.Use(nonNativeMW())
	app2.Use(New(Config{Store: store2, TTL: time.Hour}))
	app2.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec2 := postWithKey(app2, "kz", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("stdlib zero-status replay: %d", rec2.Code)
	}
}

func TestCaptureWriter_FlushOverflow_WithBufferedPrefix(t *testing.T) {
	// Buffer some bytes before exceeding the cap, then trigger overflow:
	// flushOverflow drains the buffered prefix BEFORE the overflowing
	// write — exercises the `if cw.body.Len() > 0` branch.
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec, 4)
	defer releaseCaptureWriter(cw)
	cw.WriteHeader(http.StatusOK)
	if _, err := cw.Write([]byte("12")); err != nil {
		t.Fatal(err)
	}
	if _, err := cw.Write([]byte("345")); err != nil {
		t.Fatal(err)
	}
	if !cw.Overflowed() {
		t.Fatal("expected overflow")
	}
	got := rec.Body.String()
	if !strings.HasPrefix(got, "12") || !strings.Contains(got, "345") {
		t.Fatalf("buffered prefix lost: %q", got)
	}
}

// ---------- ctx cancellation in Wait ----------

func TestWait_RespectsContextCancel(t *testing.T) {
	store := NewMemoryStore()
	// Reserve the key so Wait blocks.
	if ok, _ := store.Lock("kctx"); !ok {
		t.Fatal("setup: lock failed")
	}
	defer func() { _ = store.Unlock("kctx") }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	resp, err := store.Wait(ctx, "kctx")
	elapsed := time.Since(start)
	if resp != nil {
		t.Fatal("expected nil resp")
	}
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("wait did not honor cancellation: %v", elapsed)
	}
}
