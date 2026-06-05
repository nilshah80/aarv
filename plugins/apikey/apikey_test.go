package apikey

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nilshah80/aarv"
)

type client struct{ name string }

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
		c.Set("seen", identity)
		return c.JSON(http.StatusOK, map[string]string{"who": identity.(*client).name})
	})
	return app
}

func validator(known map[string]*client) Validator {
	return func(key string) (any, error) {
		if c, ok := known[key]; ok {
			return c, nil
		}
		return nil, errInvalidKey
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Header != "X-API-Key" {
		t.Fatalf("want default header X-API-Key, got %q", cfg.Header)
	}
	if cfg.Query != "" {
		t.Fatalf("want default query disabled, got %q", cfg.Query)
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

func TestNew_PanicsWithoutLookupSource(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when both Header and Query are empty")
		}
	}()
	_ = New(Config{
		Header:    "",
		Query:     "",
		Validator: func(string) (any, error) { return nil, nil },
	})
}

func TestNew_ValidKeyInHeader(t *testing.T) {
	known := map[string]*client{"good-key": {name: "alice"}}
	cfg := DefaultConfig()
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "good-key")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["who"] != "alice" {
		t.Fatalf("want alice, got %v", got)
	}
}

func TestNew_InvalidKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "nope")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "unauthorized" {
		t.Fatalf("want error=unauthorized, got %q", body.Error)
	}
}

func TestNew_MissingKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestNew_CustomHeader(t *testing.T) {
	known := map[string]*client{"k": {name: "svc"}}
	cfg := DefaultConfig()
	cfg.Header = "X-Custom-Token"
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-Custom-Token", "k")
	// Default header should not be honored.
	req.Header.Set("X-API-Key", "k")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 from custom header, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "k") // not the configured header
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 when only default header set, got %d", rec.Code)
	}
}

func TestNew_QueryFallback(t *testing.T) {
	known := map[string]*client{"qkey": {name: "via-query"}}
	cfg := DefaultConfig()
	cfg.Query = "api_key"
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected?api_key=qkey", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestNew_QueryDisabledByDefault(t *testing.T) {
	known := map[string]*client{"qkey": {name: "via-query"}}
	cfg := DefaultConfig()
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected?api_key=qkey", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 when query lookup disabled, got %d", rec.Code)
	}
}

func TestNew_HeaderTakesPrecedenceOverQuery(t *testing.T) {
	known := map[string]*client{"good": {name: "ok"}}
	cfg := DefaultConfig()
	cfg.Query = "api_key"
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected?api_key=bad", nil)
	req.Header.Set("X-API-Key", "good")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 when header is valid, got %d", rec.Code)
	}
}

func TestNew_ValidatorReturnsAppError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(string) (any, error) {
		return nil, aarv.ErrForbidden("revoked")
	}
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "anything")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "revoked" {
		t.Fatalf("want message 'revoked', got %q", body.Message)
	}
}

func TestNew_NilIdentityRejected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(string) (any, error) { return nil, nil }
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "anything")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 when validator returns (nil, nil), got %d", rec.Code)
	}
}

func TestNew_NilIdentityRejectedStdlibPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(string) (any, error) { return nil, nil }

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error {
		t.Errorf("handler must not run when validator returns nil identity")
		return nil
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("X-API-Key", "anything")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 on stdlib path when validator returns (nil, nil), got %d", rec.Code)
	}
}

func TestStaticKeys_Hit(t *testing.T) {
	v := StaticKeys(map[string]any{"abc": "client-a", "xyz": "client-b"})
	got, err := v("abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "client-a" {
		t.Fatalf("want client-a, got %v", got)
	}
}

func TestStaticKeys_Miss(t *testing.T) {
	v := StaticKeys(map[string]any{"abc": "client-a"})
	if _, err := v("nope"); err == nil {
		t.Fatal("want error on miss")
	}
}

func TestStaticKeys_EmptyKey(t *testing.T) {
	v := StaticKeys(map[string]any{"": "should-not-match", "good": "ok"})
	if _, err := v(""); err == nil {
		t.Fatal("empty presented key must always fail")
	}
}

