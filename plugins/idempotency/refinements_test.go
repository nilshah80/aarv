package idempotency

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// --- Custom ErrorHandler ---

// TestPayloadMismatch_NativeUsesCustomErrorHandler covers
// errPayloadMismatchNative's `if n.errFn != nil` branch.
func TestPayloadMismatch_NativeUsesCustomErrorHandler(t *testing.T) {
	store := NewMemoryStore()
	var calls atomic.Int32
	mw := New(Config{
		Store:           store,
		TTL:             time.Hour,
		HashRequestBody: true,
		ErrorHandler: func(c *aarv.Context, status int, message string) error {
			calls.Add(1)
			return aarv.NewError(status, "custom_payload_mismatch", message)
		},
	})

	app := aarv.New()
	app.Use(mw)
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusCreated, "ok") })

	// First request: succeeds, stores response keyed by hash of body "a".
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body)))
		req.Header.Set("Idempotency-Key", "k-native")
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	_ = post("a")
	r2 := post("DIFFERENT-BODY")
	if r2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d; want 422; body=%s", r2.Code, r2.Body)
	}
	if calls.Load() != 1 {
		t.Fatalf("custom ErrorHandler not invoked; calls=%d", calls.Load())
	}
	if !strings.Contains(r2.Body.String(), "custom_payload_mismatch") {
		t.Fatalf("expected custom error code in body, got %s", r2.Body)
	}
}

// TestPayloadMismatch_StdlibUsesCustomErrorHandler covers
// errPayloadMismatchStdlib's `if hasCtx && n.errFn != nil` branch.
func TestPayloadMismatch_StdlibUsesCustomErrorHandler(t *testing.T) {
	// Force the stdlib path via a non-native sibling middleware.
	nonNativeMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	store := NewMemoryStore()
	var calls atomic.Int32
	app := aarv.New()
	app.Use(nonNativeMW)
	app.Use(New(Config{
		Store:           store,
		TTL:             time.Hour,
		HashRequestBody: true,
		ErrorHandler: func(c *aarv.Context, status int, message string) error {
			calls.Add(1)
			return aarv.NewError(status, "custom_payload_mismatch_stdlib", message)
		},
	}))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusCreated, "ok") })

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body)))
		req.Header.Set("Idempotency-Key", "k-stdlib")
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	_ = post("a")
	r2 := post("DIFFERENT-BODY")
	if r2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d; want 422; body=%s", r2.Code, r2.Body)
	}
	if calls.Load() != 1 {
		t.Fatalf("custom ErrorHandler not invoked on stdlib path; calls=%d", calls.Load())
	}
}

// --- CachedHeaders allowlist ---

func TestCachedHeaders_DefaultAllowlistRoundtrip(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, TTL: time.Hour}))
	app.Post("/", func(c *aarv.Context) error {
		// Default-allowlist headers: should replay.
		c.SetHeader("Content-Type", "application/json")
		c.SetHeader("ETag", `"abc"`)
		c.SetHeader("Location", "/r/123")
		// Off-list custom: should NOT replay.
		c.SetHeader("X-Custom", "secret")
		// Hard-blocked even if added to allowlist: must NOT replay.
		c.SetHeader("Authorization", "Bearer leaked")
		c.SetHeader("Set-Cookie", "session=leaked")
		c.SetHeader("X-Request-Id", "req-123")
		return c.JSON(http.StatusCreated, map[string]string{"ok": "yes"})
	})

	r1 := postKey(app, "k1")
	r2 := postKey(app, "k1")
	if r2.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("expected replay marker, got %q", r2.Header().Get("Idempotency-Replayed"))
	}
	if r2.Header().Get("ETag") != `"abc"` {
		t.Fatalf("ETag should replay, got %q", r2.Header().Get("ETag"))
	}
	if r2.Header().Get("Location") != "/r/123" {
		t.Fatalf("Location should replay, got %q", r2.Header().Get("Location"))
	}
	if r2.Header().Get("X-Custom") != "" {
		t.Fatalf("X-Custom must not replay (off-list), got %q", r2.Header().Get("X-Custom"))
	}
	if r2.Header().Get("Authorization") != "" {
		t.Fatalf("Authorization must not replay, got %q", r2.Header().Get("Authorization"))
	}
	if r2.Header().Get("Set-Cookie") != "" {
		t.Fatalf("Set-Cookie must not replay, got %q", r2.Header().Get("Set-Cookie"))
	}
	if r2.Header().Get("X-Request-Id") == "req-123" {
		t.Fatalf("X-Request-Id must not be cached")
	}
	_ = r1
}

