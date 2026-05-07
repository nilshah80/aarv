package session

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// failReader returns 0,EOF to exhaust crypto/rand-style readers.
type failReader struct{}

func (failReader) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func swapRand(t *testing.T, r io.Reader) {
	t.Helper()
	old := randReader
	randReader = r
	t.Cleanup(func() { randReader = old })
}

// TestCookieStoreEncodeRandFailure exercises the encode path's
// nonce-generation error branch.
func TestCookieStoreEncodeRandFailure(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := normalizeCookieConfig(CookieConfig{Key: testCookieKey})
	swapRand(t, failReader{})
	if _, err := cs.encode(cookieEnvelope{IssuedAt: time.Now().Unix()}, cfg); err == nil {
		t.Fatal("encode must surface rand failure")
	}
}

// TestCookieStoreFreshSessionRandFailure exercises the freshSession
// error path, which fires when generateID hits a rand failure.
func TestCookieStoreFreshSessionRandFailure(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := normalizeCookieConfig(CookieConfig{Key: testCookieKey})
	swapRand(t, failReader{})
	if _, err := cs.freshSession(cfg); err == nil {
		t.Fatal("freshSession must surface rand failure")
	}
}

// TestCookieStoreLoadFreshFailure forces the load path's "no cookie →
// freshSession → rand error" propagation. The middleware translates
// it into ErrorHandler.
func TestCookieStoreLoadFreshFailure(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := normalizeCookieConfig(CookieConfig{Key: testCookieKey})
	swapRand(t, failReader{})
	if _, err := cs.load(nil, httptest.NewRequest(http.MethodGet, "/", nil), cfg); err == nil {
		t.Fatal("load with no cookie must surface rand failure from freshSession")
	}
}

// TestCookieStoreDecodeShortBlob covers the "raw too short" branch.
func TestCookieStoreDecodeShortBlob(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := normalizeCookieConfig(CookieConfig{Key: testCookieKey})
	// "AAAA" base64-decodes to 3 bytes — less than the GCM nonce size.
	if _, err := cs.decode("AAAA", cfg); err == nil {
		t.Fatal("decode of too-short blob should fail")
	}
}

