package basicauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nilshah80/aarv"
)

type user struct{ name string }

func encode(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func newApp(t *testing.T, mw any) *aarv.App {
	t.Helper()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(mw)
	app.Get("/protected", func(c *aarv.Context) error {
		identity, ok := From(c)
		if !ok {
			t.Errorf("expected identity in context")
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "no identity"})
		}
		ctxIdentity, ok := FromContext(c.Context())
		if !ok || ctxIdentity != identity {
			t.Errorf("expected matching identity from context.Context, got %v ok=%v", ctxIdentity, ok)
		}
		return c.JSON(http.StatusOK, map[string]string{"who": identity.(*user).name})
	})
	return app
}

func validator(known map[string]string) Validator {
	return func(u, p string) (any, error) {
		if expected, ok := known[u]; ok && expected == p {
			return &user{name: u}, nil
		}
		return nil, errInvalidCreds
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Realm != "" {
		t.Fatalf("want empty default realm, got %q", cfg.Realm)
	}
	if cfg.Charset != "" {
		t.Fatalf("want empty default charset, got %q", cfg.Charset)
	}
	if cfg.ErrorMessage == "" {
		t.Fatal("want non-empty default error message")
	}
}

func TestNew_PanicsWithoutValidator(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when Validator is nil")
		}
	}()
	_ = New(Config{})
}

func TestNew_PanicsOnRealmWithQuote(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on realm containing '\"'")
		}
	}()
	_ = New(Config{Realm: `bad"realm`, Validator: func(string, string) (any, error) { return nil, nil }})
}

func TestNew_PanicsOnRealmWithBackslash(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on realm containing '\\'")
		}
	}()
	_ = New(Config{Realm: `bad\realm`, Validator: func(string, string) (any, error) { return nil, nil }})
}

func TestNew_PanicsOnRealmWithControlChar(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on realm containing CR/LF")
		}
	}()
	_ = New(Config{Realm: "x\nfoo", Validator: func(string, string) (any, error) { return nil, nil }})
}

func TestNew_PanicsOnInvalidCharset(t *testing.T) {
	cases := []string{"latin1", "ISO-8859-1", `bad"charset`, "utf 8"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("expected panic on charset %q", c)
				}
			}()
			_ = New(Config{Charset: c, Validator: func(string, string) (any, error) { return nil, nil }})
		})
	}
}

func TestNew_AcceptsCaseInsensitiveUTF8Charset(t *testing.T) {
	for _, c := range []string{"UTF-8", "utf-8", "Utf-8"} {
		t.Run(c, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("New must accept charset %q, got panic %v", c, r)
				}
			}()
			_ = New(Config{Charset: c, Validator: func(string, string) (any, error) { return nil, nil }})
		})
	}
}

func TestNew_ValidCredentials(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(map[string]string{"alice": "wonderland"})
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", encode("alice", "wonderland"))
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if h := rec.Header().Get("WWW-Authenticate"); h != "" {
		t.Fatalf("must not set WWW-Authenticate on success, got %q", h)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["who"] != "alice" {
		t.Fatalf("want alice, got %v", got)
	}
}

func TestNew_InvalidCredentials(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "test-realm"
	cfg.Validator = validator(map[string]string{"alice": "wonderland"})
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", encode("alice", "wrong"))
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if h := rec.Header().Get("WWW-Authenticate"); h != `Basic realm="test-realm"` {
		t.Fatalf("unexpected WWW-Authenticate: %q", h)
	}
}

func TestNew_MissingHeader(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "secure"
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if h := rec.Header().Get("WWW-Authenticate"); h != `Basic realm="secure"` {
		t.Fatalf("expected WWW-Authenticate challenge, got %q", h)
	}
}

func TestNew_WrongScheme(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer abc")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("expected WWW-Authenticate challenge on wrong scheme")
	}
}

func TestNew_SchemeCaseInsensitive(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(map[string]string{"a": "b"})
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "basic "+base64.StdEncoding.EncodeToString([]byte("a:b")))
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("lowercase scheme: want 200, got %d", rec.Code)
	}
}

func TestNew_MalformedBase64(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Basic !!!not-base64!!!")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestNew_NoColonInDecoded(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("noseparator")))
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestNew_PasswordContainsColon(t *testing.T) {
	cfg := DefaultConfig()
	gotUser, gotPass := "", ""
	cfg.Validator = func(u, p string) (any, error) {
		gotUser, gotPass = u, p
		return &user{name: u}, nil
	}
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", encode("alice", "pa:ss:word"))
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if gotUser != "alice" || gotPass != "pa:ss:word" {
		t.Fatalf("first-colon split failed: user=%q pass=%q", gotUser, gotPass)
	}
}

func TestNew_EmptyUserPassesToValidator(t *testing.T) {
	called := false
	cfg := DefaultConfig()
	cfg.Validator = func(u, p string) (any, error) {
		called = true
		if u != "" || p != "secret" {
			t.Errorf("unexpected creds u=%q p=%q", u, p)
		}
		return nil, errInvalidCreds
	}
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", encode("", "secret"))
	app.ServeHTTP(rec, req)

	if !called {
		t.Fatal("validator must be invoked even for empty username; the validator decides")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 from validator rejection, got %d", rec.Code)
	}
}

func TestNew_RealmAndCharsetInChallenge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "secure"
	cfg.Charset = "UTF-8"
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	want := `Basic realm="secure", charset="UTF-8"`
	if got := rec.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate: want %q got %q", want, got)
	}
}

