package ratelimitredis

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// TestDefaultConfig covers the documented sugar constructor. It is
// pure and has no I/O.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d; want 429", cfg.StatusCode)
	}
	if cfg.Message != "rate limit exceeded" {
		t.Fatalf("Message = %q; want 'rate limit exceeded'", cfg.Message)
	}
}

// TestCodeForStatus covers all three branches: 429, 503, and the
// http.StatusText fallback.
func TestCodeForStatus(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusTooManyRequests, "rate_limit_exceeded"},
		{http.StatusServiceUnavailable, "service_unavailable"},
		{http.StatusForbidden, http.StatusText(http.StatusForbidden)},
		{http.StatusBadGateway, http.StatusText(http.StatusBadGateway)},
	}
	for _, c := range cases {
		if got := codeForStatus(c.status); got != c.want {
			t.Errorf("codeForStatus(%d) = %q; want %q", c.status, got, c.want)
		}
	}
}

// TestRequestIDOf covers the two short-circuit branches of the helper:
// hasCtx=false → "" and c=nil → "".
func TestRequestIDOf(t *testing.T) {
	if got := requestIDOf(nil, false); got != "" {
		t.Errorf("requestIDOf(nil,false) = %q; want \"\"", got)
	}
	if got := requestIDOf(nil, true); got != "" {
		t.Errorf("requestIDOf(nil,true) = %q; want \"\"", got)
	}
}

// TestLuaInt directly exercises every type branch of the helper that
// normalises Redis-Lua reply types across go-redis versions.
func TestLuaInt(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int64
	}{
		{"int64", int64(42), 42},
		{"int", int(7), 7},
		{"float64", float64(3.9), 3}, // truncated, not rounded
		{"string-numeric", "1234", 1234},
		{"string-empty", "", 0},
		{"string-garbage", "not a number", 0},
		{"bool-fallthrough", true, 0},
		{"nil-fallthrough", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := luaInt(c.in); got != c.want {
				t.Errorf("luaInt(%v) = %d; want %d", c.in, got, c.want)
			}
		})
	}
}

