// Tests landed in response to a security review covering: session
// fixation via forged cookie value, Destroy not expiring the client
// cookie when Store.Delete fails, IDLength config being unused, and
// sessionWriter only forwarding Flush.
package session

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// --- Finding 1: session fixation via forged cookie value ---

// TestNoFixationOnLoginAfterPlantedCookie is the end-to-end version of
// the attack: attacker plants `_session=known-id`, victim hits a login
// route that calls Set + (no Regenerate). The save MUST land under a
// fresh server-minted ID, not under the attacker's planted one.
func TestNoFixationOnLoginAfterPlantedCookie(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true}))
	app.Post("/login", func(c *aarv.Context) error {
		MustFrom(c).Set("user", "victim")
		return c.JSON(http.StatusOK, "ok")
	})

	const planted = "attacker-planted-id"
	tc := aarv.NewTestClient(app)
	resp := tc.WithCookie(&http.Cookie{Name: "_session", Value: planted}).Post("/login", nil)

	// The Save MUST NOT land under the attacker-planted ID.
	if got, _ := store.Get(planted); got != nil {
		t.Fatal("session-fixation: login persisted under attacker-supplied id")
	}

	// And the response cookie must carry the freshly-minted ID.
	c := parseSetCookie(t, resp.Headers.Get("Set-Cookie"))
	if c.Value == planted {
		t.Fatal("session-fixation: response Set-Cookie echoed planted id")
	}
	if c.Value == "" {
		t.Fatal("expected fresh session id in response cookie")
	}
	if got, _ := store.Get(c.Value); got == nil {
		t.Fatal("freshly-minted id should be persisted in store")
	}
}

// --- Finding 2: Destroy must expire client cookie even when Delete fails ---

// TestDestroyExpiresCookieEvenWhenDeleteFails proves that a logout
// always returns Set-Cookie MaxAge=-1 to the client, so a flaky
// backend cannot leave the browser holding a still-valid cookie.
func TestDestroyExpiresCookieEvenWhenDeleteFails(t *testing.T) {
	store := &deleteFailingStore{MemoryStore: NewMemoryStore()}
	saveErrCalls := 0
	app := aarv.New()
	app.Use(New(Config{
		Store:            store,
		DisableSecure:    true,
		SaveErrorHandler: func(c *aarv.Context, err error) { saveErrCalls++ },
	}))
	app.Get("/seed", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	app.Post("/logout", func(c *aarv.Context) error {
		MustFrom(c).Destroy()
		return c.JSON(http.StatusOK, "bye")
	})
	tc := aarv.NewTestClient(app)
	cookie := extractSetCookie(t, tc.Get("/seed").Headers.Get("Set-Cookie"))

	store.failNextDelete.Store(true)
	resp := tc.WithCookie(cookie).Post("/logout", nil)

	if resp.Status != http.StatusOK {
		t.Fatalf("logout response status = %d; want 200", resp.Status)
	}
	expired := parseSetCookie(t, resp.Headers.Get("Set-Cookie"))
	if expired.MaxAge != -1 {
		t.Fatalf("logout cookie MaxAge = %d; want -1 even though Store.Delete failed", expired.MaxAge)
	}
	if saveErrCalls != 1 {
		t.Fatalf("SaveErrorHandler must be called for the Delete failure; calls = %d", saveErrCalls)
	}
}

// --- Finding 3: IDLength is honored across new and Regenerate paths ---

// TestIDLengthHonoredOnFreshSession verifies that the configured raw
// byte length is what generateID actually uses. Picks a non-default
// value above MinIDLength (24 bytes → 32-char base64url) so the
// assertion can't accidentally match defaultIDLen output.
func TestIDLengthHonoredOnFreshSession(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true, IDLength: 24}))
	var seenID string
	app.Get("/x", func(c *aarv.Context) error {
		s := MustFrom(c)
		s.Set("k", "v")
		seenID = s.ID()
		return c.JSON(http.StatusOK, "ok")
	})
	aarv.NewTestClient(app).Get("/x")
	// 24 raw bytes → 32 base64url characters (no padding).
	if want := 32; len(seenID) != want {
		t.Fatalf("session id length = %d; want %d (IDLength=24)", len(seenID), want)
	}
}

