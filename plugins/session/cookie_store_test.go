package session

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testCookieKey = []byte("0123456789abcdef0123456789abcdef") // 32 bytes

func newCookieCfg(t *testing.T) *normalized {
	t.Helper()
	return normalizeCookieConfig(CookieConfig{Key: testCookieKey})
}

func TestNewCookieStoreInvalidKey(t *testing.T) {
	if _, err := NewCookieStore(nil); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("nil key returned %v; want ErrInvalidKey", err)
	}
	if _, err := NewCookieStore([]byte("short")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("short key returned %v; want ErrInvalidKey", err)
	}
}

func TestCookieStoreRoundTrip(t *testing.T) {
	cs, err := NewCookieStore(testCookieKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := newCookieCfg(t)

	// Save a populated session.
	sess := newSession("ignored-id", true, defaultIDLen)
	sess.Set("user", "alice")
	sess.Flash("notice", "saved")
	if _, err := sess.CSRFToken(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := cs.save(nil, rec, sess, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if setCookie == "" {
		t.Fatal("no Set-Cookie emitted")
	}

	// Load it back.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cookie", setCookie)
	loaded, err := cs.load(nil, r, cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if v, _ := loaded.Get("user"); v != "alice" {
		t.Fatalf("data mismatch: got %v", v)
	}
	if v, ok := loaded.ConsumeFlash("notice"); !ok || v != "saved" {
		t.Fatalf("flash mismatch: (%v, %v)", v, ok)
	}
	tok, _ := loaded.CSRFToken()
	if origTok, _ := sess.CSRFToken(); tok != origTok {
		t.Fatal("CSRF token did not survive round-trip")
	}
}

func TestCookieStoreTamperedYieldsFresh(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := newCookieCfg(t)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cookie", cfg.cookieName+"=not-a-valid-payload")
	sess, err := cs.load(nil, r, cfg)
	if err != nil {
		t.Fatalf("load with tampered cookie returned err: %v", err)
	}
	if !sess.IsNew() {
		t.Fatal("tampered cookie must yield a fresh session")
	}
}

func TestCookieStoreMissingCookieYieldsFresh(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := newCookieCfg(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := cs.load(nil, r, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.IsNew() {
		t.Fatal("missing cookie must yield fresh session")
	}
}

func TestCookieStoreOversizePayload(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := newCookieCfg(t)

	sess := newSession("id", true, defaultIDLen)
	// 4 KiB string blows the 3.5 KiB encoded guard once base64 + GCM
	// overhead is added.
	sess.Set("blob", strings.Repeat("x", 4096))

	rec := httptest.NewRecorder()
	if err := cs.save(nil, rec, sess, cfg); !errors.Is(err, ErrCookiePayloadTooLarge) {
		t.Fatalf("save with oversize payload returned %v; want ErrCookiePayloadTooLarge", err)
	}
}

func TestCookieStoreExpiry(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := newCookieCfg(t)
	cfg.maxAge = 1 // 1 nanosecond → cookie always expired

	rec := httptest.NewRecorder()
	sess := newSession("id", true, defaultIDLen)
	sess.Set("k", "v")
	if err := cs.save(nil, rec, sess, cfg); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
	loaded, _ := cs.load(nil, r, cfg)
	if !loaded.IsNew() {
		t.Fatal("expired cookie must yield fresh session")
	}
}

func TestCookieStoreDestroy(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := newCookieCfg(t)
	rec := httptest.NewRecorder()
	if err := cs.destroy(nil, rec, newSession("id", false, defaultIDLen), cfg); err != nil {
		t.Fatal(err)
	}
	c := rec.Result().Cookies()[0]
	if c.MaxAge != -1 {
		t.Fatalf("destroy cookie MaxAge = %d; want -1", c.MaxAge)
	}
}
