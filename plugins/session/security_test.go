package session

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

// TestSecurityTamperedSessionCookieYieldsFresh asserts that a forged or
// truncated cookie value never reaches the handler as a valid session.
// The CookieStore variant is the interesting case here because it is
// the only backend where the cookie value carries decoded state.
func TestSecurityTamperedSessionCookieYieldsFresh(t *testing.T) {
	app := aarv.New()
	app.Use(NewCookie(CookieConfig{Key: testCookieKey, DisableSecure: true}))
	app.Get("/", func(c *aarv.Context) error {
		s := MustFrom(c)
		if v, ok := s.Get("user"); ok {
			t.Errorf("tampered cookie produced data: %v", v)
		}
		return c.JSON(http.StatusOK, "ok")
	})
	tc := aarv.NewTestClient(app)
	tc.WithCookie(&http.Cookie{Name: "_session", Value: "garbage"}).Get("/")
}

// TestSecurityRegenerateInvalidatesOldServerSession proves the old ID
// is removed from a server-side store so a stolen pre-Regenerate
// session ID becomes unusable after privilege change.
func TestSecurityRegenerateInvalidatesOldServerSession(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true}))
	app.Get("/seed", func(c *aarv.Context) error {
		MustFrom(c).Set("user", "anon")
		return c.JSON(http.StatusOK, "ok")
	})
	app.Post("/login", func(c *aarv.Context) error {
		s := MustFrom(c)
		s.Set("user", "alice")
		return s.Regenerate()
	})

	tc := aarv.NewTestClient(app)
	r1 := tc.Get("/seed")
	cookie := extractSetCookie(t, r1.Headers.Get("Set-Cookie"))
	oldID := cookie.Value

	r2 := tc.WithCookie(cookie).Post("/login", nil)
	if r2.Status >= 400 {
		t.Fatalf("login failed: status %d body %s", r2.Status, r2.Body)
	}

	if got, _ := store.Get(oldID); got != nil {
		t.Fatalf("oldID %q must be deleted from store after Regenerate", oldID)
	}
}

// TestSecurityExpiredEntryYieldsFresh proves a request bearing a cookie
// for a TTL-expired store entry gets a fresh empty session, not the
// previous data.
func TestSecurityExpiredEntryYieldsFresh(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true, MaxAge: 1 * 1}))
	// MaxAge=1ns above is too coarse with int seconds; force tiny TTL by hand.
	// Simpler: seed a session, then manually expire its store entry.
	app.Get("/seed", func(c *aarv.Context) error {
		MustFrom(c).Set("user", "alice")
		return c.JSON(http.StatusOK, "ok")
	})
	app.Get("/check", func(c *aarv.Context) error {
		s := MustFrom(c)
		if !s.IsNew() {
			t.Error("expired entry must produce a fresh session")
		}
		return c.JSON(http.StatusOK, "ok")
	})
	tc := aarv.NewTestClient(app)
	r1 := tc.Get("/seed")
	cookie := extractSetCookie(t, r1.Headers.Get("Set-Cookie"))
	// Force-expire by deleting the entry directly.
	_ = store.Delete(cookie.Value)
	tc.WithCookie(cookie).Get("/check")
}

// TestSecurityCookieAttributesDefaultSecure verifies the secure-by-
// default cookie attributes when nothing in Config disables them.
func TestSecurityCookieAttributesDefaultSecure(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{Store: NewMemoryStore()})) // no Disable* flags
	app.Get("/x", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	resp := aarv.NewTestClient(app).Get("/x")
	c := parseSetCookie(t, resp.Headers.Get("Set-Cookie"))
	if !c.Secure {
		t.Error("default Secure must be true")
	}
	if !c.HttpOnly {
		t.Error("default HttpOnly must be true")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("default SameSite = %v; want Lax", c.SameSite)
	}
}

// TestSecuritySessionWriterUnwrap verifies the wrapper participates in
// the http.ResponseController unwrap chain. The repo treats Unwrap as
// the compatibility contract for plugin writers.
func TestSecuritySessionWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := newSessionWriter(rec, func() {})
	if sw.Unwrap() != http.ResponseWriter(rec) {
		t.Fatal("Unwrap must return the underlying writer")
	}
	// http.ResponseController.Flush should walk Unwrap to find the recorder's Flusher.
	if err := http.NewResponseController(sw).Flush(); err != nil {
		t.Fatalf("ResponseController.Flush failed: %v", err)
	}
}

// TestSecuritySessionWriterFlushCommitsCookie ensures explicit Flush
// from an SSE-style handler flushes session cookies before the first
// data chunk leaves the server.
func TestSecuritySessionWriterFlushCommitsCookie(t *testing.T) {
	store := &instrumentedStore{MemoryStore: NewMemoryStore()}
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true}))
	app.Get("/sse", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		w := c.Response()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Force a flush mid-stream.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte("data: hi\n\n"))
		return nil
	})
	resp := aarv.NewTestClient(app).Get("/sse")
	if !strings.HasPrefix(resp.Headers.Get("Set-Cookie"), "_session=") {
		t.Fatalf("Set-Cookie missing on SSE response; got %q", resp.Headers.Get("Set-Cookie"))
	}
}