// TestIDLengthHonoredOnRegenerate verifies that Regenerate produces an
// ID of the same length as the original (i.e. honors the cfg.IDLength
// stored on the Session at construction).
func TestIDLengthHonoredOnRegenerate(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true, IDLength: 24}))
	app.Post("/regen", func(c *aarv.Context) error {
		s := MustFrom(c)
		s.Set("k", "v")
		if err := s.Regenerate(); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, s.ID())
	})
	resp := aarv.NewTestClient(app).Post("/regen", nil)
	id := strings.Trim(strings.TrimSpace(resp.Text()), `"`)
	if want := 32; len(id) != want {
		t.Fatalf("regenerated id length = %d; want %d (IDLength=24)", len(id), want)
	}
}

// TestIDLengthHonoredOnCookieStoreFreshSession exercises the same path
// for the stateless backend.
func TestIDLengthHonoredOnCookieStoreFreshSession(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfg := normalizeCookieConfig(CookieConfig{Key: testCookieKey, IDLength: 24})
	s, err := cs.freshSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := 32; len(s.id) != want {
		t.Fatalf("cookie store fresh id length = %d; want %d", len(s.id), want)
	}
}

// --- Finding 4: Hijacker / Pusher conditional forwarding ---

// hijackableRecorder wraps httptest.ResponseRecorder and adds Hijack().
// A real Hijack would return a real conn; we return a stub error since
// the test only asserts the assertion-and-call path executes commit().
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, errors.New("test stub: not a real hijack")
}

func TestWrapWriterForwardsHijackerWhenUnderlyingSupports(t *testing.T) {
	rec := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	committed := false
	wrapped, sw := wrapWriter(rec, func() { committed = true })

	// Direct type-assertion must succeed because the underlying writer is a Hijacker.
	hj, ok := wrapped.(http.Hijacker)
	if !ok {
		t.Fatal("wrapped writer should expose http.Hijacker when underlying does")
	}
	_, _, _ = hj.Hijack()
	if !rec.hijacked {
		t.Fatal("Hijack call did not reach the underlying writer")
	}
	if !committed {
		t.Fatal("Hijack must commit pending session cookies before handing off the conn")
	}
	// sw is exposed for the middleware's post-handler commit; calling
	// twice must not invoke commitFn twice.
	sw.commit()
}

func TestWrapWriterDoesNotForgeHijackerWhenUnderlyingLacks(t *testing.T) {
	rec := httptest.NewRecorder() // does NOT implement http.Hijacker
	wrapped, _ := wrapWriter(rec, func() {})
	if _, ok := wrapped.(http.Hijacker); ok {
		t.Fatal("wrapper must not expose http.Hijacker when underlying writer lacks it")
	}
}

// pushableRecorder wraps httptest.ResponseRecorder with a stub Push.
type pushableRecorder struct {
	*httptest.ResponseRecorder
	pushed string
}

func (p *pushableRecorder) Push(target string, opts *http.PushOptions) error {
	p.pushed = target
	return nil
}

func TestWrapWriterForwardsPusherWhenUnderlyingSupports(t *testing.T) {
	rec := &pushableRecorder{ResponseRecorder: httptest.NewRecorder()}
	wrapped, _ := wrapWriter(rec, func() {})
	ps, ok := wrapped.(http.Pusher)
	if !ok {
		t.Fatal("wrapped writer should expose http.Pusher when underlying does")
	}
	if err := ps.Push("/static.css", nil); err != nil {
		t.Fatal(err)
	}
	if rec.pushed != "/static.css" {
		t.Fatalf("Push did not reach underlying writer; got %q", rec.pushed)
	}
}

func TestWrapWriterDoesNotForgePusherWhenUnderlyingLacks(t *testing.T) {
	rec := httptest.NewRecorder() // not a Pusher
	wrapped, _ := wrapWriter(rec, func() {})
	if _, ok := wrapped.(http.Pusher); ok {
		t.Fatal("wrapper must not expose http.Pusher when underlying writer lacks it")
	}
}

// hijackerPusherRecorder implements both interfaces — exercises the
// combined variant.
type hijackerPusherRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
	pushed   string
}