func TestNew_NoRealmNoCharset(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	if got := rec.Header().Get("WWW-Authenticate"); got != "Basic" {
		t.Fatalf("want bare 'Basic' challenge, got %q", got)
	}
}

func TestNew_CharsetOnlyNoRealm(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Charset = "UTF-8"
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	want := `Basic charset="UTF-8"`
	if got := rec.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestNew_ValidatorAppError403_NoChallenge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "x"
	cfg.Validator = func(string, string) (any, error) {
		return nil, aarv.ErrForbidden("revoked")
	}
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", encode("a", "b"))
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("403 must not include WWW-Authenticate, got %q", got)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "revoked" {
		t.Fatalf("want message 'revoked', got %q", body.Message)
	}
}

func TestNew_ValidatorAppError401_HasChallenge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "x"
	cfg.Validator = func(string, string) (any, error) {
		return nil, aarv.ErrUnauthorized("session expired")
	}
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", encode("a", "b"))
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="x"` {
		t.Fatalf("401 from validator must include challenge, got %q", got)
	}
}

func TestNew_NilIdentityRejected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(string, string) (any, error) { return nil, nil }
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", encode("a", "b"))
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 when validator returns (nil, nil), got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("expected WWW-Authenticate on nil-identity rejection")
	}
}

func TestStaticCreds_Hit(t *testing.T) {
	v := StaticCreds(map[string]string{"alice": "pw1", "bob": "pw2"})
	got, err := v("alice", "pw1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "alice" {
		t.Fatalf("want alice, got %v", got)
	}
}

func TestStaticCreds_WrongPassword(t *testing.T) {
	v := StaticCreds(map[string]string{"alice": "pw1"})
	if _, err := v("alice", "wrong"); err == nil {
		t.Fatal("want error on wrong password")
	}
}

func TestStaticCreds_UnknownUser(t *testing.T) {
	v := StaticCreds(map[string]string{"alice": "pw1"})
	if _, err := v("eve", "pw1"); err == nil {
		t.Fatal("want error on unknown user")
	}
}

func TestStaticCreds_EmptyUser(t *testing.T) {
	v := StaticCreds(map[string]string{"": "pw"})
	if _, err := v("", "pw"); err == nil {
		t.Fatal("empty username must always fail")
	}
}

func TestStaticCreds_SnapshotIndependence(t *testing.T) {
	src := map[string]string{"u": "p"}
	v := StaticCreds(src)
	src["u"] = "changed"
	delete(src, "u")
	if _, err := v("u", "p"); err != nil {
		t.Fatalf("snapshot must survive caller mutation: %v", err)
	}
}

// TestStaticCreds_LengthVariations exercises the post-fix invariant: the
// helper hashes both stored and attempted passwords to fixed-length digests
// before comparing, so wildly mismatched lengths must reject without panic
// or false-positive. This is a behavioral proxy for the side-channel fix —
// it does not measure timing but ensures the code path runs without
// length-mismatch shortcuts.
func TestStaticCreds_LengthVariations(t *testing.T) {
	v := StaticCreds(map[string]string{"alice": "shortpw"})

	// Correct match.
	if _, err := v("alice", "shortpw"); err != nil {
		t.Fatalf("known user/correct pw must succeed: %v", err)
	}
	// Known user, very short attempted password.
	if _, err := v("alice", "x"); err == nil {
		t.Fatal("known user/wrong-short pw must fail")
	}
	// Known user, very long attempted password.
	long := make([]byte, 4096)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := v("alice", string(long)); err == nil {
		t.Fatal("known user/wrong-long pw must fail")
	}
	// Unknown user with realistic-length password.
	if _, err := v("eve", "anyrealisticpw"); err == nil {
		t.Fatal("unknown user must fail")
	}
	// Unknown user with empty password.
	if _, err := v("eve", ""); err == nil {
		t.Fatal("unknown user with empty pw must fail")
	}
}

// TestStaticCreds_SentinelDoesNotMatchEmptyPassword guards the choice of
// using the zero digest as a sentinel for unknown users: a real password
// must never produce the zero SHA-256 digest, so any valid password attempt
// against an unknown user must reject.
func TestStaticCreds_SentinelDoesNotMatchEmptyPassword(t *testing.T) {
	v := StaticCreds(map[string]string{}) // empty map → every user is unknown
	for _, pw := range []string{"", " ", "password", "longerpasswordvalue"} {
		if _, err := v("anyuser", pw); err == nil {
			t.Fatalf("unknown user with pw %q must fail", pw)
		}
	}
}

func TestFromContext_NilAndMissing(t *testing.T) {
	var nilCtx context.Context
	if _, ok := FromContext(nilCtx); ok {
		t.Fatal("FromContext(nil) must report not-ok")
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("FromContext on empty ctx must report not-ok")
	}
}

func TestFrom_NilContext(t *testing.T) {
	if _, ok := From(nil); ok {
		t.Fatal("From(nil) must report not-ok")
	}
}

func nonNativeMiddleware() aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func TestNew_StdlibPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "stdlib-test"
	cfg.Validator = validator(map[string]string{"u": "p"})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error {
		identity, ok := From(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "no identity"})
		}
		return c.JSON(http.StatusOK, map[string]string{"who": identity.(*user).name})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", encode("u", "p"))
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib-path success: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", encode("u", "wrong"))
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stdlib-path failure: want 401, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="stdlib-test"` {
		t.Fatalf("stdlib-path failure: missing/wrong challenge %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("stdlib-path failure: want JSON content-type, got %q", ct)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "unauthorized" || body.Message == "" {
		t.Fatalf("stdlib-path failure: unexpected body %+v", body)
	}
}

