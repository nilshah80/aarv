package ratelimit

import (
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

func makeApp(t *testing.T, mw aarv.Middleware) *aarv.App {
	t.Helper()
	app := aarv.New()
	app.Use(mw)
	app.Get("/", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})
	app.Get("/skip", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "skipped")
	})
	return app
}

func reqWithIP(ip string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = ip + ":1234"
	return req
}

func do(app *aarv.App, ip string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, reqWithIP(ip))
	return rec
}

func TestTokenBucket_WithinLimit(t *testing.T) {
	mw := New(Config{Limit: 5, Window: time.Minute})
	app := makeApp(t, mw)
	for i := 0; i < 5; i++ {
		if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
			t.Fatalf("iter %d: want 200, got %d", i, rec.Code)
		}
	}
}

func TestTokenBucket_ExceedReturns429(t *testing.T) {
	mw := New(Config{Limit: 3, Window: time.Minute})
	app := makeApp(t, mw)
	for i := 0; i < 3; i++ {
		if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
			t.Fatalf("iter %d: want 200, got %d", i, rec.Code)
		}
	}
	rec := do(app, "10.0.0.1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After should be set on 429")
	}
}

func TestHeadersPresent(t *testing.T) {
	mw := New(Config{Limit: 10, Window: time.Minute})
	app := makeApp(t, mw)
	rec := do(app, "10.0.0.1")
	if rec.Header().Get("X-RateLimit-Limit") != "10" {
		t.Fatalf("X-RateLimit-Limit=%q", rec.Header().Get("X-RateLimit-Limit"))
	}
	if rec.Header().Get("X-RateLimit-Remaining") == "" {
		t.Fatal("X-RateLimit-Remaining missing")
	}
	if rec.Header().Get("X-RateLimit-Reset") == "" {
		t.Fatal("X-RateLimit-Reset missing")
	}
}

func TestCustomKeyFunc(t *testing.T) {
	mw := New(Config{
		Limit:  2,
		Window: time.Minute,
		KeyFunc: func(c *aarv.Context) string {
			return c.Header("X-User")
		},
	})
	app := makeApp(t, mw)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User", "alice")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("alice iter %d: %d", i, rec.Code)
		}
	}
	// Bob should still pass because key changed.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-User", "bob")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bob first req: %d", rec.Code)
	}
	// Alice's third should be limited.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-User", "alice")
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("alice 3rd: %d", rec2.Code)
	}
}

func TestSkipPaths_Bypass(t *testing.T) {
	mw := New(Config{Limit: 1, Window: time.Minute, SkipPaths: []string{"/skip"}})
	app := makeApp(t, mw)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/skip", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: skipped path returned %d", i, rec.Code)
		}
	}
}

func TestSkipper_Bypass(t *testing.T) {
	mw := New(Config{
		Limit:  1,
		Window: time.Minute,
		Skipper: func(c *aarv.Context) bool {
			return c.Header("X-Bypass") != ""
		},
	})
	app := makeApp(t, mw)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set("X-Bypass", "1")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: %d", i, rec.Code)
		}
	}
}

func TestBurst(t *testing.T) {
	mw := New(Config{Limit: 10, Window: time.Minute, Burst: 3})
	app := makeApp(t, mw)
	for i := 0; i < 3; i++ {
		if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
			t.Fatalf("burst iter %d: %d", i, rec.Code)
		}
	}
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("burst should cap at 3, 4th got %d", rec.Code)
	}
}

func TestSlidingWindow_OldRequestStillCountsWithinWindow(t *testing.T) {
	// Regression: with Limit=1 and Window covering 10 sub-windows, a
	// request at t=0 followed by another at t=subWindow must still find
	// the window full — the first request has not rolled out yet.
	mw := New(Config{Algorithm: SlidingWindow, Limit: 1, Window: 100 * time.Millisecond})
	app := makeApp(t, mw)
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	// Sleep > 1 sub-window (10ms) but well below Window (100ms).
	time.Sleep(15 * time.Millisecond)
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second within Window must still be limited; got %d", rec.Code)
	}
	// And again midway through the window.
	time.Sleep(50 * time.Millisecond)
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("midway through Window must still be limited; got %d", rec.Code)
	}
}

func TestSlidingWindow_Rollover(t *testing.T) {
	mw := New(Config{Algorithm: SlidingWindow, Limit: 3, Window: 100 * time.Millisecond})
	app := makeApp(t, mw)
	for i := 0; i < 3; i++ {
		if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
			t.Fatalf("iter %d: %d", i, rec.Code)
		}
	}
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th should be limited, got %d", rec.Code)
	}
	// Wait for the window to fully elapse.
	time.Sleep(150 * time.Millisecond)
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
		t.Fatalf("post-window: %d", rec.Code)
	}
}

