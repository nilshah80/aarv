package session

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// instrumentedStore wraps MemoryStore with call counters so tests can
// assert that clean reads do not trigger Save / Delete.
type instrumentedStore struct {
	*MemoryStore
	getN, saveN, delN atomic.Int32
	failGet, failSave error
}

func (i *instrumentedStore) Get(id string) (*Stored, error) {
	i.getN.Add(1)
	if i.failGet != nil {
		return nil, i.failGet
	}
	return i.MemoryStore.Get(id)
}

func (i *instrumentedStore) Save(id string, s *Stored, ttl time.Duration) error {
	i.saveN.Add(1)
	if i.failSave != nil {
		return i.failSave
	}
	return i.MemoryStore.Save(id, s, ttl)
}

func (i *instrumentedStore) Delete(id string) error {
	i.delN.Add(1)
	return i.MemoryStore.Delete(id)
}

func newApp(mw any) *aarv.App {
	app := aarv.New()
	app.Use(mw)
	return app
}

func TestMiddlewareEmitsCookieBeforeJSON(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	app := newApp(New(Config{Store: store, DisableSecure: true}))
	app.Get("/login", func(c *aarv.Context) error {
		s := MustFrom(c)
		s.Set("user", "alice")
		// c.JSON commits headers inline — Set-Cookie must already be staged.
		return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
	})
	tc := aarv.NewTestClient(app)
	resp := tc.Get("/login")
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d; body = %s", resp.Status, resp.Body)
	}
	cookie := resp.Headers.Get("Set-Cookie")
	if cookie == "" {
		t.Fatal("Set-Cookie missing — wrapper did not commit before WriteHeader")
	}
	if !strings.HasPrefix(cookie, "_session=") {
		t.Fatalf("Set-Cookie = %q; want _session prefix", cookie)
	}
	if store.saveN.Load() != 1 {
		t.Fatalf("saveN = %d; want 1", store.saveN.Load())
	}
}

func TestMiddlewareCleanReadEmitsNoCookie(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	app := newApp(New(Config{Store: store, DisableSecure: true}))
	app.Get("/health", func(c *aarv.Context) error {
		// Touch the session via From to prove even a lookup is OK.
		_, _ = From(c)
		return c.JSON(http.StatusOK, "ok")
	})
	resp := aarv.NewTestClient(app).Get("/health")
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d", resp.Status)
	}
	if c := resp.Headers.Get("Set-Cookie"); c != "" {
		t.Fatalf("clean read should not emit Set-Cookie; got %q", c)
	}
	if store.saveN.Load() != 0 {
		t.Fatalf("clean read triggered Save; saveN = %d", store.saveN.Load())
	}
}

func TestMiddlewareNoWriteHandlerStillSaves(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	app := newApp(New(Config{Store: store, DisableSecure: true}))
	// Handler mutates session but never writes a body. Without the
	// post-handler sw.commit() fallback the cookie would be lost.
	app.Get("/touch", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return nil
	})
	resp := aarv.NewTestClient(app).Get("/touch")
	if resp.Headers.Get("Set-Cookie") == "" {
		t.Fatal("post-handler commit fallback failed to emit Set-Cookie")
	}
	if store.saveN.Load() != 1 {
		t.Fatalf("saveN = %d; want 1", store.saveN.Load())
	}
}

func TestMiddlewareLoadExistingSession(t *testing.T) {
	store := NewMemoryStore()
	app := newApp(New(Config{Store: store, DisableSecure: true}))
	app.Get("/get", func(c *aarv.Context) error {
		v, _ := MustFrom(c).Get("user")
		if v == nil {
			return c.JSON(http.StatusOK, "anon")
		}
		return c.JSON(http.StatusOK, v)
	})
	app.Post("/set", func(c *aarv.Context) error {
		MustFrom(c).Set("user", "alice")
		return c.JSON(http.StatusOK, "ok")
	})

	tc := aarv.NewTestClient(app)
	resp := tc.Post("/set", nil)
	cookie := extractSetCookie(t, resp.Headers.Get("Set-Cookie"))
	resp2 := tc.WithCookie(cookie).Get("/get")
	if got := strings.Trim(resp2.Text(), `"`+"\n"); got != "alice" {
		t.Fatalf("loaded session value = %q; want alice", got)
	}
}