// TestSaveErrorHandlerOnDelete exercises the Regenerate-delete-fail
// branch reported via SaveErrorHandler.
func TestSaveErrorHandlerOnDelete(t *testing.T) {
	store := &deleteFailingStore{MemoryStore: NewMemoryStore()}
	saveErrCalls := atomic.Int32{}
	app := aarv.New()
	app.Use(New(Config{
		Store:            store,
		DisableSecure:    true,
		SaveErrorHandler: func(c *aarv.Context, err error) { saveErrCalls.Add(1) },
	}))
	app.Get("/seed", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	app.Post("/regen", func(c *aarv.Context) error {
		s := MustFrom(c)
		return s.Regenerate()
	})
	tc := aarv.NewTestClient(app)
	r1 := tc.Get("/seed")
	cookie := extractSetCookie(t, r1.Headers.Get("Set-Cookie"))

	store.failNextDelete.Store(true)
	tc.WithCookie(cookie).Post("/regen", nil)
	if saveErrCalls.Load() != 1 {
		t.Fatalf("SaveErrorHandler not called for Delete failure; calls = %d", saveErrCalls.Load())
	}
}

type deleteFailingStore struct {
	*MemoryStore
	failNextDelete atomic.Bool
}

func (d *deleteFailingStore) Delete(id string) error {
	if d.failNextDelete.CompareAndSwap(true, false) {
		return errors.New("delete-boom")
	}
	return d.MemoryStore.Delete(id)
}

// TestStoreBackendNoCookieRandFailure exercises the storeBackend.load
// path when there is no cookie and ID generation fails.
func TestStoreBackendNoCookieRandFailure(t *testing.T) {
	b := &storeBackend{store: NewMemoryStore()}
	cfg := normalizeConfig(Config{Store: NewMemoryStore()})
	swapRand(t, failReader{})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := b.load(nil, r, cfg); err == nil {
		t.Fatal("storeBackend.load with no cookie must surface rand failure")
	}
}

// TestStoreBackendDestroyError exercises the destroy error branch.
func TestStoreBackendDestroyError(t *testing.T) {
	store := &deleteFailingStore{MemoryStore: NewMemoryStore()}
	store.failNextDelete.Store(true)
	b := &storeBackend{store: store}
	cfg := normalizeConfig(Config{Store: store})
	rec := httptest.NewRecorder()
	if err := b.destroy(nil, rec, newSession("id", false, defaultIDLen), cfg); err == nil {
		t.Fatal("destroy must propagate Delete failure")
	}
}

// TestSessionDestroyedSettersAreNoOps directly exercises the
// destroyed-guards on Flash and ConsumeFlash.
func TestSessionDestroyedSettersAreNoOps(t *testing.T) {
	s := newSession("id", false, defaultIDLen)
	s.Destroy()
	s.Flash("k", "v")
	if v, ok := s.ConsumeFlash("k"); ok || v != nil {
		t.Fatal("Flash/ConsumeFlash must be no-ops after Destroy")
	}
}

// TestSessionDeleteOnDestroyedNoop covers the destroyed-guard on Delete.
func TestSessionDeleteOnDestroyedNoop(t *testing.T) {
	s := newSession("id", false, defaultIDLen)
	s.Set("k", "v")
	s.Destroy()
	prevDirty := s.dirty
	s.Delete("k")
	if s.dirty != prevDirty {
		t.Fatal("Delete after Destroy should not flip dirty")
	}
}

// TestCloneStoredNil and similar small helpers.
func TestCloneStoredNilInput(t *testing.T) {
	if cloneStored(nil) != nil {
		t.Fatal("cloneStored(nil) must be nil")
	}
}

// TestCookieStoreEncodeJSONFailure forces a JSON marshal failure by
// stuffing the Stored.Data with a value that json.Marshal rejects
// (a channel cannot be marshaled).
func TestCookieStoreEncodeJSONFailure(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := normalizeCookieConfig(CookieConfig{Key: testCookieKey})
	sess := newSession("id", true, defaultIDLen)
	sess.Set("ch", make(chan int))
	rec := httptest.NewRecorder()
	if err := cs.save(nil, rec, sess, cfg); err == nil {
		t.Fatal("save with non-marshalable value must fail")
	}
}

// TestStoreBackendLoadStoreError exercises the b.store.Get error path.
func TestStoreBackendLoadStoreError(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failGet: errors.New("boom")}
	b := &storeBackend{store: store}
	cfg := normalizeConfig(Config{Store: store})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: cfg.cookieName, Value: "id"})
	if _, err := b.load(nil, r, cfg); err == nil {
		t.Fatal("load must propagate store.Get error")
	}
}

// TestStoreBackendLoadMissingEntryMintsFreshID asserts the
// session-fixation defense: when the client supplies a cookie whose ID
// is not present in the store (forged, expired, or rotated key), the
// backend MUST mint a fresh random ID rather than reuse the
// attacker-supplied one. Reusing the supplied id would let an attacker
// pre-plant `_session=known-id` and then have the victim's login Save
// persist under that attacker-chosen id.
func TestStoreBackendLoadMissingEntryMintsFreshID(t *testing.T) {
	store := NewMemoryStore()
	b := &storeBackend{store: store}
	cfg := normalizeConfig(Config{Store: store})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: cfg.cookieName, Value: "attacker-planted-id"})
	s, err := b.load(nil, r, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.id == "attacker-planted-id" {
		t.Fatal("session-fixation: backend reused attacker-supplied id")
	}
	if !s.IsNew() {
		t.Fatal("missing-entry load must mark session as new")
	}
}

// TestStdlibErrorHandlerReturnsErrorWrites500 covers the stdlib path's
// "ErrorHandler returned non-nil" branch that writes a generic 500.
func TestStdlibErrorHandlerReturnsErrorWrites500(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failGet: errors.New("boom")}
	app := aarv.New()
	app.Use(New(Config{
		Store:         store,
		DisableSecure: true,
		ErrorHandler:  func(c *aarv.Context, err error) error { return err }, // signal "stop"
	}))
	// Force stdlib path with a non-native middleware.
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	})
	app.Get("/x", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "should not run") })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "_session", Value: "anything"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
}