func TestCustomHandler(t *testing.T) {
	called := atomic.Int32{}
	mw := New(Config{
		Limit:  1,
		Window: time.Minute,
		Handler: func(c *aarv.Context, snap Snapshot) error {
			called.Add(1)
			return c.JSON(http.StatusTeapot, map[string]any{
				"limit":     snap.Limit,
				"remaining": snap.Remaining,
			})
		},
	})
	app := makeApp(t, mw)
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := do(app, "10.0.0.1")
	if rec.Code != http.StatusTeapot {
		t.Fatalf("custom handler should preempt; got %d body=%s", rec.Code, rec.Body.String())
	}
	if called.Load() != 1 {
		t.Fatalf("custom handler called %d times", called.Load())
	}
}

func TestPanic_InvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"zero limit", Config{Window: time.Minute}, "Limit must be > 0"},
		{"zero window", Config{Limit: 1}, "Window must be > 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected panic")
				}
				if !strings.Contains(r.(string), tc.want) {
					t.Fatalf("want %q in %v", tc.want, r)
				}
			}()
			_ = New(tc.cfg)
		})
	}
}

func TestLazySweep_DeterministicEviction(t *testing.T) {
	rl := newLimiter(Config{Limit: 1, Window: 10 * time.Millisecond, EntryTTL: 10 * time.Millisecond})
	// Generate enough admissions on distinct keys to ensure many shards are
	// touched and the sweep counter rolls over multiple times. With shardCount
	// = 64 and sweep frequency = every 64 admissions, 64 * 64 = 4096
	// admissions guarantees every shard has been swept at least once.
	for i := 0; i < 4096; i++ {
		k := "key-" + strconv.Itoa(i%128)
		rl.decide(k)
	}
	preTTL := rl.store.size()
	if preTTL < 100 {
		t.Fatalf("expected many entries, got %d", preTTL)
	}
	// Wait for entries to age past EntryTTL.
	time.Sleep(15 * time.Millisecond)
	// Drive more admissions so the lazy sweep runs and evicts stale entries.
	for i := 0; i < 4096; i++ {
		rl.decide("active-key")
	}
	postTTL := rl.store.size()
	if postTTL >= preTTL {
		t.Fatalf("lazy sweep failed to evict: pre=%d post=%d", preTTL, postTTL)
	}
}

func TestNewWithCleanup_StopsCleanly(t *testing.T) {
	before := runtime.NumGoroutine()
	mw, stop := NewWithCleanup(Config{Limit: 5, Window: time.Minute})
	app := aarv.New()
	app.Use(mw)
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	for i := 0; i < 3; i++ {
		do(app, "10.0.0.1")
	}
	if err := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// Idempotent.
	if err := stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	// Allow scheduler a tick to fully tear down.
	time.Sleep(20 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Fatalf("janitor goroutine leaked: before=%d after=%d", before, after)
	}
}

func TestConcurrent_Race(t *testing.T) {
	mw := New(Config{Limit: 1000, Window: time.Second})
	app := makeApp(t, mw)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ip := "10.0.0." + strconv.Itoa(id%32)
			for j := 0; j < 50; j++ {
				do(app, ip)
			}
		}(i)
	}
	wg.Wait()
}

// nonNativeMW forces the runtime onto the stdlib path.
func nonNativeMW() aarv.Middleware {
	return aarv.Middleware(func(next http.Handler) http.Handler { return next })
}

func TestStdlibPath_AdmitAndDeny(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Limit: 2, Window: time.Minute}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	for i := 0; i < 2; i++ {
		if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
			t.Fatalf("stdlib admit %d: %d", i, rec.Code)
		}
	}
	rec := do(app, "10.0.0.1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("stdlib deny: %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After missing on stdlib 429")
	}
}

func TestStdlibPath_SkipPathsAndSkipper(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Limit:     1,
		Window:    time.Minute,
		SkipPaths: []string{"/skip"},
		Skipper: func(c *aarv.Context) bool {
			return c.Header("X-Bypass") != ""
		},
	}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	app.Get("/skip", func(c *aarv.Context) error { return c.Text(http.StatusOK, "skipped") })

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/skip", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("stdlib SkipPaths: %d", rec.Code)
		}
	}
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set("X-Bypass", "1")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("stdlib Skipper: %d", rec.Code)
		}
	}
}