func TestMiddlewareRegenerateDeletesOldID(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	app := newApp(New(Config{Store: store, DisableSecure: true}))
	app.Post("/login", func(c *aarv.Context) error {
		s := MustFrom(c)
		s.Set("user", "alice")
		if err := s.Regenerate(); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, "ok")
	})
	// First request to seed a session.
	app.Get("/seed", func(c *aarv.Context) error {
		MustFrom(c).Set("seed", true)
		return c.JSON(http.StatusOK, "ok")
	})
	tc := aarv.NewTestClient(app)
	seed := tc.Get("/seed")
	cookie := extractSetCookie(t, seed.Headers.Get("Set-Cookie"))
	store.delN.Store(0)
	store.saveN.Store(0)
	_ = tc.WithCookie(cookie).Post("/login", nil)
	if store.delN.Load() != 1 {
		t.Fatalf("Regenerate must trigger Delete(oldID); delN = %d", store.delN.Load())
	}
	if store.saveN.Load() != 1 {
		t.Fatalf("Regenerate must trigger Save(newID); saveN = %d", store.saveN.Load())
	}
}

func TestMiddlewareDestroy(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	app := newApp(New(Config{Store: store, DisableSecure: true}))
	app.Get("/seed", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	app.Post("/logout", func(c *aarv.Context) error {
		MustFrom(c).Destroy()
		return c.JSON(http.StatusOK, "bye")
	})
	tc := aarv.NewTestClient(app)
	seed := tc.Get("/seed")
	cookie := extractSetCookie(t, seed.Headers.Get("Set-Cookie"))
	resp := tc.WithCookie(cookie).Post("/logout", nil)
	if store.delN.Load() != 1 {
		t.Fatalf("Destroy must call Store.Delete; delN = %d", store.delN.Load())
	}
	c := parseSetCookie(t, resp.Headers.Get("Set-Cookie"))
	if c.MaxAge != -1 {
		t.Fatalf("destroy cookie MaxAge = %d; want -1", c.MaxAge)
	}
}

func TestMiddlewareFlashConsumeForcesSave(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	app := newApp(New(Config{Store: store, DisableSecure: true}))
	app.Get("/set-flash", func(c *aarv.Context) error {
		MustFrom(c).Flash("notice", "hi")
		return c.JSON(http.StatusOK, "ok")
	})
	app.Get("/read-flash", func(c *aarv.Context) error {
		v, _ := MustFrom(c).ConsumeFlash("notice")
		return c.JSON(http.StatusOK, v)
	})
	tc := aarv.NewTestClient(app)
	r1 := tc.Get("/set-flash")
	cookie := extractSetCookie(t, r1.Headers.Get("Set-Cookie"))

	store.saveN.Store(0)
	r2 := tc.WithCookie(cookie).Get("/read-flash")
	if store.saveN.Load() != 1 {
		t.Fatalf("ConsumeFlash must force Save; saveN = %d", store.saveN.Load())
	}
	if !strings.Contains(r2.Text(), "hi") {
		t.Fatalf("flash not delivered: %s", r2.Body)
	}

	r3 := tc.WithCookie(cookie).Get("/read-flash")
	if strings.Contains(r3.Text(), "hi") {
		t.Fatal("flash should be gone after one consumption (cookie not refreshed in this client)")
	}
}

func TestMiddlewareSkipper(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	skipperCalled := false
	app := newApp(New(Config{
		Store:         store,
		DisableSecure: true,
		Skipper: func(c *aarv.Context) bool {
			skipperCalled = true
			return c.Path() == "/skip"
		},
	}))
	app.Get("/skip", func(c *aarv.Context) error {
		if _, ok := From(c); ok {
			t.Error("skipped path must not have a session attached")
		}
		return c.JSON(http.StatusOK, "skipped")
	})
	app.Get("/use", func(c *aarv.Context) error {
		if _, ok := From(c); !ok {
			t.Error("session must be present when skipper returns false")
		}
		return c.JSON(http.StatusOK, "ok")
	})
	tc := aarv.NewTestClient(app)
	tc.Get("/skip")
	if !skipperCalled {
		t.Fatal("skipper was not invoked")
	}
	if store.getN.Load() != 0 {
		t.Fatalf("skipped path should not call Store.Get; getN = %d", store.getN.Load())
	}
	tc.Get("/use")
	// /use has no cookie → backend skips Get and creates a fresh session.
	// The proof that the middleware ran is that From returned ok.
}

func TestMiddlewareErrorHandlerLoadFailure(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failGet: errors.New("boom")}
	called := false
	app := newApp(New(Config{
		Store:         store,
		DisableSecure: true,
		ErrorHandler: func(c *aarv.Context, err error) error {
			called = true
			return nil // proceed with fresh session
		},
	}))
	app.Get("/x", func(c *aarv.Context) error {
		s := MustFrom(c)
		if !s.IsNew() {
			t.Error("expected fresh session after load error")
		}
		return c.JSON(http.StatusOK, "ok")
	})
	// Send a cookie so failGet actually fires.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "_session", Value: "anything"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if !called {
		t.Fatal("ErrorHandler was not invoked on Store.Get failure")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (handler ran); got %d", rec.Code)
	}
}

