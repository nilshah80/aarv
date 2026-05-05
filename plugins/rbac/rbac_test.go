package rbac

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

// fixedRolesExtractor returns the same role slice on every call. Used by
// most tests; for stateful behavior the tests build their own closures.
func fixedRolesExtractor(roles ...string) RoleExtractor {
	return func(c *aarv.Context) []string { return roles }
}

func newApp(t *testing.T, mw aarv.Middleware) *aarv.App {
	t.Helper()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(mw)
	app.Get("/protected", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "true"})
	})
	return app
}

func TestNew_PanicsWithoutExtractor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when RoleExtractor is nil")
		}
	}()
	_ = New(Config{})
}

func TestNew_FillsDefaultErrorMessage(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor()})
	app := newApp(t, authz.RequireRoles("admin"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Message != "insufficient privileges" {
		t.Fatalf("default error message must be applied, got %q", body.Message)
	}
}

func TestRequireRoles_PanicsOnEmpty(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor()})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on RequireRoles() with no roles")
		}
	}()
	_ = authz.RequireRoles()
}

func TestRequireAnyRole_PanicsOnEmpty(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor()})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on RequireAnyRole() with no roles")
		}
	}()
	_ = authz.RequireAnyRole()
}

func TestRequireRoles_AllPresent(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor("admin", "editor", "viewer")})
	app := newApp(t, authz.RequireRoles("admin", "editor"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequireRoles_MissingOne(t *testing.T) {
	// has admin but missing editor → AND check fails
	authz := New(Config{RoleExtractor: fixedRolesExtractor("admin", "viewer")})
	app := newApp(t, authz.RequireRoles("admin", "editor"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 when one required role missing, got %d", rec.Code)
	}
}

func TestRequireRoles_NoneOfRequired(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor("viewer")})
	app := newApp(t, authz.RequireRoles("admin"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestRequireRoles_NilRoleSlice(t *testing.T) {
	// extractor returns nil (e.g. unauthenticated request) → 403
	authz := New(Config{RoleExtractor: func(c *aarv.Context) []string { return nil }})
	app := newApp(t, authz.RequireRoles("admin"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 on nil role slice, got %d", rec.Code)
	}
}

func TestRequireAnyRole_HasOne(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor("editor")})
	app := newApp(t, authz.RequireAnyRole("admin", "editor"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestRequireAnyRole_HasAll(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor("admin", "editor")})
	app := newApp(t, authz.RequireAnyRole("admin", "editor"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestRequireAnyRole_HasNone(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor("viewer", "guest")})
	app := newApp(t, authz.RequireAnyRole("admin", "editor"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestRequireAnyRole_EmptyRoleSlice(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor()})
	app := newApp(t, authz.RequireAnyRole("admin"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestForbidden_BodyShape(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor()})
	app := newApp(t, authz.RequireRoles("admin"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("want application/json, got %q", ct)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "forbidden" {
		t.Fatalf("want error=forbidden, got %q", body.Error)
	}
	if body.Message == "" {
		t.Fatal("want non-empty message")
	}
}

func TestExtractor_RunsPerRequest(t *testing.T) {
	// The extractor must be invoked on every request, not cached at
	// middleware construction. Use a counter to verify the call rate
	// matches the request rate.
	var calls int
	authz := New(Config{RoleExtractor: func(c *aarv.Context) []string {
		calls++
		return []string{"admin"}
	}})
	app := newApp(t, authz.RequireRoles("admin"))

	for i := range 5 {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i, rec.Code)
		}
	}
	if calls != 5 {
		t.Fatalf("extractor called %d times, want 5", calls)
	}
}

func TestExtractor_CaseSensitive(t *testing.T) {
	// "Admin" and "admin" must not match — the extractor returns canonical
	// names from the IdP and a case-insensitive policy would silently mask
	// drift between the two systems.
	authz := New(Config{RoleExtractor: fixedRolesExtractor("Admin")})
	app := newApp(t, authz.RequireRoles("admin"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 (case-sensitive), got %d", rec.Code)
	}
}

func TestSnapshot_Independent(t *testing.T) {
	// Mutating the role slice the caller passed to RequireRoles must not
	// change the policy at request time.
	roles := []string{"admin"}
	authz := New(Config{RoleExtractor: fixedRolesExtractor("admin")})
	mw := authz.RequireRoles(roles...)

	roles[0] = "viewer" // caller mutates after construction

	app := newApp(t, mw)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/protected", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("policy must be a snapshot of the original roles; got %d", rec.Code)
	}
}

func TestRequestID_PropagatedToBody(t *testing.T) {
	// When mounted in an aarv.App with the requestid plugin upstream, the
	// 403 response body should carry request_id so the rejection can be
	// correlated with logs. Here we set a request id manually via
	// c.Set("requestId", ...) using a tiny middleware.
	authz := New(Config{RoleExtractor: fixedRolesExtractor()})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c, ok := aarv.FromRequest(r); ok {
				c.Set("requestId", "abc-123")
				r = c.RawRequest()
			}
			next.ServeHTTP(w, r)
		})
	})
	app.Use(authz.RequireRoles("admin"))
	app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusOK) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RequestID != "abc-123" {
		t.Fatalf("request_id=%q, want abc-123", body.RequestID)
	}
}

// nonNativeMiddleware forces the framework to fall back to the stdlib
// http.Handler middleware path. When rbac runs after this wrapper, the chain
// cannot stay fully native and goes through rbac's stdlib branch.
func nonNativeMiddleware() aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func TestStdlibPath_Allow(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor("admin")})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), authz.RequireRoles("admin"))
	app.Get("/p", func(c *aarv.Context) error { return c.JSON(http.StatusOK, map[string]string{"ok": "1"}) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib path allow: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStdlibPath_Deny(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor("viewer")})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(nonNativeMiddleware(), authz.RequireRoles("admin"))
	app.Get("/p", func(c *aarv.Context) error {
		t.Errorf("handler must not run when denied")
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stdlib path deny: want 403, got %d", rec.Code)
	}
}

func TestStdlibPath_NoAarvContext_FailsClosed(t *testing.T) {
	// Mount the middleware on plain net/http (no aarv.App). The extractor
	// signature requires *aarv.Context; rbac fails closed with 403 rather
	// than admitting silently.
	authz := New(Config{RoleExtractor: func(c *aarv.Context) []string {
		t.Errorf("extractor must not run without an aarv.Context")
		return []string{"admin"}
	}})

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := authz.RequireRoles("admin")(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/anything", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 fail-closed, got %d", rec.Code)
	}
	if called {
		t.Fatal("handler must not run on fail-closed denial")
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RequestID != "" {
		t.Fatalf("request_id must be empty without an aarv.Context, got %q", body.RequestID)
	}
}

// rolesCtxKey is the key an upstream stdlib auth middleware would use to
// attach the caller's roles to r.Context(). The test below verifies that
// rbac's stdlib path picks the value up via c.Context() — proving the
// BindRequest sync after FromRequest works correctly for the canonical
// r.WithContext(...) pattern.
type rolesCtxKey struct{}

// TestStdlibPath_ReadsUpstreamWrappedRequestContext exercises the
// canonical Go pattern an upstream stdlib auth middleware would use:
// `next.ServeHTTP(w, r.WithContext(ctxWithRoles))`. Without
// (*Context).BindRequest after FromRequest, c.Context() would return the
// OLD context.Context (the one bound to c.req before the upstream
// wrapped r), and the extractor reading c.Context().Value(rolesCtxKey{})
// would silently fail closed.
func TestStdlibPath_ReadsUpstreamWrappedRequestContext(t *testing.T) {
	authz := New(Config{RoleExtractor: func(c *aarv.Context) []string {
		v, _ := c.Context().Value(rolesCtxKey{}).([]string)
		return v
	}})

	// Upstream stdlib auth middleware: attach roles to r.Context() in the
	// idiomatic Go way (r.WithContext + next.ServeHTTP). This is the
	// pattern any third-party stdlib middleware will use.
	upstreamAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), rolesCtxKey{}, []string{"admin"})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	app := aarv.New(aarv.WithBanner(false))
	// nonNativeMiddleware first forces rbac onto its stdlib path.
	app.Use(nonNativeMiddleware(), upstreamAuth, authz.RequireRoles("admin"))
	app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("stdlib path must read roles from upstream-wrapped r.Context(); got %d body=%s",
			rec.Code, rec.Body.String())
	}
}

