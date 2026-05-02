package throttle

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

func makeApp(t *testing.T, mw aarv.Middleware, handler aarv.HandlerFunc) *aarv.App {
	t.Helper()
	app := aarv.New()
	app.Use(mw)
	app.Get("/", handler)
	app.Get("/skip", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "skipped")
	})
	return app
}

func do(app *aarv.App, path string) *httptest.ResponseRecorder {
	if path == "" {
		path = "/"
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestUnderLimit_Passes(t *testing.T) {
	mw := New(Config{MaxConcurrent: 4})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})
	for i := 0; i < 10; i++ {
		if rec := do(app, ""); rec.Code != http.StatusOK {
			t.Fatalf("iter %d: want 200, got %d", i, rec.Code)
		}
	}
}

func TestAtLimit_NoQueue_Returns503(t *testing.T) {
	hold := make(chan struct{})
	released := make(chan struct{})
	mw := New(Config{MaxConcurrent: 1})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go func() {
		do(app, "")
		close(released)
	}()
	// Give the first request time to enter the handler.
	waitForGoroutineInsideHandler(t)
	rec := do(app, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d body=%q", rec.Code, rec.Body.String())
	}
	close(hold)
	<-released
}

func TestAtLimit_WithQueue_WaitsAndProceeds(t *testing.T) {
	hold := make(chan struct{})
	mw := New(Config{
		MaxConcurrent: 1,
		QueueSize:     2,
		QueueTimeout:  500 * time.Millisecond,
	})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	results := make(chan int, 3)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- do(app, "").Code
		}()
	}
	// Let all three goroutines reach acquire().
	time.Sleep(50 * time.Millisecond)
	// Release them all serially.
	close(hold)
	wg.Wait()
	close(results)
	count200 := 0
	for code := range results {
		if code == http.StatusOK {
			count200++
		}
	}
	if count200 != 3 {
		t.Fatalf("all three queued requests should succeed, got %d", count200)
	}
}

func TestQueueFull_Returns503(t *testing.T) {
	hold := make(chan struct{})
	mw := New(Config{
		MaxConcurrent: 1,
		QueueSize:     1,
		QueueTimeout:  2 * time.Second,
	})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	// Slot 1: enters handler, blocks on hold.
	go do(app, "")
	waitForGoroutineInsideHandler(t)

	// Slot 2: takes queue token, blocks waiting for slot.
	queued := make(chan int, 1)
	go func() { queued <- do(app, "").Code }()
	time.Sleep(50 * time.Millisecond)

	// Slot 3: queue full, fails fast.
	rec := do(app, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 (queue full), got %d", rec.Code)
	}

	close(hold)
	if got := <-queued; got != http.StatusOK {
		t.Fatalf("queued request should have succeeded, got %d", got)
	}
}

func TestQueueTimeout_Returns503(t *testing.T) {
	hold := make(chan struct{})
	mw := New(Config{
		MaxConcurrent: 1,
		QueueSize:     5,
		QueueTimeout:  20 * time.Millisecond,
	})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go do(app, "")
	waitForGoroutineInsideHandler(t)

	start := time.Now()
	rec := do(app, "")
	elapsed := time.Since(start)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 on queue timeout, got %d", rec.Code)
	}
	if elapsed < 15*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("expected ~20ms wait, got %v", elapsed)
	}

	close(hold)
}

func TestSlotReleasedOnHandlerError(t *testing.T) {
	mw := New(Config{MaxConcurrent: 1})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		return aarv.ErrInternal(nil)
	})
	for i := 0; i < 5; i++ {
		rec := do(app, "")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("iter %d: want 500, got %d", i, rec.Code)
		}
	}
}