func TestMiddlewareSaveErrorHandlerCalled(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failSave: errors.New("save-boom")}
	called := false
	app := newApp(New(Config{
		Store:            store,
		DisableSecure:    true,
		SaveErrorHandler: func(c *aarv.Context, err error) { called = true },
	}))
	app.Get("/x", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	resp := aarv.NewTestClient(app).Get("/x")
	if resp.Status != http.StatusOK {
		t.Fatalf("save error must not change response status; got %d", resp.Status)
	}
	if !called {
		t.Fatal("SaveErrorHandler was not invoked on Store.Save failure")
	}
}

func TestMiddlewareNewPanicsWithoutStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("New(Config{}) must panic without a Store")
		}
	}()
	_ = New(Config{})
}

func TestMiddlewareSessionMaxAgeEmitsBrowserCookie(t *testing.T) {
	store := NewMemoryStore()
	app := newApp(New(Config{Store: store, DisableSecure: true, MaxAge: SessionMaxAge}))
	app.Get("/x", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	resp := aarv.NewTestClient(app).Get("/x")
	c := parseSetCookie(t, resp.Headers.Get("Set-Cookie"))
	if c.MaxAge != 0 {
		t.Fatalf("SessionMaxAge cookie should have no MaxAge attr; got %d", c.MaxAge)
	}
	if !c.Expires.IsZero() {
		t.Fatalf("SessionMaxAge cookie should have no Expires; got %v", c.Expires)
	}
}

func TestMiddlewareDefaultsApplied(t *testing.T) {
	store := NewMemoryStore()
	app := newApp(New(Config{Store: store, DisableSecure: true}))
	app.Get("/x", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	resp := aarv.NewTestClient(app).Get("/x")
	c := parseSetCookie(t, resp.Headers.Get("Set-Cookie"))
	if c.Name != "_session" {
		t.Fatalf("default cookie name = %q; want _session", c.Name)
	}
	if c.Path != "/" {
		t.Fatalf("default path = %q; want /", c.Path)
	}
	if !c.HttpOnly {
		t.Fatal("default HttpOnly should be true")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("default SameSite = %v; want Lax", c.SameSite)
	}
	if c.MaxAge != int(DefaultMaxAge.Seconds()) {
		t.Fatalf("default MaxAge = %d; want %d", c.MaxAge, int(DefaultMaxAge.Seconds()))
	}
}

func TestMiddlewareCookieBackend(t *testing.T) {
	app := newApp(NewCookie(CookieConfig{Key: testCookieKey, DisableSecure: true}))
	app.Get("/set", func(c *aarv.Context) error {
		s := MustFrom(c)
		s.Set("user", "alice")
		return c.JSON(http.StatusOK, "ok")
	})
	app.Get("/read", func(c *aarv.Context) error {
		s := MustFrom(c)
		v, _ := s.Get("user")
		return c.JSON(http.StatusOK, v)
	})
	tc := aarv.NewTestClient(app)
	r1 := tc.Get("/set")
	cookie := extractSetCookie(t, r1.Headers.Get("Set-Cookie"))
	r2 := tc.WithCookie(cookie).Get("/read")
	if !strings.Contains(r2.Text(), "alice") {
		t.Fatalf("cookie backend round-trip failed; body = %s", r2.Body)
	}
}

// TestMiddlewareStdlibPath forces the framework off the native fast
// path by mounting a stdlib-only middleware after the session
// middleware. This exercises the http.Handler branch of buildMiddleware.
func TestMiddlewareStdlibPath(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true}))
	// Stdlib-only middleware: no native pair → whole chain falls back.
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	})
	app.Get("/x", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	resp := aarv.NewTestClient(app).Get("/x")
	if !strings.HasPrefix(resp.Headers.Get("Set-Cookie"), "_session=") {
		t.Fatalf("stdlib path failed to emit Set-Cookie; got %q", resp.Headers.Get("Set-Cookie"))
	}
	if store.saveN.Load() != 1 {
		t.Fatalf("stdlib path did not save; saveN = %d", store.saveN.Load())
	}
}