// TestStdlibPath_PreservesUpstreamRequestMutations covers the case where
// an upstream stdlib middleware does more than r.WithContext: it
// rewrites a header and the URL.Path on a cloned request and then calls
// next.ServeHTTP with the clone. rbac must hand the downstream chain a
// request whose URL/Headers reflect those mutations AND whose
// *aarv.Context (recovered via FromRequest or read via c.Header / c.Path)
// agrees with what the downstream chain sees. (*Context).BindRequest is
// the contract that makes both sides consistent; an older
// SetContext-then-RawRequest sequence would silently discard the
// upstream URL/Header changes.
func TestStdlibPath_PreservesUpstreamRequestMutations(t *testing.T) {
	authz := New(Config{RoleExtractor: func(c *aarv.Context) []string {
		// Read a header the upstream injected. If the c-recoverable
		// view ever drifts from the downstream view, this would return
		// the stale value and the policy check would fail closed.
		if c.Header("X-Roles") == "admin,viewer" {
			return []string{"admin", "viewer"}
		}
		return nil
	}})

	// Upstream stdlib middleware: clone r, rewrite the path, inject a
	// header, and forward the clone. This is the kind of thing static-
	// prefix-strippers, header-rewriters, and version-selectors do.
	upstreamMutate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2 := r.Clone(r.Context())
			r2.Header.Set("X-Roles", "admin,viewer")
			r2.URL.Path = "/p" // rewrite to the registered route
			next.ServeHTTP(w, r2)
		})
	}

	// Downstream probe: must see both the header and the rewritten path
	// on the request rbac forwarded.
	downstreamProbe := func(t *testing.T) aarv.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("X-Roles"); got != "admin,viewer" {
					t.Errorf("downstream r lost upstream header X-Roles; got %q", got)
				}
				if r.URL.Path != "/p" {
					t.Errorf("downstream r lost upstream URL.Path rewrite; got %q", r.URL.Path)
				}
				if c, ok := aarv.FromRequest(r); ok {
					if c.Header("X-Roles") != "admin,viewer" {
						t.Errorf("c.Header lost upstream injection; got %q", c.Header("X-Roles"))
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
	app.Use(upstreamMutate, nonNativeMiddleware(), authz.RequireRoles("admin"), downstreamProbe(t))
	app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/original-path-replaced-by-upstream", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204 (extractor saw upstream-injected header), got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestStdlibPath_BridgeOff_AllowAndDenyStillSeeAarvContext exercises
// the regression that motivated forwarding the BindRequest-returned
// request rather than the upstream r unchanged. Under
// WithRequestContextBridge(false), BindRequest deletes the registry
// mapping for the previous request and re-binds c to a marker-attached
// request. If the stdlib branch then forwards the OLD r unchanged,
// downstream code (or, in the deny case, writeForbidden's own
// FromRequest call to populate request_id) cannot recover *aarv.Context
// — the request would 500 or silently drop the request_id.
//
// The test asserts both the allow and the deny paths still work and
// preserve the *aarv.Context discovery contract under bridge-off mode.
func TestStdlibPath_BridgeOff_AllowAndDenyStillSeeAarvContext(t *testing.T) {
	allowAuthz := New(Config{RoleExtractor: fixedRolesExtractor("admin")})
	denyAuthz := New(Config{RoleExtractor: fixedRolesExtractor("viewer")})

	// Helper: stdlib middleware that asserts FromRequest succeeds on the
	// request it sees. Placed AFTER rbac so the only way it can fail is
	// if rbac forwards a stale r whose registry entry has been deleted.
	downstreamProbe := func(t *testing.T, label string) aarv.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, ok := aarv.FromRequest(r); !ok {
					t.Errorf("%s: downstream middleware lost *aarv.Context — r was not synced after SetContext", label)
				}
				next.ServeHTTP(w, r)
			})
		}
	}

	t.Run("allow", func(t *testing.T) {
		app := aarv.New(aarv.WithBanner(false), aarv.WithRequestContextBridge(false))
		app.Use(nonNativeMiddleware(), allowAuthz.RequireRoles("admin"), downstreamProbe(t, "allow"))
		app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) })

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("allow path under bridge-off: want 204, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("deny_preserves_request_id", func(t *testing.T) {
		app := aarv.New(aarv.WithBanner(false), aarv.WithRequestContextBridge(false))
		// Tiny upstream middleware that stamps a request_id so the
		// 403 body should carry it back.
		stampRequestID := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c, ok := aarv.FromRequest(r); ok {
					c.Set("requestId", "req-bridge-off")
					r = c.RawRequest()
				}
				next.ServeHTTP(w, r)
			})
		}
		app.Use(stampRequestID, nonNativeMiddleware(), denyAuthz.RequireRoles("admin"))
		app.Get("/p", func(c *aarv.Context) error {
			t.Errorf("handler must not run when denied")
			return nil
		})

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("deny path under bridge-off: want 403, got %d", rec.Code)
		}
		var body errorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.RequestID != "req-bridge-off" {
			t.Fatalf("deny under bridge-off must preserve request_id; got %q", body.RequestID)
		}
	})
}