func (h *hijackerPusherRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, errors.New("stub")
}

func (h *hijackerPusherRecorder) Push(target string, _ *http.PushOptions) error {
	h.pushed = target
	return nil
}

func TestWrapWriterForwardsBothWhenUnderlyingSupportsBoth(t *testing.T) {
	rec := &hijackerPusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	wrapped, _ := wrapWriter(rec, func() {})
	if _, ok := wrapped.(http.Hijacker); !ok {
		t.Fatal("wrapped writer should expose Hijacker")
	}
	if _, ok := wrapped.(http.Pusher); !ok {
		t.Fatal("wrapped writer should expose Pusher")
	}
	_, _, _ = wrapped.(http.Hijacker).Hijack()
	_ = wrapped.(http.Pusher).Push("/x", nil)
	if !rec.hijacked || rec.pushed != "/x" {
		t.Fatalf("forwarding failed: hijacked=%v pushed=%q", rec.hijacked, rec.pushed)
	}
}

// TestWrapWriterPlainRecorderIsPlainWriter confirms the no-op variant.
func TestWrapWriterPlainRecorderIsPlainWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	wrapped, sw := wrapWriter(rec, func() {})
	// Plain wrapper IS the *sessionWriter directly — no extra struct.
	if _, ok := wrapped.(*sessionWriter); !ok {
		t.Fatal("plain underlying writer should yield a bare *sessionWriter")
	}
	if sw == nil {
		t.Fatal("sw must be non-nil")
	}
}

// --- Round 2: AEAD AAD binding to cookie name ---