func TestCachedHeaders_HardBlockedEvenWhenAllowlisted(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	// Even though Authorization is on the allowlist, the hard-strip
	// list takes precedence — the allowlist cannot opt back in to a
	// blocked header.
	app.Use(New(Config{
		Store:         store,
		TTL:           time.Hour,
		CachedHeaders: []string{"Authorization", "Set-Cookie", "Content-Type"},
	}))
	app.Post("/", func(c *aarv.Context) error {
		c.SetHeader("Authorization", "Bearer x")
		c.SetHeader("Set-Cookie", "s=1")
		return c.Text(http.StatusOK, "k")
	})

	postKey(app, "h1")
	r2 := postKey(app, "h1")
	if r2.Header().Get("Authorization") != "" {
		t.Fatalf("hard block bypassed: Authorization replayed")
	}
	if r2.Header().Get("Set-Cookie") != "" {
		t.Fatalf("hard block bypassed: Set-Cookie replayed")
	}
}

func TestCachedHeaders_EmptyDropsAll(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(New(Config{
		Store:         store,
		TTL:           time.Hour,
		CachedHeaders: []string{}, // explicit empty: drop every header
	}))
	app.Post("/", func(c *aarv.Context) error {
		c.SetHeader("Content-Type", "application/json")
		c.SetHeader("ETag", `"x"`)
		return c.JSON(http.StatusOK, map[string]string{})
	})

	postKey(app, "ek1")
	r2 := postKey(app, "ek1")
	if r2.Header().Get("Content-Type") != "" {
		t.Fatalf("empty allowlist must drop Content-Type, got %q", r2.Header().Get("Content-Type"))
	}
	if r2.Header().Get("ETag") != "" {
		t.Fatalf("empty allowlist must drop ETag, got %q", r2.Header().Get("ETag"))
	}
}

// --- CacheStatusFunc ---

func TestCacheStatusFunc_OverridesAllowlist(t *testing.T) {
	store := NewMemoryStore()
	hits := atomic.Int32{}
	app := aarv.New()
	app.Use(New(Config{
		Store: store,
		TTL:   time.Hour,
		CacheStatusFunc: func(s int) bool {
			// ALP-style: cache 2xx and deterministic 4xx, never 5xx.
			return (s >= 200 && s < 300) || s == http.StatusConflict
		},
		CachedHeaders: []string{"Content-Type"},
	}))
	app.Post("/", func(c *aarv.Context) error {
		hits.Add(1)
		// Return 409 with a deterministic body.
		return c.JSON(http.StatusConflict, map[string]string{"err": "alias_in_use"})
	})

	r1 := postKey(app, "fn1")
	r2 := postKey(app, "fn1")
	if r1.Code != http.StatusConflict || r2.Code != http.StatusConflict {
		t.Fatalf("expected 409 both: %d / %d", r1.Code, r2.Code)
	}
	if r2.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("CacheStatusFunc opted-in 409 should replay, got marker %q", r2.Header().Get("Idempotency-Replayed"))
	}
	if hits.Load() != 1 {
		t.Fatalf("handler ran %d times, expected 1 (cached 409)", hits.Load())
	}
}

func TestCacheStatusFunc_RejectsServerErrors(t *testing.T) {
	store := NewMemoryStore()
	hits := atomic.Int32{}
	app := aarv.New()
	app.Use(New(Config{
		Store: store,
		TTL:   time.Hour,
		CacheStatusFunc: func(s int) bool {
			return s >= 200 && s < 400
		},
	}))
	app.Post("/", func(c *aarv.Context) error {
		hits.Add(1)
		return c.JSON(http.StatusInternalServerError, map[string]string{"err": "boom"})
	})

	postKey(app, "se1")
	postKey(app, "se1")
	if hits.Load() != 2 {
		t.Fatalf("handler ran %d times, expected 2 (5xx not cached)", hits.Load())
	}
}