func TestSnapshotFromScriptReplyRejectsBadShape(t *testing.T) {
	_, snap, err := snapshotFromScriptReply([]any{int64(1), int64(2)}, 9)
	if err == nil {
		t.Fatal("expected bad-shape error")
	}
	if snap.Limit != 9 {
		t.Fatalf("snap limit = %d, want 9", snap.Limit)
	}
	if !strings.Contains(err.Error(), "unexpected script reply shape") {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = snapshotFromScriptReply("not-array", 9)
	if err == nil {
		t.Fatal("expected non-array error")
	}
}

func TestSnapshotFromScriptReplyClampsPastRetry(t *testing.T) {
	allowed, snap, err := snapshotFromScriptReply([]any{
		int64(1),
		int64(3),
		time.Now().Add(-time.Second).UnixMilli(),
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("allowed = false, want true")
	}
	if snap.Limit != 5 || snap.Remaining != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %v, want 0", snap.RetryAfter)
	}
}

// TestShouldSkipNativeSkipperBranch covers the native-path Skipper
// branch (existing tests cover SkipPaths but not the function-skipper
// branch on the native path).
func TestShouldSkipNativeSkipperBranch(t *testing.T) {
	rdb, _ := mustClient(t)
	app := aarv.New()
	app.Use(New(Config{
		Client:  rdb,
		Limit:   1,
		Burst:   1,
		Window:  time.Second,
		Skipper: func(c *aarv.Context) bool { return c.Header("X-Skip") == "yes" },
		KeyFunc: func(c *aarv.Context) string { return "native-skipper" },
	}))
	app.Get("/", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	tc := aarv.NewTestClient(app)
	// Limit is 1 — without skipper this would deny on the second hit.
	for range 4 {
		tc.WithHeader("X-Skip", "yes").Get("/").AssertStatus(t, http.StatusOK)
	}
}

// TestStdlibPathNoAarvContext exercises the stdlib middleware when the
// incoming request is NOT being served by an aarv.App — there is no
// *aarv.Context to recover, so keyFunc is bypassed and r.RemoteAddr
// is the rate-limit key.
func TestStdlibPathNoAarvContext(t *testing.T) {
	rdb, _ := mustClient(t)

	mw := New(Config{
		Client:  rdb,
		Limit:   2,
		Burst:   2,
		Window:  time.Second,
		KeyFunc: func(c *aarv.Context) string { return "should-not-be-called" },
	})
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw.Stdlib(final)

	// Three hits from the same fake remote addr; first 2 admit, 3rd denies.
	for i, expect := range []int{200, 200, 429} {
		req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != expect {
			t.Fatalf("hit %d: status = %d; want %d", i, rec.Code, expect)
		}
	}
}

// TestStdlibPathHandlerReturnsErrorFallsBack covers the stdlib branch
// where a custom Handler returns a non-nil error, triggering the
// generic writeJSONError fallback.
func TestStdlibPathHandlerReturnsErrorFallsBack(t *testing.T) {
	rdb, _ := mustClient(t)
	stubErr := http.ErrAbortHandler // any non-nil error will do

	app := aarv.New()
	app.Use(
		New(Config{
			Client:  rdb,
			Limit:   1,
			Burst:   1,
			Window:  time.Second,
			KeyFunc: func(c *aarv.Context) string { return "handler-err" },
			Handler: func(c *aarv.Context, snap Snapshot) error { return stubErr },
		}),
		stdlibSibling(),
	)
	app.Get("/", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	tc := aarv.NewTestClient(app)
	tc.Get("/").AssertStatus(t, http.StatusOK) // first hit consumes the budget
	r2 := tc.Get("/")
	r2.AssertStatus(t, http.StatusTooManyRequests)
	if !strings.Contains(r2.Text(), `"rate_limit_exceeded"`) {
		t.Fatalf("fallback body missing canonical code: %s", r2.Body)
	}
}

// TestNewTTLFloor covers the New() branch that floors ttlMs at 1000
// when 2*windowMs is shorter (i.e. Window < 500ms).
func TestNewTTLFloor(t *testing.T) {
	rdb, _ := mustClient(t)
	// Construction must not panic; the request-time check below actually
	// exercises the decide() ttl-floor branch (windowMs*2 = 200ms < 1000ms
	// floor → ttlMs clamped to 1000).
	app := aarv.New()
	app.Use(
		New(Config{
			Client:  rdb,
			Limit:   1,
			Burst:   1,
			Window:  100 * time.Millisecond, // 2*100ms = 200ms < 1000ms floor
			KeyFunc: func(c *aarv.Context) string { return "ttl-floor" },
		}),
		stdlibSibling(),
	)
	app.Get("/", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body)
	}
}

// TestWriteJSONError exercises the error-body writer directly.
func TestWriteJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, http.StatusServiceUnavailable, "service_unavailable", "down", "req_42")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	if want := `"request_id":"req_42"`; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("body missing %q: %s", want, rec.Body.String())
	}
}

// --- stdlib-path coverage ---
//
// The existing tests run through aarv's native fast path. To exercise
// the stdlib http.Handler branch (shouldSkipStdlib, the
// stdlib-side decide/setHeaders/writeJSONError flow) we install a
// stdlib-only sibling middleware that forces the chain to fall off
// the native fast path.

func stdlibSibling() aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

// TestStdlibPathAdmitsAndSetsHeaders verifies the stdlib-path admit
// flow sets the rate-limit headers and forwards.
func TestStdlibPathAdmitsAndSetsHeaders(t *testing.T) {
	rdb, _ := mustClient(t)

	app := aarv.New()
	app.Use(
		New(Config{
			Client:  rdb,
			Limit:   5,
			Window:  time.Second,
			KeyFunc: func(c *aarv.Context) string { return "k1" },
		}),
		stdlibSibling(),
	)
	app.Get("/", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-RateLimit-Limit") == "" {
		t.Fatalf("expected X-RateLimit-Limit on stdlib admit response")
	}
}