// TestNativeAndStdlib_ByteIdenticalDenial_DefaultPipeline gives the
// "byte-identical across paths" claim teeth: status, Content-Type, and
// the raw response body must match between the two middleware paths so
// a downstream proxy or audit log cannot tell which one ran.
//
// This parity holds ONLY when the framework uses its default response
// pipeline: default ErrorHandler, default JSON codec / Content-Type,
// and no response-mutating OnError. With any of WithErrorHandler,
// WithCodec, or a response-mutating OnError installed, the native path
// flows through the framework's pipeline while the stdlib path writes
// JSON directly, so the two diverge. See
// TestCustomErrorHandler_DivergesAcrossPaths for the machine-checked
// limitation.
func TestNativeAndStdlib_ByteIdenticalDenial_DefaultPipeline(t *testing.T) {
	build := func() *Authorizer {
		return New(Config{RoleExtractor: fixedRolesExtractor("viewer")})
	}

	type response struct {
		status      int
		contentType string
		body        string
	}
	doRequest := func(setup func(app *aarv.App)) response {
		t.Helper()
		app := aarv.New(aarv.WithBanner(false))
		setup(app)
		app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusOK) })

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
		return response{
			status:      rec.Code,
			contentType: rec.Header().Get("Content-Type"),
			body:        rec.Body.String(),
		}
	}

	native := doRequest(func(app *aarv.App) { app.Use(build().RequireRoles("admin")) })
	stdlib := doRequest(func(app *aarv.App) { app.Use(nonNativeMiddleware(), build().RequireRoles("admin")) })

	if native.status != http.StatusForbidden {
		t.Fatalf("native: want 403, got %d body=%s", native.status, native.body)
	}
	if native.contentType != "application/json" {
		t.Errorf("native: Content-Type=%q, want application/json", native.contentType)
	}

	if native.status != stdlib.status {
		t.Errorf("status diff: native=%d stdlib=%d", native.status, stdlib.status)
	}
	if native.contentType != stdlib.contentType {
		t.Errorf("Content-Type diff: native=%q stdlib=%q", native.contentType, stdlib.contentType)
	}
	if native.body != stdlib.body {
		t.Errorf("response body bytes differ:\nnative=%q\nstdlib=%q", native.body, stdlib.body)
	}
}