func TestNew_FillsDefaultErrorMessage(t *testing.T) {
	cfg := Config{
		Validator: validator(nil),
	}
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "missing or invalid credentials" {
		t.Fatalf("default error message must apply when Config.ErrorMessage is empty, got %q", body.Message)
	}
}

func TestNew_StdlibPath_MissingHeader(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "test"
	cfg.Validator = validator(nil)

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusOK) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stdlib-path missing header: want 401, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="test"` {
		t.Fatalf("stdlib-path missing header: want challenge, got %q", got)
	}
}

func TestNew_StdlibPath_ValidatorAppError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "x"
	cfg.Validator = func(string, string) (any, error) {
		return nil, aarv.ErrForbidden("revoked")
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", encode("a", "b"))
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("stdlib-path AppError: want 403, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("stdlib-path 403 must omit challenge, got %q", got)
	}
}

func TestNew_StdlibPath_NilIdentity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(string, string) (any, error) { return nil, nil }

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error {
		t.Errorf("handler must not run when validator returns nil identity")
		return nil
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", encode("a", "b"))
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stdlib-path nil identity: want 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("stdlib-path nil-identity rejection must include WWW-Authenticate")
	}
}

func TestNew_StdlibPath_NoAarvContext(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(u, _ string) (any, error) { return u, nil }

	var seenIdentity any
	var seenOK bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenIdentity, seenOK = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	// Mount the middleware on plain net/http — no aarv.App means
	// aarv.FromRequest(r) returns false, exercising the else branch.
	h := New(cfg).Stdlib(next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/anything", nil)
	req.Header.Set("Authorization", encode("alice", "pw"))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("plain net/http path: want 200, got %d", rec.Code)
	}
	if !seenOK || seenIdentity != "alice" {
		t.Fatalf("identity must reach handler via r.Context(); got %v ok=%v", seenIdentity, seenOK)
	}
}

func TestCodeForStatus(t *testing.T) {
	if got := codeForStatus(http.StatusUnauthorized); got != "unauthorized" {
		t.Fatalf("want unauthorized, got %q", got)
	}
	if got := codeForStatus(http.StatusForbidden); got != "forbidden" {
		t.Fatalf("want forbidden, got %q", got)
	}
	// Default branch.
	if got := codeForStatus(http.StatusInternalServerError); got != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("default branch must fall through to http.StatusText, got %q", got)
	}
}

func TestNew_StaticCredsEndToEnd(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "api"
	cfg.Validator = StaticCreds(map[string]string{"alice": "wonderland"})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/me", func(c *aarv.Context) error {
		identity, _ := From(c)
		return c.JSON(http.StatusOK, map[string]string{"user": identity.(string)})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set("Authorization", encode("alice", "wonderland"))
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("StaticCreds e2e: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["user"] != "alice" {
		t.Fatalf("want alice, got %v", got)
	}
}

func TestParseAuthHeader_LeadingWhitespace(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("u:p"))
	u, p, ok := parseAuthHeader("Basic   " + encoded)
	if !ok || u != "u" || p != "p" {
		t.Fatalf("want successful parse with leading whitespace; got u=%q p=%q ok=%v", u, p, ok)
	}
}

func TestParseAuthHeader_TooShort(t *testing.T) {
	if _, _, ok := parseAuthHeader("B"); ok {
		t.Fatal("must reject short header")
	}
	if _, _, ok := parseAuthHeader(""); ok {
		t.Fatal("must reject empty header")
	}
}