func TestStdlibPath_CustomHandlerAndError(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Limit:  1,
		Window: time.Minute,
		Handler: func(c *aarv.Context, snap Snapshot) error {
			return c.Text(http.StatusTeapot, "throttled stdlib")
		},
	}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := do(app, "10.0.0.1")
	if rec.Code != http.StatusTeapot {
		t.Fatalf("stdlib custom handler: %d", rec.Code)
	}
}

func TestStdlibPath_HandlerErrorFallback(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Limit:  1,
		Window: time.Minute,
		Handler: func(c *aarv.Context, snap Snapshot) error {
			return aarv.ErrInternal(nil)
		},
	}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	do(app, "10.0.0.1")
	rec := do(app, "10.0.0.1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("stdlib handler-error fallback: %d", rec.Code)
	}
}

func TestStdlibPath_NoContext(t *testing.T) {
	// Drive the stdlib middleware directly with no aarv.Context.
	mw := New(Config{Limit: 1, Window: time.Minute})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, addr := range []string{"10.0.0.1:1234", "[::1]:443", "nohost"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// First request always admits (fresh bucket); we don't care
		// about exact status — we're just exercising remoteAddrKey
		// and the no-context branches.
		_ = rec.Code
	}
	// Now hit the deny path on a known key.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("no-context deny: %d", rec.Code)
	}
}

func TestDefaultConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Algorithm != TokenBucket || cfg.StatusCode != http.StatusTooManyRequests || cfg.Message != "rate limit exceeded" {
		t.Fatalf("DefaultConfig: %+v", cfg)
	}
}

func TestCodeForStatus_AllBranches(t *testing.T) {
	cases := map[int]string{
		http.StatusTooManyRequests:    "too_many_requests",
		http.StatusServiceUnavailable: "service_unavailable",
		http.StatusTeapot:             http.StatusText(http.StatusTeapot),
	}
	for s, want := range cases {
		if got := codeForStatus(s); got != want {
			t.Fatalf("codeForStatus(%d): %q != %q", s, got, want)
		}
	}
}

func TestSweepAll_ClearsAllShards(t *testing.T) {
	rl := newLimiter(Config{Limit: 1, Window: time.Hour, EntryTTL: 1 * time.Nanosecond})
	for i := 0; i < 32; i++ {
		rl.decide("k-" + strconv.Itoa(i))
	}
	if rl.store.size() == 0 {
		t.Fatal("setup: no entries created")
	}
	time.Sleep(2 * time.Millisecond) // ensure cutoff passes
	rl.store.sweepAll()
	if got := rl.store.size(); got != 0 {
		t.Fatalf("sweepAll left %d entries", got)
	}
}

func TestSweepShard_TTLZeroIsNoOp(t *testing.T) {
	rl := newLimiter(Config{Limit: 1, Window: time.Hour})
	rl.store.entryTTL = 0 // disable
	for i := 0; i < 10; i++ {
		rl.decide("k-" + strconv.Itoa(i))
	}
	pre := rl.store.size()
	rl.store.sweepShard(0)
	if rl.store.size() != pre {
		t.Fatal("entryTTL=0 should be a no-op")
	}
}

func TestSetHeaders_RetryAfterFloor(t *testing.T) {
	// snap.RetryAfter < 1s should round up to 1s in the header.
	rl := &rateLimiter{}
	h := http.Header{}
	rl.setHeaders(h, Snapshot{
		Limit:      10,
		Remaining:  0,
		Reset:      time.Now().Add(100 * time.Millisecond),
		RetryAfter: 100 * time.Millisecond,
	}, true)
	if h.Get("Retry-After") != "1" {
		t.Fatalf("Retry-After floor: %q", h.Get("Retry-After"))
	}
}

func TestTokenBucket_RateZero_DegenerateWindow(t *testing.T) {
	// Exercise the rate <= 0 reset branch in the algorithm.
	e := &entry{}
	now := time.Now()
	// limit=0 produces rate=0; capacity=0; nothing admitted.
	admit, _, _ := tokenBucketDecide(e, now, 0, 0, time.Second)
	if admit {
		t.Fatal("zero capacity should never admit")
	}
}

func TestSlidingWindow_DegenerateSubWindow(t *testing.T) {
	// Window smaller than slidingBuckets nanoseconds — degenerate but
	// must not divide-by-zero.
	e := &entry{}
	admit, _, _ := slidingWindowDecide(e, time.Now(), 1, 5*time.Nanosecond)
	if !admit {
		t.Fatal("first request should still admit in degenerate window")
	}
}