func TestSlotReleasedOnPanic(t *testing.T) {
	// Compose: Recovery → Throttle → handler-that-panics.
	app := aarv.New()
	app.Use(aarv.Recovery())
	app.Use(New(Config{MaxConcurrent: 1}))
	var calls atomic.Int32
	app.Get("/", func(c *aarv.Context) error {
		n := calls.Add(1)
		if n == 1 {
			panic("boom")
		}
		return c.Text(http.StatusOK, "ok")
	})
	// First call panics; Recovery turns it into a 500.
	rec1 := do(app, "")
	if rec1.Code != http.StatusInternalServerError {
		t.Fatalf("first call: want 500, got %d", rec1.Code)
	}
	// Second call must succeed — slot was released by the throttle's defer.
	rec2 := do(app, "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("second call: slot leaked after panic, got %d", rec2.Code)
	}
}

func TestSkipPaths_Bypass(t *testing.T) {
	hold := make(chan struct{})
	mw := New(Config{
		MaxConcurrent: 1,
		SkipPaths:     []string{"/skip"},
	})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go do(app, "")
	waitForGoroutineInsideHandler(t)

	// /skip bypasses the throttle even with the slot held.
	if rec := do(app, "/skip"); rec.Code != http.StatusOK {
		t.Fatalf("want 200 on skipped path, got %d", rec.Code)
	}
	close(hold)
}

func TestSkipper_Bypass(t *testing.T) {
	hold := make(chan struct{})
	mw := New(Config{
		MaxConcurrent: 1,
		Skipper: func(c *aarv.Context) bool {
			return c.Header("X-Bypass") != ""
		},
	})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go do(app, "")
	waitForGoroutineInsideHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/skip", nil)
	req.Header.Set("X-Bypass", "1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 via Skipper, got %d", rec.Code)
	}
	close(hold)
}

func TestCustomHandler_Preempts(t *testing.T) {
	hold := make(chan struct{})
	called := atomic.Bool{}
	mw := New(Config{
		MaxConcurrent: 1,
		Handler: func(c *aarv.Context) error {
			called.Store(true)
			return c.Text(http.StatusTeapot, "throttled")
		},
	})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go do(app, "")
	waitForGoroutineInsideHandler(t)

	rec := do(app, "")
	if !called.Load() {
		t.Fatal("custom handler not invoked")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("want 418, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "throttled") {
		t.Fatalf("body=%q", rec.Body.String())
	}
	close(hold)
}

func TestPanic_InvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"zero MaxConcurrent", Config{MaxConcurrent: 0}, "MaxConcurrent must be > 0"},
		{"negative MaxConcurrent", Config{MaxConcurrent: -1}, "MaxConcurrent must be > 0"},
		{"negative QueueSize", Config{MaxConcurrent: 1, QueueSize: -1}, "QueueSize must be >= 0"},
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

func TestConcurrent_Race(t *testing.T) {
	mw := New(Config{MaxConcurrent: 4, QueueSize: 8, QueueTimeout: 200 * time.Millisecond})
	var inFlight atomic.Int32
	var maxObserved atomic.Int32
	app := makeApp(t, mw, func(c *aarv.Context) error {
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			old := maxObserved.Load()
			if now <= old || maxObserved.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		return c.Text(http.StatusOK, "ok")
	})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do(app, "")
		}()
	}
	wg.Wait()
	if got := maxObserved.Load(); got > 4 {
		t.Fatalf("MaxConcurrent breached: observed %d simultaneous handlers", got)
	}
}

// nonNativeMW forces the runtime onto the stdlib path.
func nonNativeMW() aarv.Middleware {
	return aarv.Middleware(func(next http.Handler) http.Handler { return next })
}

func TestQueuedNoTimeout_NonBlockingTry(t *testing.T) {
	// QueueSize > 0 but QueueTimeout == 0 exercises the queued-but-no-wait
	// branch in acquire(). The queued goroutine takes a queue token, tries
	// to grab a slot non-blockingly, and (since the slot is held) returns
	// false immediately.
	hold := make(chan struct{})
	defer close(hold)
	mw := New(Config{
		MaxConcurrent: 1,
		QueueSize:     1,
		// QueueTimeout: 0 — explicit, no wait
	})
	app := makeApp(t, mw, func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})
	go do(app, "")
	waitForGoroutineInsideHandler(t)

	rec := do(app, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("queued+no-timeout: %d", rec.Code)
	}
}

