package bearer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nilshah80/aarv"
)

type user struct{ name string }

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

func validator(known map[string]*user) Validator {
	return func(token string) (any, error) {
		if u, ok := known[token]; ok {
			return u, nil
		}
		return nil, errInvalidToken
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Header != "Authorization" {
		t.Fatalf("want default header Authorization, got %q", cfg.Header)
	}
	if cfg.Query != "" {
		t.Fatalf("want default query disabled, got %q", cfg.Query)
	}
	if cfg.Realm != "" {
		t.Fatalf("want empty default realm, got %q", cfg.Realm)
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
	_ = New(Config{Header: "Authorization"})
}

func TestNew_PanicsWithoutLookupSource(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when both Header and Query are empty")
		}
	}()
	_ = New(Config{
		Validator: func(string) (any, error) { return nil, nil },
	})
}

func TestNew_PanicsOnRealmWithQuote(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on realm containing '\"'")
		}
	}()
	_ = New(Config{
		Header:    "Authorization",
		Realm:     `bad"realm`,
		Validator: func(string) (any, error) { return nil, nil },
	})
}

func TestNew_PanicsOnRealmWithBackslash(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on realm containing '\\'")
		}
	}()
	_ = New(Config{
		Header:    "Authorization",
		Realm:     `bad\realm`,
		Validator: func(string) (any, error) { return nil, nil },
	})
}

func TestNew_PanicsOnRealmWithControlChar(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on realm containing CR/LF")
		}
	}()
	_ = New(Config{
		Header:    "Authorization",
		Realm:     "x\nfoo",
		Validator: func(string) (any, error) { return nil, nil },
	})
}

func TestNew_ValidTokenInHeader(t *testing.T) {
	known := map[string]*user{"good-token": {name: "alice"}}
	cfg := DefaultConfig()
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer good-token")
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

func TestNew_InvalidToken(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer nope")
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

func TestNew_MissingAuthHeader(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("want challenge 'Bearer', got %q", got)
	}
}

func TestNew_NonBearerSchemeRejected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(string) (any, error) {
		t.Error("validator must not be invoked for non-Bearer scheme")
		return nil, nil
	}
	app := newApp(t, New(cfg))

	for _, header := range []string{
		"Basic dXNlcjpwYXNz",
		"Token xxx",
		"BearerNoSpace", // missing separator
		"Bearer",        // scheme only, no token
		"Bearer ",       // scheme + space, no token
		"Bearer\tabc",   // tab is not the required separator
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/protected", nil)
		req.Header.Set("Authorization", header)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("header %q: want 401, got %d", header, rec.Code)
		}
	}
}

func TestNew_SchemeCaseInsensitive(t *testing.T) {
	known := map[string]*user{"tok": {name: "u"}}
	cfg := DefaultConfig()
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	for _, scheme := range []string{"Bearer", "bearer", "BEARER", "BeArEr"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/protected", nil)
		req.Header.Set("Authorization", scheme+" tok")
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("scheme %q: want 200, got %d", scheme, rec.Code)
		}
	}
}

func TestNew_ToleratesDoubleSpace(t *testing.T) {
	known := map[string]*user{"tok": {name: "u"}}
	cfg := DefaultConfig()
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer  tok")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 with double-space separator, got %d", rec.Code)
	}
}

func TestNew_QueryFallback(t *testing.T) {
	known := map[string]*user{"qtoken": {name: "via-query"}}
	cfg := DefaultConfig()
	cfg.Query = "access_token"
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected?access_token=qtoken", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestNew_QueryDisabledByDefault(t *testing.T) {
	known := map[string]*user{"qtoken": {name: "via-query"}}
	cfg := DefaultConfig()
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected?access_token=qtoken", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 when query lookup disabled, got %d", rec.Code)
	}
}