// TestStdlibPathDeniesEmitsJSON exercises the 429 branch with default
// JSON formatting (no custom Handler).
func TestStdlibPathDeniesEmitsJSON(t *testing.T) {
	rdb, _ := mustClient(t)

	app := aarv.New()
	app.Use(
		New(Config{
			Client:  rdb,
			Limit:   1,
			Burst:   1,
			Window:  time.Second,
			KeyFunc: func(c *aarv.Context) string { return "deny" },
		}),
		stdlibSibling(),
	)
	app.Get("/", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	tc := aarv.NewTestClient(app)
	tc.Get("/").AssertStatus(t, http.StatusOK)
	r2 := tc.Get("/")
	r2.AssertStatus(t, http.StatusTooManyRequests)
	if !strings.Contains(r2.Text(), `"rate_limit_exceeded"`) {
		t.Fatalf("denied body missing canonical code: %s", r2.Body)
	}
}

// TestStdlibPathSkipPaths covers shouldSkipStdlib's SkipPaths branch.
func TestStdlibPathSkipPaths(t *testing.T) {
	rdb, _ := mustClient(t)

	app := aarv.New()
	app.Use(
		New(Config{
			Client:    rdb,
			Limit:     1,
			Window:    time.Second,
			SkipPaths: []string{"/health"},
			KeyFunc:   func(c *aarv.Context) string { return "skipper" },
		}),
		stdlibSibling(),
	)
	app.Get("/health", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	// Hit /health 5x — limit is 1, but skip path bypasses entirely.
	tc := aarv.NewTestClient(app)
	for range 5 {
		tc.Get("/health").AssertStatus(t, http.StatusOK)
	}
}

// TestStdlibPathSkipper covers shouldSkipStdlib's Skipper branch.
func TestStdlibPathSkipper(t *testing.T) {
	rdb, _ := mustClient(t)

	app := aarv.New()
	app.Use(
		New(Config{
			Client:  rdb,
			Limit:   1,
			Window:  time.Second,
			Skipper: func(c *aarv.Context) bool { return c.Header("X-Bypass") == "1" },
			KeyFunc: func(c *aarv.Context) string { return "skipper2" },
		}),
		stdlibSibling(),
	)
	app.Get("/", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	tc := aarv.NewTestClient(app)
	for range 5 {
		tc.WithHeader("X-Bypass", "1").Get("/").AssertStatus(t, http.StatusOK)
	}
}

// TestStdlibPathFailOpen covers the stdlib-path fail-open branch when
// Redis is dead.
func TestStdlibPathFailOpen(t *testing.T) {
	rdb, mr := mustClient(t)
	mr.Close() // kill Redis so script EVALSHA fails

	app := aarv.New()
	app.Use(
		New(Config{
			Client:               rdb,
			Limit:                1,
			Window:               time.Second,
			FailOpenOnRedisError: true,
			KeyFunc:              func(c *aarv.Context) string { return "fail-open" },
		}),
		stdlibSibling(),
	)
	app.Get("/", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	aarv.NewTestClient(app).Get("/").AssertStatus(t, http.StatusOK)
}

// TestStdlibPathFailClosed covers the stdlib-path fail-closed branch
// (default behaviour) — Redis dead → 503 with rate-limit headers.
func TestStdlibPathFailClosed(t *testing.T) {
	rdb, mr := mustClient(t)
	mr.Close()

	app := aarv.New()
	app.Use(
		New(Config{
			Client:  rdb,
			Limit:   1,
			Window:  time.Second,
			KeyFunc: func(c *aarv.Context) string { return "fail-closed" },
		}),
		stdlibSibling(),
	)
	app.Get("/", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	r := aarv.NewTestClient(app).Get("/")
	r.AssertStatus(t, http.StatusServiceUnavailable)
	if !strings.Contains(r.Text(), `"rate_limit_unavailable"`) {
		t.Fatalf("expected unavailable code in body: %s", r.Body)
	}
}

// TestStdlibPathCustomHandler covers the stdlib-path "custom Handler"
// branch when the handler returns nil (custom response written) and
// when it returns an error (fallback JSON path).
func TestStdlibPathCustomHandler(t *testing.T) {
	rdb, _ := mustClient(t)

	called := 0
	app := aarv.New()
	app.Use(
		New(Config{
			Client:  rdb,
			Limit:   1,
			Burst:   1,
			Window:  time.Second,
			KeyFunc: func(c *aarv.Context) string { return "custom" },
			Handler: func(c *aarv.Context, snap Snapshot) error {
				called++
				return c.JSON(http.StatusTooManyRequests, map[string]string{"custom": "yes"})
			},
		}),
		stdlibSibling(),
	)
	app.Get("/", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	tc := aarv.NewTestClient(app)
	tc.Get("/").AssertStatus(t, http.StatusOK)
	r2 := tc.Get("/")
	r2.AssertStatus(t, http.StatusTooManyRequests)
	if !strings.Contains(r2.Text(), `"custom":"yes"`) {
		t.Fatalf("custom handler body missing: %s", r2.Body)
	}
	if called != 1 {
		t.Fatalf("handler called %d times; want 1", called)
	}
}