func TestStaticKeys_LengthVariations(t *testing.T) {
	// Stored keys span a wide range of lengths. After the SHA-256 fix, all
	// comparisons go through fixed-length 32-byte digests, so length
	// variation must not affect correctness.
	v := StaticKeys(map[string]any{
		"k":          "short",
		"medium-key": "med",
		"a-much-longer-api-key-value-with-many-bytes": "long",
	})

	for _, tc := range []struct{ key, want string }{
		{"k", "short"},
		{"medium-key", "med"},
		{"a-much-longer-api-key-value-with-many-bytes", "long"},
	} {
		got, err := v(tc.key)
		if err != nil {
			t.Errorf("key %q: unexpected error %v", tc.key, err)
			continue
		}
		if got != tc.want {
			t.Errorf("key %q: want %q, got %v", tc.key, tc.want, got)
		}
	}

	// Wildly mismatched lengths must reject without crashing or matching.
	if _, err := v(""); err == nil {
		t.Error("empty key must fail")
	}
	long := make([]byte, 4096)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := v(string(long)); err == nil {
		t.Error("oversized unknown key must fail")
	}
}

func TestStaticKeys_SnapshotIndependence(t *testing.T) {
	src := map[string]any{"k": "original"}
	v := StaticKeys(src)
	src["k"] = "mutated"
	delete(src, "k")
	got, err := v("k")
	if err != nil {
		t.Fatalf("snapshot must survive caller mutation: %v", err)
	}
	if got != "original" {
		t.Fatalf("want original, got %v", got)
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

// nonNativeMiddleware forces fallback to the stdlib middleware path by
// wrapping next with no native registration. When apikey runs after this
// wrapper, the framework cannot build a fully-native chain and uses the
// stdlib http.Handler form of apikey instead.
func nonNativeMiddleware() aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func TestNew_StdlibPath(t *testing.T) {
	known := map[string]*client{"k": {name: "stdlib-client"}}
	cfg := DefaultConfig()
	cfg.Validator = validator(known)

	app := aarv.New(aarv.WithBanner(false))
	// nonNativeMiddleware first guarantees the chain has a non-native link,
	// pushing apikey through its stdlib http.Handler implementation.
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error {
		identity, ok := From(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "no identity"})
		}
		return c.JSON(http.StatusOK, map[string]string{"who": identity.(*client).name})
	})

	// success
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("X-API-Key", "k")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib-path success: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["who"] != "stdlib-client" {
		t.Fatalf("stdlib-path success: want who=stdlib-client, got %v", got)
	}

	// failure
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("X-API-Key", "wrong")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stdlib-path failure: want 401, got %d", rec.Code)
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
		Header:    "X-API-Key",
		Validator: validator(nil),
	}
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "bad")
	app.ServeHTTP(rec, req)

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "missing or invalid API key" {
		t.Fatalf("default error message must be applied when Config.ErrorMessage is empty, got %q", body.Message)
	}
}

func TestNew_StdlibPath_MissingKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusOK) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stdlib-path missing key: want 401, got %d", rec.Code)
	}
}

func TestNew_StdlibPath_ValidatorAppError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(string) (any, error) {
		return nil, aarv.ErrForbidden("revoked")
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("X-API-Key", "anything")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("stdlib-path AppError: want 403, got %d", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "revoked" {
		t.Fatalf("want message 'revoked', got %q", body.Message)
	}
}

func TestNew_StdlibPath_NoAarvContext(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(key string) (any, error) {
		if key == "good" {
			return "client-x", nil
		}
		return nil, errInvalidKey
	}

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
	req.Header.Set("X-API-Key", "good")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("plain net/http path: want 200, got %d", rec.Code)
	}
	if !seenOK || seenIdentity != "client-x" {
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
	// Default branch: any other status falls through to http.StatusText.
	if got := codeForStatus(http.StatusInternalServerError); got != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("default branch must fall through to http.StatusText, got %q", got)
	}
}

func TestNew_HeaderOnlyExplicitlyDisabled(t *testing.T) {
	known := map[string]*client{"q": {name: "qonly"}}
	cfg := DefaultConfig()
	cfg.Header = ""
	cfg.Query = "k"
	cfg.Validator = validator(known)

	app := newApp(t, New(cfg))

	// Header set should not be consulted.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "q")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("header lookup should be disabled, got %d", rec.Code)
	}

	// Query should work.
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected?k=q", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("query-only lookup: want 200, got %d", rec.Code)
	}
}