// TestStdlibPathSkipperBypasses covers the stdlib-path skipper branch
// (hasCtx=true, skipper returns true) — the middleware short-circuits
// to next.ServeHTTP(w, r) without ever reaching backend.load.
func TestStdlibPathSkipperBypasses(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	app := aarv.New()
	app.Use(New(Config{
		Store:         store,
		DisableSecure: true,
		Skipper:       func(c *aarv.Context) bool { return c.Path() == "/skip" },
	}))
	// Stdlib-only sibling forces the chain off the native fast path.
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	})
	app.Get("/skip", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })
	resp := aarv.NewTestClient(app).Get("/skip")
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d", resp.Status)
	}
	if store.getN.Load() != 0 {
		t.Fatalf("skipper bypass failed; getN = %d", store.getN.Load())
	}
}

// TestStdlibPathFreshSessionRandFailure covers the stdlib-path branch
// where the load-error fallback's backend.freshSession also fails
// (rand exhausted). The middleware writes a generic 500 and stops.
func TestStdlibPathFreshSessionRandFailure(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failGet: errors.New("boom")}
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true}))
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	})
	app.Get("/x", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	swapRand(t, failReader{}) // makes generateID fail in the fallback path

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "_session", Value: "anything"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500 (rand-failure fallback)", rec.Code)
	}
}

// TestNativePathErrorHandlerReturnsErr covers the native-path branch
// where ErrorHandler returns a non-nil error — that error propagates
// up through the middleware chain.
func TestNativePathErrorHandlerReturnsErr(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failGet: errors.New("boom")}
	called := false
	app := aarv.New()
	app.Use(New(Config{
		Store:         store,
		DisableSecure: true,
		ErrorHandler: func(c *aarv.Context, err error) error {
			called = true
			return aarv.ErrServiceUnavailable("session backend down")
		},
	}))
	app.Get("/x", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "should not run") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "_session", Value: "anything"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if !called {
		t.Fatal("ErrorHandler not invoked")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 (handler-returned error)", rec.Code)
	}
}

// TestNativePathFreshSessionRandFailure covers the native-path branch
// where the load-error fallback's backend.freshSession also fails.
// The middleware returns the rand error, which the framework error
// handler translates to a 500.
func TestNativePathFreshSessionRandFailure(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failGet: errors.New("boom")}
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true}))
	app.Get("/x", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })

	swapRand(t, failReader{})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "_session", Value: "anything"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500 (native-path rand failure)", rec.Code)
	}
}

// TestSessionFromStoredNilMaps covers the defensive nil-map branches
// in sessionFromStored: when Stored.Data and/or Stored.Flash come in
// as nil (legacy entries from older serializers), the function must
// substitute fresh empty maps so handlers calling Set/Flash don't
// nil-deref.
func TestSessionFromStoredNilMaps(t *testing.T) {
	st := &Stored{} // both Data and Flash are nil
	s := sessionFromStored("id", st, defaultIDLen)
	if s.data == nil {
		t.Fatal("sessionFromStored must replace nil Data with empty map")
	}
	if s.flash == nil {
		t.Fatal("sessionFromStored must replace nil Flash with empty map")
	}
	// Verify Set still works without panic.
	s.Set("k", "v")
	if v, _ := s.Get("k"); v != "v" {
		t.Fatal("Set after nil-Data fallback failed")
	}
}

// TestRegenerateZeroIDLenFallsBackToDefault covers the
// idLen <= 0 defensive branch in Regenerate. Triggered by hand-
// constructing a Session with idLen=0 (legacy / unconfigured shape).
func TestRegenerateZeroIDLenFallsBackToDefault(t *testing.T) {
	s := newSession("orig", false, 0) // idLen explicitly zero
	if err := s.Regenerate(); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	// 32 raw bytes → 43 base64url characters (no padding).
	if want := 43; len(s.id) != want {
		t.Fatalf("regenerated id length = %d; want %d (default fallback)", len(s.id), want)
	}
}