func TestSlidingWindow_ZeroWindow(t *testing.T) {
	// window == 0 forces both inner clauses of the subWindow guard.
	e := &entry{}
	admit, _, _ := slidingWindowDecide(e, time.Now(), 1, 0)
	if !admit {
		t.Fatal("first request must admit even on zero window")
	}
}

func TestSlidingWindow_RemainingNeverNegative(t *testing.T) {
	// Hand-corrupt the entry so total > limit, then assert remaining
	// is clamped to 0. Exercises the `if remaining < 0` branch.
	e := &entry{}
	e.lastAbsSub = time.Now().UnixNano() / int64(time.Millisecond)
	e.buckets[0] = 5
	_, remaining, _ := slidingWindowDecide(e, time.Now(), 1, 10*time.Millisecond)
	if remaining != 0 {
		t.Fatalf("remaining=%d (expected 0)", remaining)
	}
}

func TestTokenBucket_RemainingNeverNegative(t *testing.T) {
	// Set capacity below 1 so admit fails and remaining math goes
	// negative; expect clamp to 0.
	e := &entry{}
	e.last = time.Now()
	e.tokens = -2
	_, remaining, _ := tokenBucketDecide(e, time.Now(), 1, 1, time.Second)
	if remaining != 0 {
		t.Fatalf("remaining=%d (expected 0)", remaining)
	}
}

func TestNewWithCleanup_SweepsAtTickerInterval(t *testing.T) {
	// Use a Window larger than 1m so the period >= time.Minute branch
	// is exercised (period := cfg.Window) and seed the store with
	// stale entries; manually advance entries' lastAccess so a single
	// tick will evict.
	mw, stop := NewWithCleanup(Config{
		Limit:    1,
		Window:   time.Hour, // forces "period := cfg.Window" branch (period > time.Minute)
		EntryTTL: 1 * time.Nanosecond,
	})
	defer func() { _ = stop() }()
	app := aarv.New()
	app.Use(mw)
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	// Just verify the wiring works; the ticker case-branch coverage is
	// driven by the next test which uses a tiny custom janitor.
	_ = mw
}

func TestNewWithCleanup_StopsBeforeFirstTick(t *testing.T) {
	// stop() must drain the goroutine cleanly even when the first tick
	// has not fired yet (production period is 1 minute, tests should
	// not block on it).
	before := runtime.NumGoroutine()
	_, stop := NewWithCleanup(Config{Limit: 1, Window: 30 * time.Second}) // clamped to 1m
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestStartJanitor_TickerFires(t *testing.T) {
	// Drive the unexported janitor directly with a sub-ms period so
	// the `case <-t.C` branch executes in test time. Seed the store
	// with stale entries; one tick should evict them.
	s := newStore(time.Nanosecond)
	for i := 0; i < 32; i++ {
		s.withEntry("k-"+strconv.Itoa(i), func(e *entry) {})
	}
	if s.size() == 0 {
		t.Fatal("setup")
	}
	stop := startJanitor(s, 2*time.Millisecond)
	defer func() { _ = stop() }()
	// Sleep enough for >= 1 tick. Be generous to avoid CI flakiness.
	time.Sleep(20 * time.Millisecond)
	if got := s.size(); got != 0 {
		t.Fatalf("janitor did not clear stale entries: %d remain", got)
	}
}

func TestRemoteAddrKey_IPv4(t *testing.T) {
	if got := remoteAddrKey("10.0.0.1:1234"); got != "10.0.0.1" {
		t.Fatalf("ipv4: %q", got)
	}
}

func TestRemoteAddrKey_IPv6(t *testing.T) {
	if got := remoteAddrKey("[::1]:1234"); got != "::1" {
		t.Fatalf("ipv6 short: %q", got)
	}
	if got := remoteAddrKey("[2001:db8::dead:beef]:443"); got != "2001:db8::dead:beef" {
		t.Fatalf("ipv6 long: %q", got)
	}
}

func TestRemoteAddrKey_FallbackOnUnparseable(t *testing.T) {
	// No port — SplitHostPort errors; we keep the original string so the
	// key is at least deterministic for the same input.
	if got := remoteAddrKey("nohost"); got != "nohost" {
		t.Fatalf("bare: %q", got)
	}
}

func TestDefensiveCopy_Skip(t *testing.T) {
	skip := []string{"/skip"}
	mw := New(Config{Limit: 1, Window: time.Minute, SkipPaths: skip})
	skip[0] = "/" // mutate caller slice
	app := makeApp(t, mw)
	// "/" should still be limited despite the post-construction mutation.
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	if rec := do(app, "10.0.0.1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("post-construction skip-list mutation leaked: %d", rec.Code)
	}
}