// TestCookieStoreCrossNameReplayRejected proves a value sealed under
// cookie name A cannot be opened under cookie name B even with the
// same key — the AAD differs, so AES-GCM authentication fails. Without
// the cookie-name-bound AAD an attacker who learned a session value
// for one cookie name could replay it under a sibling cookie name on
// the same deployment.
func TestCookieStoreCrossNameReplayRejected(t *testing.T) {
	cs, _ := NewCookieStore(testCookieKey)
	cfgA := normalizeCookieConfig(CookieConfig{Key: testCookieKey, CookieName: "_session_a"})
	cfgB := normalizeCookieConfig(CookieConfig{Key: testCookieKey, CookieName: "_session_b"})

	encoded, err := cs.encode(cookieEnvelope{IssuedAt: time.Now().Unix(), Stored: &Stored{Data: map[string]any{"u": "alice"}}}, cfgA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.decode(encoded, cfgA); err != nil {
		t.Fatalf("self-decode under same name failed: %v", err)
	}
	if _, err := cs.decode(encoded, cfgB); err == nil {
		t.Fatal("cross-name replay accepted: AES-GCM should reject when AAD differs")
	}
}

// --- Round 2: Destroy after Regenerate clears both IDs ---

// TestDestroyAfterRegenerateDeletesBothIDs proves that a handler which
// regenerates the session AND destroys it in the same request leaves
// no live store entry under either the original or the regenerated id.
// The old test shape only deleted sess.id; the pre-regen oldID would
// otherwise survive until TTL.
func TestDestroyAfterRegenerateDeletesBothIDs(t *testing.T) {
	store := NewMemoryStore()
	app := aarv.New()
	app.Use(New(Config{Store: store, DisableSecure: true}))

	// Seed: one request that creates the session under id A.
	app.Get("/seed", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	// Single request that regenerates (id A → id B) and then destroys.
	app.Post("/regen-then-destroy", func(c *aarv.Context) error {
		s := MustFrom(c)
		s.Set("k", "v") // force dirty so a save would otherwise happen
		if err := s.Regenerate(); err != nil {
			return err
		}
		s.Destroy()
		return c.JSON(http.StatusOK, "bye")
	})

	tc := aarv.NewTestClient(app)
	cookie := extractSetCookie(t, tc.Get("/seed").Headers.Get("Set-Cookie"))
	originalID := cookie.Value

	tc.WithCookie(cookie).Post("/regen-then-destroy", nil)

	if got, _ := store.Get(originalID); got != nil {
		t.Fatal("destroy after regenerate must also delete the pre-regen id")
	}
	// Sweep all entries — there must be zero session rows for this user.
	store.mu.Lock()
	n := len(store.entries)
	store.mu.Unlock()
	if n != 0 {
		t.Fatalf("store still holds %d entries after destroy-after-regenerate; want 0", n)
	}
}

// TestDestroyAfterRegenerateStillExpiresCookie verifies the cookie is
// still expired even if both Delete calls fail underneath.
func TestDestroyAfterRegenerateStillExpiresCookie(t *testing.T) {
	store := &alwaysFailDeleteStore{MemoryStore: NewMemoryStore()}
	saveErrCalls := 0
	app := aarv.New()
	app.Use(New(Config{
		Store:            store,
		DisableSecure:    true,
		SaveErrorHandler: func(c *aarv.Context, err error) { saveErrCalls++ },
	}))
	app.Get("/seed", func(c *aarv.Context) error {
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})
	app.Post("/x", func(c *aarv.Context) error {
		s := MustFrom(c)
		s.Set("k", "v")
		_ = s.Regenerate()
		s.Destroy()
		return c.JSON(http.StatusOK, "bye")
	})
	tc := aarv.NewTestClient(app)

	// Seed via the always-fail store: failures during seed don't matter
	// because there's nothing to delete yet — we just need the client
	// cookie. The failure-tracking starts after seed.
	store.failOn = false
	cookie := extractSetCookie(t, tc.Get("/seed").Headers.Get("Set-Cookie"))
	store.failOn = true

	resp := tc.WithCookie(cookie).Post("/x", nil)
	expired := parseSetCookie(t, resp.Headers.Get("Set-Cookie"))
	if expired.MaxAge != -1 {
		t.Fatalf("cookie MaxAge = %d; want -1 even when both Delete calls fail", expired.MaxAge)
	}
	if saveErrCalls == 0 {
		t.Fatal("SaveErrorHandler must fire for the Delete failures")
	}
}

// alwaysFailDeleteStore returns an error from Delete when failOn is true.
type alwaysFailDeleteStore struct {
	*MemoryStore
	failOn bool
}

func (a *alwaysFailDeleteStore) Delete(id string) error {
	if a.failOn {
		return errors.New("delete-boom")
	}
	return a.MemoryStore.Delete(id)
}

// --- Round 2: stdlib path BindRequest sync ---

// TestStdlibPathSyncsContextWithUpstreamRewrite proves that an upstream
// stdlib middleware which mutates r (here: header injection via
// r.Clone) is observable to the session middleware's skipper and
// downstream handlers when the session middleware runs in the stdlib
// path. Without c.BindRequest(r), c.Header("X-Tenant") would return
// the pre-rewrite value (empty), and the skipper / handler would make
// decisions on stale state.
func TestStdlibPathSyncsContextWithUpstreamRewrite(t *testing.T) {
	store := NewMemoryStore()
	skipperSawHeader := ""
	handlerSawHeader := ""

	app := aarv.New()

	// Upstream mutator: sits BEFORE the session middleware in install order
	// but executes BEFORE it in the request chain, cloning r and adding a
	// header. Stdlib middleware (no native pair) so the whole chain falls
	// off the native fast path → exercises the session stdlib path.
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2 := r.Clone(r.Context())
			r2.Header.Set("X-Tenant", "acme")
			next.ServeHTTP(w, r2)
		})
	})
	app.Use(New(Config{
		Store:         store,
		DisableSecure: true,
		Skipper: func(c *aarv.Context) bool {
			skipperSawHeader = c.Header("X-Tenant")
			return false
		},
	}))

	app.Get("/x", func(c *aarv.Context) error {
		handlerSawHeader = c.Header("X-Tenant")
		MustFrom(c).Set("k", "v")
		return c.JSON(http.StatusOK, "ok")
	})

	aarv.NewTestClient(app).Get("/x")

	if skipperSawHeader != "acme" {
		t.Fatalf("skipper saw X-Tenant=%q; want %q (stdlib path failed to BindRequest)", skipperSawHeader, "acme")
	}
	if handlerSawHeader != "acme" {
		t.Fatalf("handler saw X-Tenant=%q; want %q", handlerSawHeader, "acme")
	}
}