func TestNew_HeaderTakesPrecedenceOverQuery(t *testing.T) {
	known := map[string]*user{"good": {name: "ok"}}
	cfg := DefaultConfig()
	cfg.Query = "access_token"
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected?access_token=bad", nil)
	req.Header.Set("Authorization", "Bearer good")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 when header is valid, got %d", rec.Code)
	}
}

func TestNew_NonBearerHeaderDoesNotFallThroughToQuery(t *testing.T) {
	// Header presence is exclusive: a non-empty Authorization header that is
	// not a valid Bearer token must NOT shortcut to the query lookup, even
	// when a valid query token is present. RFC 6750 §2 single-transport
	// model; also closes an audit-bypass where a malformed header is used
	// to sneak a query token past header-only logging.
	known := map[string]*user{"qtoken": {name: "via-query"}}
	cfg := DefaultConfig()
	cfg.Query = "access_token"
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	for _, header := range []string{
		"Basic ZGVhZDpiZWVm",
		"Bearer ", // scheme + separator, no token
		"Bearer",  // scheme only
		"Token abc",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/protected?access_token=qtoken", nil)
		req.Header.Set("Authorization", header)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("header %q + valid query: want 401, got %d", header, rec.Code)
		}
	}
}

func TestNew_AbsentHeaderFallsThroughToQuery(t *testing.T) {
	// When the configured header is absent (or empty string), the query
	// lookup IS consulted. Distinguishes "absent" from "present-but-bad".
	known := map[string]*user{"qtoken": {name: "via-query"}}
	cfg := DefaultConfig()
	cfg.Query = "access_token"
	cfg.Validator = validator(known)
	app := newApp(t, New(cfg))

	for _, name := range []string{"absent", "empty"} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/protected?access_token=qtoken", nil)
			if name == "empty" {
				req.Header.Set("Authorization", "")
			}
			app.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("want 200, got %d", rec.Code)
			}
		})
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
	req.Header.Set("Authorization", "Bearer anything")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate must be omitted on non-401, got %q", got)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "revoked" {
		t.Fatalf("want message 'revoked', got %q", body.Message)
	}
}

func TestNew_Validator401AppErrorIncludesChallenge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "api"
	cfg.Validator = func(string) (any, error) {
		return nil, aarv.ErrUnauthorized("token expired")
	}
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer expired")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="api"` {
		t.Fatalf("want challenge with realm, got %q", got)
	}
}

func TestNew_NilIdentityRejected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(string) (any, error) { return nil, nil }
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer anything")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 when validator returns (nil, nil), got %d", rec.Code)
	}
}