// TestCustomErrorHandler_DivergesAcrossPaths documents the (intentional)
// limitation of the byte-parity claim: when the framework is configured
// with a custom WithErrorHandler, the native path goes through that
// handler (because the middleware returns aarv.ErrForbidden, which the
// framework dispatches via its ErrorHandler), while the stdlib path
// writes its own JSON shape directly. The two responses therefore
// diverge.
//
// This test is here to lock in the documented behavior: anyone planning
// to depend on cross-path parity needs to either stay on the default
// ErrorHandler or write their own native + stdlib symmetric handling.
// If the framework grows a way for plugins to thread a custom-handler
// hook into both paths, this test will start failing — at which point
// remove it and tighten the parity claim.
func TestCustomErrorHandler_DivergesAcrossPaths(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor("viewer")})

	customBody := `{"error":"custom","detail":"role check failed"}`
	customHandler := func(c *aarv.Context, err error) {
		_ = c.JSON(http.StatusForbidden, json.RawMessage(customBody))
	}

	doRequest := func(setup func(app *aarv.App)) string {
		t.Helper()
		app := aarv.New(aarv.WithBanner(false), aarv.WithErrorHandler(customHandler))
		setup(app)
		app.Get("/p", func(c *aarv.Context) error { return c.NoContent(http.StatusOK) })

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	native := doRequest(func(app *aarv.App) { app.Use(authz.RequireRoles("admin")) })
	stdlib := doRequest(func(app *aarv.App) { app.Use(nonNativeMiddleware(), authz.RequireRoles("admin")) })

	// Native uses the custom handler (its body should contain "custom").
	if !strings.Contains(native, `"error":"custom"`) {
		t.Errorf("native should route through WithErrorHandler; got %q", native)
	}
	// Stdlib bypasses the custom handler (its body should be the plugin's
	// hardcoded "forbidden" shape).
	if !strings.Contains(stdlib, `"error":"forbidden"`) {
		t.Errorf("stdlib bypasses WithErrorHandler; expected plugin's forbidden shape, got %q", stdlib)
	}
	// And therefore the two diverge — which is exactly the documented
	// limitation. If this assertion ever fires, the parity contract has
	// been broadened: update the docs and delete this test.
	if native == stdlib {
		t.Fatalf("native and stdlib unexpectedly match under custom handler; tighten the parity claim and remove this test\nnative=%q\nstdlib=%q", native, stdlib)
	}
}

// TestComposes_AuthorizerReusedAcrossRoutes checks the documented use case
// of building one Authorizer and applying many distinct role policies.
func TestComposes_AuthorizerReusedAcrossRoutes(t *testing.T) {
	authz := New(Config{RoleExtractor: fixedRolesExtractor("editor")})

	app := aarv.New(aarv.WithBanner(false))
	app.Get("/admin", func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) },
		aarv.WithRouteMiddleware(authz.RequireRoles("admin")))
	app.Get("/editorial", func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) },
		aarv.WithRouteMiddleware(authz.RequireAnyRole("admin", "editor")))

	cases := []struct {
		path     string
		wantCode int
	}{
		{"/admin", http.StatusForbidden},
		{"/editorial", http.StatusNoContent},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if rec.Code != tc.wantCode {
			t.Errorf("%s: want %d, got %d body=%s", tc.path, tc.wantCode, rec.Code, rec.Body.String())
		}
	}
}