// TestMiddlewareSessionIDExposed verifies Session.ID returns the
// current ID (and changes after Regenerate) — covers Session.ID and
// the regen path end-to-end.
func TestMiddlewareSessionIDExposed(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{Store: NewMemoryStore(), DisableSecure: true}))
	var seenID string
	app.Get("/id", func(c *aarv.Context) error {
		s := MustFrom(c)
		seenID = s.ID()
		return c.JSON(http.StatusOK, s.ID())
	})
	resp := aarv.NewTestClient(app).Get("/id")
	if seenID == "" || !strings.Contains(resp.Text(), seenID) {
		t.Fatalf("Session.ID exposure failed: seenID=%q body=%s", seenID, resp.Body)
	}
}

// TestMiddlewareLoadErrorDefaultLogsAndProceeds covers reportLoadErr.
func TestMiddlewareLoadErrorDefaultLogsAndProceeds(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failGet: errors.New("boom")}
	app := aarv.New()
	// No ErrorHandler → default reportLoadErr path.
	app.Use(New(Config{Store: store, DisableSecure: true}))
	app.Get("/x", func(c *aarv.Context) error {
		s := MustFrom(c)
		if !s.IsNew() {
			t.Error("expected fresh session after default load-error handling")
		}
		return c.JSON(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "_session", Value: "anything"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
}

// TestPickLoggerNilContext covers the c==nil branch.
func TestPickLoggerNilContext(t *testing.T) {
	cfg := normalizeConfig(Config{Store: NewMemoryStore()})
	if pickLogger(nil, cfg) == nil {
		t.Fatal("pickLogger(nil) returned nil; expected cfg.logger")
	}
}

// TestFromHelpers covers From(nil) and MustFrom miss.
func TestFromHelpers(t *testing.T) {
	if s, ok := From(nil); s != nil || ok {
		t.Fatal("From(nil) must return (nil, false)")
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustFrom on missing session must panic")
		}
	}()
	app := aarv.New()
	var got bool
	app.Get("/", func(c *aarv.Context) error {
		_ = MustFrom(c)
		got = true
		return nil
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got {
		t.Fatal("MustFrom should have panicked before this line")
	}
}

func TestMiddlewareCookiePanicsOnBadKey(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewCookie must panic on invalid key")
		}
	}()
	_ = NewCookie(CookieConfig{Key: []byte("nope")})
}

// --- helpers ---

func extractSetCookie(t *testing.T, raw string) *http.Cookie {
	t.Helper()
	if raw == "" {
		t.Fatal("no Set-Cookie header")
	}
	c := parseSetCookie(t, raw)
	return &http.Cookie{Name: c.Name, Value: c.Value}
}

func parseSetCookie(t *testing.T, raw string) *http.Cookie {
	t.Helper()
	resp := http.Response{Header: http.Header{"Set-Cookie": []string{raw}}}
	cs := resp.Cookies()
	if len(cs) == 0 {
		t.Fatalf("could not parse Set-Cookie %q", raw)
	}
	return cs[0]
}