func TestNew_ChallengeIncludesRealm(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Realm = "myapi"
	cfg.Validator = validator(nil)
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="myapi"` {
		t.Fatalf(`want 'Bearer realm="myapi"', got %q`, got)
	}
}

func TestNew_FillsDefaultErrorMessage(t *testing.T) {
	cfg := Config{
		Header:    "Authorization",
		Validator: validator(nil),
	}
	app := newApp(t, New(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer bad")
	app.ServeHTTP(rec, req)

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "missing or invalid bearer token" {
		t.Fatalf("default error message must be applied when Config.ErrorMessage is empty, got %q", body.Message)
	}
}

func TestStaticTokens_Hit(t *testing.T) {
	v := StaticTokens(map[string]any{"abc": "client-a", "xyz": "client-b"})
	got, err := v("abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "client-a" {
		t.Fatalf("want client-a, got %v", got)
	}
}

func TestStaticTokens_Miss(t *testing.T) {
	v := StaticTokens(map[string]any{"abc": "client-a"})
	if _, err := v("nope"); err == nil {
		t.Fatal("want error on miss")
	}
}

func TestStaticTokens_EmptyToken(t *testing.T) {
	v := StaticTokens(map[string]any{"": "should-not-match", "good": "ok"})
	if _, err := v(""); err == nil {
		t.Fatal("empty presented token must always fail")
	}
}

func TestStaticTokens_LengthVariations(t *testing.T) {
	v := StaticTokens(map[string]any{
		"k":          "short",
		"medium-tok": "med",
		"a-much-longer-bearer-token-value-with-many-bytes": "long",
	})

	for _, tc := range []struct{ token, want string }{
		{"k", "short"},
		{"medium-tok", "med"},
		{"a-much-longer-bearer-token-value-with-many-bytes", "long"},
	} {
		got, err := v(tc.token)
		if err != nil {
			t.Errorf("token %q: unexpected error %v", tc.token, err)
			continue
		}
		if got != tc.want {
			t.Errorf("token %q: want %q, got %v", tc.token, tc.want, got)
		}
	}

	if _, err := v(""); err == nil {
		t.Error("empty token must fail")
	}
	long := make([]byte, 4096)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := v(string(long)); err == nil {
		t.Error("oversized unknown token must fail")
	}
}

func TestStaticTokens_SnapshotIndependence(t *testing.T) {
	src := map[string]any{"k": "original"}
	v := StaticTokens(src)
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

func TestBuildChallenge(t *testing.T) {
	if got := buildChallenge(""); got != "Bearer" {
		t.Fatalf("empty realm: want 'Bearer', got %q", got)
	}
	if got := buildChallenge("api"); got != `Bearer realm="api"` {
		t.Fatalf(`realm: want 'Bearer realm="api"', got %q`, got)
	}
}

// TestStdlibPath_PreservesUpstreamRequestMutations covers the case where
// an upstream stdlib middleware does more than r.WithContext: it clones
// r, rewrites the URL.Path, injects a header, and forwards the clone.
// bearer must hand the downstream chain a request whose URL/Headers
// reflect those mutations rather than discarding them in favor of the
// pre-mutation request bound to *aarv.Context. (*Context).BindRequest
// is the contract that makes both sides consistent; an older
// SetContextValue-then-RawRequest sequence would silently fall back to
// the framework's original c.req and erase the upstream changes.
func TestStdlibPath_PreservesUpstreamRequestMutations(t *testing.T) {
	known := map[string]*user{"good": {name: "alice"}}
	cfg := DefaultConfig()
	cfg.Validator = validator(known)

	upstreamMutate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2 := r.Clone(r.Context())
			r2.Header.Set("X-Upstream", "yes")
			r2.URL.Path = "/p"
			next.ServeHTTP(w, r2)
		})
	}

	downstreamProbe := func(t *testing.T) aarv.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("X-Upstream"); got != "yes" {
					t.Errorf("downstream r lost upstream header X-Upstream; got %q", got)
				}
				if r.URL.Path != "/p" {
					t.Errorf("downstream r lost upstream URL.Path rewrite; got %q", r.URL.Path)
				}
				if c, ok := aarv.FromRequest(r); ok {
					if c.Header("X-Upstream") != "yes" {
						t.Errorf("c.Header lost upstream injection; got %q", c.Header("X-Upstream"))
					}
					if c.Path() != "/p" {
						t.Errorf("c.Path lost upstream rewrite; got %q", c.Path())
					}
				} else {
					t.Errorf("downstream lost *aarv.Context")
				}
				next.ServeHTTP(w, r)
			})
		}
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(upstreamMutate, nonNativeMiddleware(), New(cfg), downstreamProbe(t))
	app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/original-path-replaced-by-upstream", nil)
	req.Header.Set("Authorization", "Bearer good")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestNew_StdlibPath_AppErrorByteIdenticalAcrossPaths_DefaultPipeline
// ensures the stdlib path emits a validator-returned *aarv.AppError with
// truly byte-identical status, response body, and security-relevant
// headers compared to the native path — when the framework uses its
// default response pipeline (default ErrorHandler, default JSON codec /
// Content-Type, no response-mutating OnError). The fields decoded on
// the wire (Error/Code/Detail) are the CHANGELOG'd contract; this test
// enforces that contract at the byte level so future drift in either
// branch (e.g. a stray space, a header re-ordering, an extra trailing
// newline) surfaces immediately rather than at the next manual diff.
//
// With any of WithErrorHandler, WithCodec, or a response-mutating
// OnError installed, parity does NOT hold: the native path flows
// through the framework's pipeline while the stdlib path writes JSON
// directly. Plugins/rbac/rbac_test.go's
// TestCustomErrorHandler_DivergesAcrossPaths documents the same
// limitation for rbac.
func TestNew_StdlibPath_AppErrorByteIdenticalAcrossPaths_DefaultPipeline(t *testing.T) {
	build := func() Config {
		return Config{
			Header: "Authorization",
			Realm:  "api",
			Validator: func(string) (any, error) {
				return nil, aarv.NewError(http.StatusUnauthorized, "token_expired", "token expired").
					WithDetail("token issued before key rotation")
			},
			ErrorMessage: "should not be used because validator returns AppError",
		}
	}

	type response struct {
		status      int
		contentType string
		wwwAuth     string
		body        string
	}

	doRequest := func(setup func(app *aarv.App)) response {
		t.Helper()
		app := aarv.New(aarv.WithBanner(false))
		setup(app)
		app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusOK) })

		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/p", nil)
		req.Header.Set("Authorization", "Bearer expired")
		app.ServeHTTP(rec, req)

		return response{
			status:      rec.Code,
			contentType: rec.Header().Get("Content-Type"),
			wwwAuth:     rec.Header().Get("WWW-Authenticate"),
			body:        rec.Body.String(),
		}
	}

	native := doRequest(func(app *aarv.App) { app.Use(New(build())) })
	stdlib := doRequest(func(app *aarv.App) { app.Use(nonNativeMiddleware(), New(build())) })

	// Field-level assertions on the native response. These also document
	// what "the contract" is, so a diff on this test reads cleanly.
	if native.status != http.StatusUnauthorized {
		t.Fatalf("native: want 401, got %d body=%s", native.status, native.body)
	}
	if native.contentType != "application/json" {
		t.Errorf("native: Content-Type=%q, want application/json", native.contentType)
	}
	if native.wwwAuth != `Bearer realm="api"` {
		t.Errorf(`native: WWW-Authenticate=%q, want 'Bearer realm="api"'`, native.wwwAuth)
	}
	var nativeBody errorBody
	if err := json.Unmarshal([]byte(native.body), &nativeBody); err != nil {
		t.Fatalf("native body unmarshal: %v", err)
	}
	if nativeBody.Error != "token_expired" || nativeBody.Message != "token expired" ||
		nativeBody.Detail != "token issued before key rotation" {
		t.Errorf("native body decoded fields wrong: %+v", nativeBody)
	}

	// Byte-level parity: status, Content-Type, WWW-Authenticate, and the
	// raw response body must all match.
	if native.status != stdlib.status {
		t.Errorf("status diff: native=%d stdlib=%d", native.status, stdlib.status)
	}
	if native.contentType != stdlib.contentType {
		t.Errorf("Content-Type diff: native=%q stdlib=%q", native.contentType, stdlib.contentType)
	}
	if native.wwwAuth != stdlib.wwwAuth {
		t.Errorf("WWW-Authenticate diff: native=%q stdlib=%q", native.wwwAuth, stdlib.wwwAuth)
	}
	if native.body != stdlib.body {
		t.Errorf("response body bytes differ:\nnative=%q\nstdlib=%q", native.body, stdlib.body)
	}
}

// nonNativeMiddleware forces fallback to the stdlib middleware path by
// wrapping next with no native registration. When bearer runs after this
// wrapper, the framework cannot build a fully-native chain and uses the
// stdlib http.Handler form of bearer instead.
func nonNativeMiddleware() aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func TestNew_StdlibPath(t *testing.T) {
	known := map[string]*user{"k": {name: "stdlib-user"}}
	cfg := DefaultConfig()
	cfg.Validator = validator(known)

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error {
		identity, ok := From(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "no identity"})
		}
		return c.JSON(http.StatusOK, map[string]string{"who": identity.(*user).name})
	})

	// success
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", "Bearer k")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib-path success: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["who"] != "stdlib-user" {
		t.Fatalf("stdlib-path success: want who=stdlib-user, got %v", got)
	}

	// failure
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stdlib-path failure: want 401, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("stdlib-path failure: want JSON content-type, got %q", ct)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("stdlib-path failure: want WWW-Authenticate, got %q", got)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "unauthorized" || body.Message == "" {
		t.Fatalf("stdlib-path failure: unexpected body %+v", body)
	}
}

func TestNew_StdlibPath_MissingHeader(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = validator(nil)

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), New(cfg))
	app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusOK) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stdlib-path missing header: want 401, got %d", rec.Code)
	}
}

func TestNew_StdlibPath_NilIdentityRejected(t *testing.T) {
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
	req.Header.Set("Authorization", "Bearer anything")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 on stdlib path when validator returns (nil, nil), got %d", rec.Code)
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
	req.Header.Set("Authorization", "Bearer anything")
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("stdlib-path AppError: want 403, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate must be omitted on non-401, got %q", got)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "revoked" {
		t.Fatalf("want message 'revoked', got %q", body.Message)
	}
}

func TestNew_StdlibPath_NoAarvContext_FailureOmitsRequestID(t *testing.T) {
	// Plain net/http mounting (no aarv.App) means aarv.FromRequest returns
	// false, exercising the requestID() fallback branch in writeUnauthorized.
	cfg := DefaultConfig()
	cfg.Validator = func(string) (any, error) { return nil, errInvalidToken }

	h := New(cfg).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("handler must not run on auth failure")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/anything", nil)
	req.Header.Set("Authorization", "Bearer bad")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RequestID != "" {
		t.Fatalf("request_id must be empty without an aarv.Context, got %q", body.RequestID)
	}
}

func TestNew_StdlibPath_NoAarvContext(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validator = func(token string) (any, error) {
		if token == "good" {
			return "client-x", nil
		}
		return nil, errInvalidToken
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
	req.Header.Set("Authorization", "Bearer good")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("plain net/http path: want 200, got %d", rec.Code)
	}
	if !seenOK || seenIdentity != "client-x" {
		t.Fatalf("identity must reach handler via r.Context(); got %v ok=%v", seenIdentity, seenOK)
	}
}

func TestNew_HeaderOnlyExplicitlyDisabled(t *testing.T) {
	known := map[string]*user{"q": {name: "qonly"}}
	cfg := DefaultConfig()
	cfg.Header = ""
	cfg.Query = "access_token"
	cfg.Validator = validator(known)

	app := newApp(t, New(cfg))

	// Bearer header set should not be consulted.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer q")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("header lookup should be disabled, got %d", rec.Code)
	}

	// Query should work.
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected?access_token=q", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("query-only lookup: want 200, got %d", rec.Code)
	}
}

func TestParseAuthHeader_TableDriven(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"too short", "Bear", ""},
		{"wrong scheme", "Token abc", ""},
		{"no separator", "Bearerabc", ""},
		{"just scheme", "Bearer", ""},
		{"scheme + space, no token", "Bearer ", ""},
		{"valid", "Bearer abc", "abc"},
		{"valid lowercase", "bearer abc", "abc"},
		{"valid mixed case", "BeArEr abc", "abc"},
		{"two-space tolerance", "Bearer  abc", "abc"},
		{"three-space rejected as part of token", "Bearer   abc", " abc"},
		{"tab not accepted as separator", "Bearer\tabc", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseAuthHeader(tc.in); got != tc.want {
				t.Errorf("parseAuthHeader(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