// TestCookieStoreLoadEnvWithNilStored covers the env.Stored == nil
// defensive branch: even when decode succeeds but the inner Stored
// pointer is nil, load must produce a usable empty session.
func TestCookieStoreLoadEnvWithNilStored(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := normalizeCookieConfig(CookieConfig{Key: testCookieKey})

	// Encode an envelope with Stored = nil explicitly.
	encoded, err := cs.encode(cookieEnvelope{IssuedAt: time.Now().Unix(), Stored: nil}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cookie", cfg.cookieName+"="+encoded)
	sess, err := cs.load(nil, r, cfg)
	if err != nil {
		t.Fatalf("load with nil Stored returned err: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session")
	}
	if sess.IsNew() {
		t.Fatal("decoded session should not be marked as new (decode succeeded)")
	}
	// Set must not panic on the synthesized empty Data map.
	sess.Set("ok", true)
}

// TestCookieStoreFreshSessionZeroIDLenFallsBack covers the
// idLen <= 0 fallback inside CookieStore.freshSession.
func TestCookieStoreFreshSessionZeroIDLenFallsBack(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := &normalized{idLength: 0} // zero → must default to defaultIDLen
	s, err := cs.freshSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := 43; len(s.id) != want { // 32 raw bytes → 43 base64url chars
		t.Fatalf("fresh id length = %d; want %d (default fallback)", len(s.id), want)
	}
}

// TestCookieStoreDecodeShortRawAfterB64 covers the "len(raw) < ns"
// branch in decode — base64 decodes to fewer bytes than the GCM nonce.
func TestCookieStoreDecodeShortRawAfterB64(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := &normalized{cookieName: "_session"}
	// "AAAAAAAA" → 6 bytes, less than NonceSize (12).
	if _, err := cs.decode("AAAAAAAA", cfg); err == nil {
		t.Fatal("expected error on too-short blob")
	}
}

// TestCookieStoreDecodeExpiredViaIssuedAt covers the "age > maxAge"
// branch where the embedded IssuedAt timestamp causes a server-side
// expiry rejection (independent of the client-spoofable cookie
// Max-Age attribute).
func TestCookieStoreDecodeExpiredViaIssuedAt(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := &normalized{cookieName: "_session", maxAge: time.Minute}

	// Issue a cookie with IssuedAt 10 minutes ago — server-side
	// expiry should reject regardless of the configured cookie
	// Max-Age (which is what the client could spoof).
	envelope := cookieEnvelope{
		IssuedAt: time.Now().Add(-10 * time.Minute).Unix(),
		Stored:   &Stored{Data: map[string]any{"k": "v"}},
	}
	encoded, err := cs.encode(envelope, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.decode(encoded, cfg); err == nil {
		t.Fatal("expected expiry error from IssuedAt check")
	}
}

// TestStdlibErrorHandlerProceedsWithFresh covers the stdlib path's
// "ErrorHandler returned nil → continue with fresh" branch.
func TestStdlibErrorHandlerProceedsWithFresh(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failGet: errors.New("boom")}
	called := false
	app := aarv.New()
	app.Use(New(Config{
		Store:         store,
		DisableSecure: true,
		ErrorHandler:  func(c *aarv.Context, err error) error { called = true; return nil },
	}))
	// Force stdlib path.
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	})
	app.Get("/x", func(c *aarv.Context) error { return c.JSON(http.StatusOK, "ok") })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "_session", Value: "anything"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if !called {
		t.Fatal("ErrorHandler was not invoked on stdlib path")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
}

// TestStoreBackendSaveError exercises the storeBackend.save error
// branch when Store.Save returns an error directly.
func TestStoreBackendSaveError(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore(), failSave: errors.New("save-boom")}
	b := &storeBackend{store: store}
	cfg := normalizeConfig(Config{Store: store})
	rec := httptest.NewRecorder()
	if err := b.save(nil, rec, newSession("id", true, defaultIDLen), cfg); err == nil {
		t.Fatal("save must propagate Store.Save error")
	}
}