// --- Round 2: IDLength validation ---

func TestNewPanicsOnFootgunIDLength(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("New must panic on IDLength in (0, MinIDLength)")
		}
	}()
	_ = New(Config{Store: NewMemoryStore(), IDLength: 1})
}

func TestNewCookiePanicsOnFootgunIDLength(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewCookie must panic on IDLength in (0, MinIDLength)")
		}
	}()
	_ = NewCookie(CookieConfig{Key: testCookieKey, IDLength: 8})
}

func TestNewAcceptsZeroAndAtLeastMinIDLength(t *testing.T) {
	// Zero (use default) must NOT panic.
	mustNotPanic(t, func() { _ = New(Config{Store: NewMemoryStore(), IDLength: 0}) })
	// Exactly MinIDLength must NOT panic.
	mustNotPanic(t, func() { _ = New(Config{Store: NewMemoryStore(), IDLength: MinIDLength}) })
	// Above default also fine.
	mustNotPanic(t, func() { _ = New(Config{Store: NewMemoryStore(), IDLength: 64}) })
}

// --- Round 3: load-error fallback routes through backend.freshSession ---

// trackingBackend wraps storeBackend so we can prove the load-error
// fallback goes through backend.freshSession (which carries backend-
// specific framing) rather than handcrafting a *Session directly.
// load() always returns (nil, error); freshSession() flags itself.
type trackingBackend struct {
	*storeBackend
	freshSessionCalls int
	loadErr           error
}

func (t *trackingBackend) load(c *aarv.Context, r *http.Request, cfg *normalized) (*Session, error) {
	return nil, t.loadErr
}

func (t *trackingBackend) freshSession(cfg *normalized) (*Session, error) {
	t.freshSessionCalls++
	return t.storeBackend.freshSession(cfg)
}

// TestLoadErrorFallbackUsesBackendFreshSession asserts the native-path
// fallback after ErrorHandler-returns-nil (or no ErrorHandler) calls
// backend.freshSession rather than the old hand-crafted
// generateID + newSession code path. This is what kept the IDLength
// honor-config bug latent for so long: backend-specific framing was
// being bypassed.
func TestLoadErrorFallbackUsesBackendFreshSession(t *testing.T) {
	tb := &trackingBackend{
		storeBackend: &storeBackend{store: NewMemoryStore()},
		loadErr:      errors.New("load-boom"),
	}
	cfg := normalizeConfig(Config{Store: tb.store, DisableSecure: true, IDLength: 24})
	mw := buildMiddleware(tb, cfg)

	app := aarv.New()
	app.Use(mw)
	app.Get("/x", func(c *aarv.Context) error {
		// Handler runs because the default load-error path logs and
		// proceeds with the freshly-minted session.
		return c.JSON(http.StatusOK, "ok")
	})
	resp := aarv.NewTestClient(app).Get("/x")
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d; want 200 (fallback proceeded)", resp.Status)
	}
	if tb.freshSessionCalls != 1 {
		t.Fatalf("backend.freshSession was called %d times; want 1", tb.freshSessionCalls)
	}
}

// TestLoadErrorFallbackStdlibPathUsesBackendFreshSession is the same
// assertion for the stdlib middleware path. A non-native sibling
// middleware forces the chain off the fast path.
func TestLoadErrorFallbackStdlibPathUsesBackendFreshSession(t *testing.T) {
	tb := &trackingBackend{
		storeBackend: &storeBackend{store: NewMemoryStore()},
		loadErr:      errors.New("load-boom"),
	}
	cfg := normalizeConfig(Config{Store: tb.store, DisableSecure: true, IDLength: 24})
	mw := buildMiddleware(tb, cfg)

	app := aarv.New()
	app.Use(mw)
	// Stdlib-only middleware, no native pair → fall back.
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	})
	app.Get("/x", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, "ok")
	})
	resp := aarv.NewTestClient(app).Get("/x")
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.Status)
	}
	if tb.freshSessionCalls != 1 {
		t.Fatalf("stdlib path: backend.freshSession was called %d times; want 1", tb.freshSessionCalls)
	}
}

func mustNotPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	fn()
}
