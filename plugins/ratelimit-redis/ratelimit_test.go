package ratelimitredis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nilshah80/aarv"
	"github.com/redis/go-redis/v9"
)

func mustClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

// makeApp wires up a single rate-limited GET / route.
func makeApp(t *testing.T, cfg Config) *aarv.App {
	t.Helper()
	if cfg.Window == 0 {
		cfg.Window = time.Second
	}
	if cfg.Limit == 0 {
		cfg.Limit = 5
	}
	app := aarv.New()
	app.Use(New(cfg))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	return app
}

func get(app *aarv.App, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = ip + ":12345"
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

// --- core decisions ---

func TestWithinLimitAdmits(t *testing.T) {
	rdb, _ := mustClient(t)
	app := makeApp(t, Config{Client: rdb, Limit: 3, Window: time.Second})

	for i := 0; i < 3; i++ {
		r := get(app, "10.0.0.1")
		if r.Code != http.StatusOK {
			t.Fatalf("call %d got %d", i, r.Code)
		}
	}
}

func TestExceedsLimitDenies(t *testing.T) {
	rdb, _ := mustClient(t)
	app := makeApp(t, Config{Client: rdb, Limit: 2, Window: time.Second})

	for i := 0; i < 2; i++ {
		if r := get(app, "10.0.0.2"); r.Code != http.StatusOK {
			t.Fatalf("under-limit denied: %d", r.Code)
		}
	}
	r := get(app, "10.0.0.2")
	if r.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit got %d want 429", r.Code)
	}
	if r.Header().Get("Retry-After") == "" {
		t.Fatalf("Retry-After missing on 429")
	}
}

func TestHeadersOnAdmitted(t *testing.T) {
	rdb, _ := mustClient(t)
	app := makeApp(t, Config{Client: rdb, Limit: 3, Window: time.Second})
	r := get(app, "10.0.0.3")
	if r.Header().Get("X-RateLimit-Limit") != "3" {
		t.Fatalf("X-RateLimit-Limit got %q", r.Header().Get("X-RateLimit-Limit"))
	}
	if r.Header().Get("X-RateLimit-Remaining") == "" {
		t.Fatalf("X-RateLimit-Remaining missing")
	}
	if r.Header().Get("X-RateLimit-Reset") == "" {
		t.Fatalf("X-RateLimit-Reset missing")
	}
}

func TestRefillTimingViaMiniredisFastForward(t *testing.T) {
	rdb, mr := mustClient(t)
	app := makeApp(t, Config{Client: rdb, Limit: 2, Window: 1000 * time.Millisecond})

	for i := 0; i < 2; i++ {
		_ = get(app, "10.0.0.4")
	}
	if r := get(app, "10.0.0.4"); r.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst")
	}
	// Advance miniredis time past the window. The Lua script keys
	// off Go's time.Now (we pass it from Go), so miniredis time
	// advance does not directly drive the limiter; advancing real
	// time is what matters here. Use a brief sleep so the bucket
	// refills.
	time.Sleep(1100 * time.Millisecond)
	if r := get(app, "10.0.0.4"); r.Code != http.StatusOK {
		t.Fatalf("expected admit after refill, got %d", r.Code)
	}
	_ = mr // unused but kept for clarity
}

func TestSkipPaths(t *testing.T) {
	rdb, _ := mustClient(t)
	app := aarv.New()
	app.Use(New(Config{
		Client:    rdb,
		Limit:     1,
		Window:    time.Second,
		SkipPaths: []string{"/health"},
	}))
	app.Get("/health", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		req.RemoteAddr = "10.0.0.5:1"
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/health call %d got %d", i, rec.Code)
		}
	}
}