func TestStdlibPath_AdmitAndDeny(t *testing.T) {
	hold := make(chan struct{})
	mw := New(Config{MaxConcurrent: 1})
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(mw)
	app.Get("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	go do(app, "")
	waitForGoroutineInsideHandler(t)

	rec := do(app, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stdlib deny: %d", rec.Code)
	}
	if rid := rec.Header().Get("Content-Type"); rid != "application/json; charset=utf-8" {
		t.Fatalf("content-type: %q", rid)
	}
	close(hold)
}

func TestStdlibPath_SkipPathsAndSkipper(t *testing.T) {
	hold := make(chan struct{})
	mw := New(Config{
		MaxConcurrent: 1,
		SkipPaths:     []string{"/skip"},
		Skipper: func(c *aarv.Context) bool {
			return c.Header("X-Bypass") != ""
		},
	})
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(mw)
	// Only the path-without-bypass blocks. The X-Bypass and /skip
	// requests must not deadlock if Skipper / SkipPaths are honored.
	app.Get("/", func(c *aarv.Context) error {
		if c.Header("X-Bypass") == "" {
			<-hold
		}
		return c.Text(http.StatusOK, "ok")
	})
	app.Get("/skip", func(c *aarv.Context) error { return c.Text(http.StatusOK, "skipped") })

	go do(app, "")
	waitForGoroutineInsideHandler(t)

	if rec := do(app, "/skip"); rec.Code != http.StatusOK {
		t.Fatalf("stdlib SkipPaths: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Bypass", "1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib Skipper: %d", rec.Code)
	}
	close(hold)
}

func TestStdlibPath_CustomHandlerAndError(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		MaxConcurrent: 1,
		Handler: func(c *aarv.Context) error {
			return c.Text(http.StatusTeapot, "throttled stdlib")
		},
	}))
	app.Get("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	go do(app, "")
	waitForGoroutineInsideHandler(t)
	rec := do(app, "")
	if rec.Code != http.StatusTeapot {
		t.Fatalf("stdlib custom handler: %d", rec.Code)
	}
}

func TestStdlibPath_HandlerReturnsError_FallsBackToJSON(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		MaxConcurrent: 1,
		Handler: func(c *aarv.Context) error {
			return aarv.ErrInternal(nil)
		},
	}))
	app.Get("/", func(c *aarv.Context) error {
		<-hold
		return c.Text(http.StatusOK, "ok")
	})

	go do(app, "")
	waitForGoroutineInsideHandler(t)
	rec := do(app, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stdlib handler-error fallback: %d", rec.Code)
	}
}

func TestStdlibPath_NoContext(t *testing.T) {
	// Drive the stdlib middleware directly with no aarv.Context wired
	// in, to exercise the requestIDOf nil-context branch.
	hold := make(chan struct{})
	defer close(hold)
	mw := New(Config{MaxConcurrent: 1})

	first := make(chan struct{})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first <- struct{}{}
		<-hold
		w.WriteHeader(http.StatusOK)
	}))

	go func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()
	<-first

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no-context deny: %d", rec.Code)
	}
}

func TestDefaultConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.StatusCode != http.StatusServiceUnavailable || cfg.Message != "service unavailable" {
		t.Fatalf("DefaultConfig: %+v", cfg)
	}
}

func TestCodeForStatus_AllBranches(t *testing.T) {
	cases := map[int]string{
		http.StatusServiceUnavailable: "service_unavailable",
		http.StatusTooManyRequests:    "too_many_requests",
		http.StatusTeapot:             http.StatusText(http.StatusTeapot),
	}
	for s, want := range cases {
		if got := codeForStatus(s); got != want {
			t.Fatalf("codeForStatus(%d): got %q want %q", s, got, want)
		}
	}
}

// waitForGoroutineInsideHandler gives a previously launched goroutine
// a brief window to enter the handler and seize a slot. 50ms is enough on
// any sensible machine; the alternative — instrumenting the handler with
// a "ready" channel — bloats tests that all need the same primitive.
func waitForGoroutineInsideHandler(t *testing.T) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
}