// --- Payload mismatch error code ---

func TestPayloadMismatch_NativeEmitsContractCode(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(New(Config{
		Store:           store,
		TTL:             time.Hour,
		HashRequestBody: true,
	}))
	app.Post("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "y"})
	})

	postWithBody(app, "pm-key", `{"a":1}`)
	rec := postWithBody(app, "pm-key", `{"a":2}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d want 422", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != PayloadMismatchErrorCode {
		t.Fatalf("got error code %q want %q", body.Error, PayloadMismatchErrorCode)
	}
}

func TestPayloadMismatch_StdlibEmitsContractCode(t *testing.T) {
	// Force the stdlib path via a non-native middleware.
	nonNativeMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(aarv.Middleware(nonNativeMW))
	app.Use(New(Config{
		Store:           store,
		TTL:             time.Hour,
		HashRequestBody: true,
	}))
	app.Post("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "y"})
	})

	postWithBody(app, "pms-key", `{"a":1}`)
	rec := postWithBody(app, "pms-key", `{"a":2}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d want 422", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != PayloadMismatchErrorCode {
		t.Fatalf("stdlib path: got error code %q want %q", body.Error, PayloadMismatchErrorCode)
	}
}

// --- Per-route TTL ---

// trackingStore captures the TTL passed to every Save call so the
// test can assert which value the middleware resolved.
type trackingStore struct {
	*MemoryStore
	saved []time.Duration
}

func newTrackingStore() *trackingStore {
	return &trackingStore{MemoryStore: NewMemoryStore()}
}

func (s *trackingStore) Save(key string, resp *Response, ttl time.Duration) error {
	s.saved = append(s.saved, ttl)
	return s.MemoryStore.Save(key, resp, ttl)
}

func TestPerRouteTTL_OverridesGlobal(t *testing.T) {
	store := newTrackingStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, TTL: time.Hour}))
	app.Post("/short", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "y"})
	}, aarv.WithRouteIdempotencyTTL(5*time.Minute))
	app.Post("/default", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "y"})
	})

	postKeyToPath(app, "/short", "s1")
	postKeyToPath(app, "/default", "d1")

	if len(store.saved) != 2 {
		t.Fatalf("expected 2 saves, got %d", len(store.saved))
	}
	if store.saved[0] != 5*time.Minute {
		t.Fatalf("/short TTL: got %v want 5m", store.saved[0])
	}
	if store.saved[1] != time.Hour {
		t.Fatalf("/default TTL: got %v want 1h", store.saved[1])
	}
}

func TestPerRouteTTL_ZeroOptsOut(t *testing.T) {
	store := newTrackingStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, TTL: time.Hour}))
	app.Post("/no-cache", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "y"})
	}, aarv.WithRouteIdempotencyTTL(0))

	r1 := postKeyToPath(app, "/no-cache", "n1")
	r2 := postKeyToPath(app, "/no-cache", "n1")
	if len(store.saved) != 0 {
		t.Fatalf("zero TTL should opt out of caching; saw %d saves", len(store.saved))
	}
	if r2.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatalf("zero-TTL route must not produce a replay, but did")
	}
	_ = r1
}

func TestPerRouteTTL_GroupRoute(t *testing.T) {
	store := newTrackingStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, TTL: time.Hour}))
	app.Group("/v1", func(g *aarv.RouteGroup) {
		g.Post("/links", func(c *aarv.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"ok": "y"})
		}, aarv.WithRouteIdempotencyTTL(15*time.Minute))
	})

	postKeyToPath(app, "/v1/links", "g1")
	if len(store.saved) != 1 {
		t.Fatalf("expected 1 save, got %d", len(store.saved))
	}
	if store.saved[0] != 15*time.Minute {
		t.Fatalf("got %v want 15m", store.saved[0])
	}
}

// --- helpers ---

func postKey(app *aarv.App, key string) *httptest.ResponseRecorder {
	return postKeyToPath(app, "/", key)
}

func postKeyToPath(app *aarv.App, path, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(""))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func postWithBody(app *aarv.App, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(body)))
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}