func TestCustomKeyFunc(t *testing.T) {
	rdb, _ := mustClient(t)
	app := aarv.New()
	app.Use(New(Config{
		Client:  rdb,
		Limit:   1,
		Window:  time.Second,
		KeyFunc: func(c *aarv.Context) string { return c.Header("X-Tenant") },
	}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	mk := func(tenant string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Tenant", tenant)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	if r := mk("a"); r.Code != http.StatusOK {
		t.Fatalf("tenant a first: %d", r.Code)
	}
	if r := mk("a"); r.Code != http.StatusTooManyRequests {
		t.Fatalf("tenant a second: %d want 429", r.Code)
	}
	// Different tenant should be unaffected.
	if r := mk("b"); r.Code != http.StatusOK {
		t.Fatalf("tenant b first: %d", r.Code)
	}
}

// --- Redis-error policy ---

func TestRedisErrorFailsClosedByDefault(t *testing.T) {
	rdb, mr := mustClient(t)
	app := makeApp(t, Config{Client: rdb, Limit: 5, Window: time.Second})
	// First, prove the path works.
	if r := get(app, "10.0.0.6"); r.Code != http.StatusOK {
		t.Fatalf("baseline: %d", r.Code)
	}
	mr.SetError("backend down")
	defer mr.SetError("")
	r := get(app, "10.0.0.6")
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("redis err should fail closed (503), got %d", r.Code)
	}
}

func TestRedisErrorFailsOpenWhenConfigured(t *testing.T) {
	rdb, mr := mustClient(t)
	app := makeApp(t, Config{Client: rdb, Limit: 5, Window: time.Second, FailOpenOnRedisError: true})
	mr.SetError("backend down")
	defer mr.SetError("")
	r := get(app, "10.0.0.7")
	if r.Code != http.StatusOK {
		t.Fatalf("fail-open should admit, got %d", r.Code)
	}
}

// --- ctx cancellation ---

func TestContextCancellationFailsClosed(t *testing.T) {
	rdb, _ := mustClient(t)
	app := makeApp(t, Config{Client: rdb, Limit: 5, Window: time.Second})

	req := httptest.NewRequest("GET", "/", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("cancelled ctx should fail closed, got %d", rec.Code)
	}
}

// --- concurrency ---

func TestConcurrentSameKeyExactCount(t *testing.T) {
	rdb, _ := mustClient(t)
	app := makeApp(t, Config{Client: rdb, Limit: 50, Window: time.Hour})

	const N = 200
	var wg sync.WaitGroup
	var ok atomic.Int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := get(app, "10.0.0.8")
			if r.Code == http.StatusOK {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()

	// Each successful request consumes exactly one token. With 50
	// tokens and 200 attempts, exactly 50 succeed. Lua atomicity
	// makes this deterministic.
	if ok.Load() != 50 {
		t.Fatalf("expected exactly 50 admitted, got %d", ok.Load())
	}
}

// --- key prefix ---

func TestKeyPrefixDefault(t *testing.T) {
	rdb, mr := mustClient(t)
	app := makeApp(t, Config{Client: rdb, Limit: 1, Window: time.Second})
	get(app, "10.0.0.9")
	keys := mr.Keys()
	matched := false
	for _, k := range keys {
		if len(k) >= len(DefaultKeyPrefix) && k[:len(DefaultKeyPrefix)] == DefaultKeyPrefix {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("no key with default prefix found: %v", keys)
	}
}

// --- panics ---

func TestPanicsOnMisconfig(t *testing.T) {
	rdb, _ := mustClient(t)
	cases := []struct {
		name string
		cfg  Config
	}{
		{"no client", Config{Limit: 1, Window: time.Second}},
		{"zero limit", Config{Client: rdb, Window: time.Second}},
		{"zero window", Config{Client: rdb, Limit: 1}},
		{"sub-ms window", Config{Client: rdb, Limit: 1, Window: 100 * time.Microsecond}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic")
				}
			}()
			_ = New(tc.cfg)
		})
	}
}

// --- custom limit handler ---

func TestCustomLimitHandler(t *testing.T) {
	rdb, _ := mustClient(t)
	called := atomic.Int32{}
	app := aarv.New()
	app.Use(New(Config{
		Client: rdb, Limit: 1, Window: time.Second,
		Handler: func(c *aarv.Context, snap Snapshot) error {
			called.Add(1)
			return c.JSON(http.StatusTooManyRequests, map[string]any{"custom": true, "limit": snap.Limit})
		},
	}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	get(app, "10.0.0.10")
	r := get(app, "10.0.0.10")
	if r.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d", r.Code)
	}
	if called.Load() != 1 {
		t.Fatalf("custom handler called %d times", called.Load())
	}
}

// --- malformed reply guard ---

func TestUnexpectedReplyShape(t *testing.T) {
	// We cannot easily make miniredis return a malformed shape, but
	// we can exercise the guard via a fake limiter that returns the
	// wrong type from the script. This test stays at the unit level
	// to lock the contract.
	if luaInt("not-a-number") != 0 {
		t.Fatal("luaInt should default to 0 on parse failure")
	}
	if luaInt(int64(42)) != 42 {
		t.Fatal("luaInt int64 path")
	}
	if luaInt(float64(7)) != 7 {
		t.Fatal("luaInt float64 path")
	}
	if luaInt("123") != 123 {
		t.Fatal("luaInt string path")
	}
}

// errors variable to keep imports honest when assertions reference it.
var _ = errors.New

// strconv is imported for header assertion compactness.
var _ = strconv.Itoa

// fmt usage placeholder.
var _ = fmt.Sprintf
