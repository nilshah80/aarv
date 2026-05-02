package csrf

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nilshah80/aarv"
)

func makeApp(t *testing.T, mw aarv.Middleware) *aarv.App {
	t.Helper()
	app := aarv.New()
	app.Use(mw)
	app.Get("/", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "got "+Token(c))
	})
	app.Post("/", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "post ok")
	})
	app.Get("/skip", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "skipped")
	})
	return app
}

func extractCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSafeMethod_IssuesCookieWhenMissing(t *testing.T) {
	app := makeApp(t, New(DefaultConfig()))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	c := extractCookie(rec, "_csrf")
	if c == nil || c.Value == "" {
		t.Fatal("expected _csrf cookie set")
	}
	if !strings.Contains(rec.Body.String(), c.Value) {
		t.Fatalf("Token(c) should match issued cookie; body=%q cookie=%q", rec.Body.String(), c.Value)
	}
}

func TestSafeMethod_PassesWhenCookiePresent(t *testing.T) {
	app := makeApp(t, New(DefaultConfig()))

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec1 := httptest.NewRecorder()
	app.ServeHTTP(rec1, req1)
	cookie := extractCookie(rec1, "_csrf")
	if cookie == nil {
		t.Fatal("first GET did not set cookie")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(&http.Cookie{Name: "_csrf", Value: cookie.Value})
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second GET: %d", rec2.Code)
	}
	// No regen — no Set-Cookie.
	if extractCookie(rec2, "_csrf") != nil {
		t.Fatal("cookie should not be regenerated when valid one is present")
	}
}

func TestUnsafeMethod_NoCookie_Rejected(t *testing.T) {
	app := makeApp(t, New(DefaultConfig()))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestUnsafeMethod_NoHeader_Rejected(t *testing.T) {
	app := makeApp(t, New(DefaultConfig()))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: "valid"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestUnsafeMethod_HeaderMismatch_Rejected(t *testing.T) {
	app := makeApp(t, New(DefaultConfig()))
	tok, _ := generateToken(32)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: tok})
	other, _ := generateToken(32)
	req.Header.Set("X-CSRF-Token", other)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnsafeMethod_HeaderMatch_Passes(t *testing.T) {
	app := makeApp(t, New(DefaultConfig()))
	tok, _ := generateToken(32)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: tok})
	req.Header.Set("X-CSRF-Token", tok)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFormFieldFallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FormField = "csrf_token"
	app := makeApp(t, New(cfg))
	tok, _ := generateToken(32)
	form := strings.NewReader("csrf_token=" + tok + "&other=value")
	req := httptest.NewRequest(http.MethodPost, "/", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: tok})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSafeMethods_NilDefaults(t *testing.T) {
	cfg := Config{} // SafeMethods: nil
	app := makeApp(t, New(cfg))
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace} {
		t.Run(method, func(t *testing.T) {
			// These methods only match on registered routes; use GET handler for safety.
			if method != http.MethodGet {
				return
			}
			req := httptest.NewRequest(method, "/", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s should bypass: %d", method, rec.Code)
			}
		})
	}
}

func TestSafeMethods_EmptyMeansNoBypass(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SafeMethods = []string{} // explicitly empty — no bypass
	app := makeApp(t, New(cfg))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("empty SafeMethods should require token even on GET; got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSafeMethods_CustomList(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SafeMethods = []string{http.MethodGet} // only GET bypasses
	app := makeApp(t, New(cfg))

	if rec := httptest.NewRecorder(); true {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET should bypass: %d", rec.Code)
		}
	}
	if rec := httptest.NewRecorder(); true {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST should still require token: %d", rec.Code)
		}
	}
}

func TestCookieHTTPOnlyTrue_TokenStillExposedToHandler(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CookieHTTPOnly = true
	app := makeApp(t, New(cfg))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.HasPrefix(body, "got ") || len(body) <= len("got ") {
		t.Fatalf("Token(c) returned empty when HTTPOnly=true; body=%q", body)
	}
	c := extractCookie(rec, "_csrf")
	if c == nil || !c.HttpOnly {
		t.Fatal("expected HttpOnly cookie")
	}
}

func TestSkipper_Bypasses(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Skipper = func(c *aarv.Context) bool {
		return c.Header("X-Skip") != ""
	}
	app := makeApp(t, New(cfg))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Skip", "1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Skipper should bypass: %d", rec.Code)
	}
}

func TestSkipPaths(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SkipPaths = []string{"/skip"}
	app := makeApp(t, New(cfg))
	req := httptest.NewRequest(http.MethodPost, "/skip", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	// /skip is registered as GET only — but skip should still bypass before
	// the framework returns 405 because skip happens in the middleware.
	// In practice the router will return 405 for POST /skip; assert that
	// CSRF did not interfere by checking we did NOT get 403.
	if rec.Code == http.StatusForbidden {
		t.Fatalf("SkipPaths should bypass CSRF; got 403")
	}
}

func TestPanic_TokenLengthTooShort(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if !strings.Contains(r.(string), "TokenLength must be >= 16") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	cfg := DefaultConfig()
	cfg.TokenLength = 8
	_ = New(cfg)
}

func TestCustomCookieAndHeaderNames(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CookieName = "mycsrf"
	cfg.HeaderName = "X-My-Csrf"
	app := makeApp(t, New(cfg))

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec1 := httptest.NewRecorder()
	app.ServeHTTP(rec1, req1)
	cookie := extractCookie(rec1, "mycsrf")
	if cookie == nil {
		t.Fatal("expected mycsrf cookie")
	}

	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.AddCookie(&http.Cookie{Name: "mycsrf", Value: cookie.Value})
	req2.Header.Set("X-My-Csrf", cookie.Value)
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("custom-name flow: %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestErrorHandler_Custom(t *testing.T) {
	cfg := DefaultConfig()
	called := false
	cfg.ErrorHandler = func(c *aarv.Context, reason string) error {
		called = true
		return c.Text(http.StatusTeapot, "blocked: "+reason)
	}
	app := makeApp(t, New(cfg))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if !called {
		t.Fatal("custom ErrorHandler not invoked")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("want 418, got %d", rec.Code)
	}
}

func TestConcurrent_Race(t *testing.T) {
	app := makeApp(t, New(DefaultConfig()))
	tok, _ := generateToken(32)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.AddCookie(&http.Cookie{Name: "_csrf", Value: tok})
			req.Header.Set("X-CSRF-Token", tok)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()
}

// nonNativeMW forces the runtime onto the stdlib path.
func nonNativeMW() aarv.Middleware {
	return aarv.Middleware(func(next http.Handler) http.Handler { return next })
}

func TestStdlibPath_SafeMethodIssuesCookie(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(DefaultConfig()))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib safe: %d", rec.Code)
	}
	if extractCookie(rec, "_csrf") == nil {
		t.Fatal("cookie not issued on stdlib safe path")
	}
}

func TestStdlibPath_UnsafeMethodValidation(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(DefaultConfig()))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	tok, _ := generateToken(32)
	// Missing cookie → 403
	{
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("stdlib no cookie: %d", rec.Code)
		}
	}
	// Cookie + missing header → 403
	{
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.AddCookie(&http.Cookie{Name: "_csrf", Value: tok})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("stdlib missing header: %d", rec.Code)
		}
	}
	// Cookie + mismatched header → 403
	{
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.AddCookie(&http.Cookie{Name: "_csrf", Value: tok})
		other, _ := generateToken(32)
		req.Header.Set("X-CSRF-Token", other)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("stdlib mismatch: %d", rec.Code)
		}
	}
	// Cookie + matching header → 200
	{
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.AddCookie(&http.Cookie{Name: "_csrf", Value: tok})
		req.Header.Set("X-CSRF-Token", tok)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("stdlib match: %d", rec.Code)
		}
	}
}

func TestStdlibPath_FormFieldFallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FormField = "csrf_token"
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(cfg))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	tok, _ := generateToken(32)
	form := strings.NewReader("csrf_token=" + tok)
	req := httptest.NewRequest(http.MethodPost, "/", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "_csrf", Value: tok})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib FormField: %d", rec.Code)
	}
}

func TestStdlibPath_SkipperAndSkipPaths(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SkipPaths = []string{"/skip"}
	cfg.Skipper = func(c *aarv.Context) bool {
		return c.Header("X-Skip") != ""
	}
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(cfg))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	app.Post("/skip", func(c *aarv.Context) error { return c.Text(http.StatusOK, "skipped") })

	if rec := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/skip", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}(); rec.Code != http.StatusOK {
		t.Fatalf("stdlib SkipPaths: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Skip", "1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib Skipper: %d", rec.Code)
	}
}

func TestStdlibPath_CustomErrorHandler(t *testing.T) {
	cfg := DefaultConfig()
	called := false
	cfg.ErrorHandler = func(c *aarv.Context, reason string) error {
		called = true
		return c.Text(http.StatusTeapot, "blocked stdlib: "+reason)
	}
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(cfg))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if !called {
		t.Fatal("custom ErrorHandler not invoked on stdlib")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("stdlib custom: %d", rec.Code)
	}
}

func TestStdlibPath_CustomErrorHandlerError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ErrorHandler = func(c *aarv.Context, reason string) error {
		return aarv.ErrInternal(nil)
	}
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(cfg))
	app.Post("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stdlib handler-error fallback: %d", rec.Code)
	}
}

func TestStdlibPath_NoContext(t *testing.T) {
	// Drive stdlib middleware directly without aarv.Context. Verifies
	// the !hasCtx branches in handleSafeStdlib / handleUnsafeStdlib /
	// rejectStdlib.
	mw := New(DefaultConfig())
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// Safe method: cookie issued, handler called.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !called {
		t.Fatal("handler not called on safe no-context")
	}
	if extractCookie(rec, "_csrf") == nil {
		t.Fatal("cookie not issued on safe no-context")
	}

	// Unsafe with no cookie: 403, request_id empty.
	called = false
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("unsafe no-context: %d", rec2.Code)
	}
	if called {
		t.Fatal("handler should not have been called")
	}
}

func TestToken_NilContextReturnsEmpty(t *testing.T) {
	if got := Token(nil); got != "" {
		t.Fatalf("Token(nil): %q", got)
	}
}

func TestToken_MissingKeyReturnsEmpty(t *testing.T) {
	// Build a Context with no csrfToken set; Token should return "".
	app := aarv.New()
	app.Get("/probe", func(c *aarv.Context) error {
		got := Token(c)
		if got != "" {
			t.Fatalf("Token() with no value: %q", got)
		}
		return c.Text(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("probe: %d", rec.Code)
	}
}

func TestToken_NonStringValueReturnsEmpty(t *testing.T) {
	// Set a non-string under csrfToken and assert Token's type-assert
	// path returns "".
	app := aarv.New()
	app.Get("/probe", func(c *aarv.Context) error {
		c.Set("csrfToken", 12345)
		if Token(c) != "" {
			t.Fatal("non-string value should yield empty Token")
		}
		return c.Text(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("probe: %d", rec.Code)
	}
}

func TestNew_DefaultsApplied(t *testing.T) {
	// Pass an empty Config. New must substitute every documented
	// default (CookieName, HeaderName, CookiePath, MaxAge, SameSite,
	// TokenLength). Not panicking is the success criterion.
	mw := New(Config{})
	if mw == nil {
		t.Fatal("New returned nil")
	}
}

func TestCodeForStatus_AllBranches(t *testing.T) {
	if got := codeForStatus(http.StatusForbidden); got != "forbidden" {
		t.Fatalf("403: %q", got)
	}
	if got := codeForStatus(http.StatusTeapot); got != http.StatusText(http.StatusTeapot) {
		t.Fatalf("418: %q", got)
	}
}

func TestTokensEqual_LengthAndDecodeChecks(t *testing.T) {
	// tokensEqual is only reached after both cookie and header are
	// non-empty (those checks live in handleUnsafe*). Verify the comparison
	// behavior on the inputs that actually reach it.
	if !tokensEqual("abc", "abc") {
		t.Fatal("equal strings should match")
	}
	if tokensEqual("abc", "abcd") {
		t.Fatal("different lengths should not match")
	}
	if tokensEqual("not!base64", "abc") {
		t.Fatal("undecodable first should not match")
	}
	if tokensEqual("abc", "not!base64") {
		t.Fatal("undecodable second should not match")
	}
}

// errReader fails every Read with the configured error. Used to exercise
// the rand.Read failure paths without monkey-patching.
type errReader struct{ err error }

func (r *errReader) Read(p []byte) (int, error) { return 0, r.err }

func TestGenerateToken_RandReadError(t *testing.T) {
	// Swap the package's randReader (NOT crypto/rand.Reader, which
	// Go 1.26+ treats as fatal on failure) so generateToken exercises
	// its error-return path.
	old := randReader
	randReader = &errReader{err: errors.New("entropy exhausted")}
	defer func() { randReader = old }()

	if _, err := generateToken(32); err == nil {
		t.Fatal("expected error from generateToken when rand source fails")
	}
}

func TestEnsureToken_GenerateTokenError(t *testing.T) {
	// rand source failure on a safe-method request must surface as
	// 500 in both native and stdlib paths.
	old := randReader
	randReader = &errReader{err: errors.New("entropy exhausted")}
	defer func() { randReader = old }()

	app := aarv.New()
	app.Use(New(DefaultConfig()))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("native: want 500, got %d", rec.Code)
	}

	// Stdlib lane.
	app2 := aarv.New()
	app2.Use(nonNativeMW())
	app2.Use(New(DefaultConfig()))
	app2.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec2 := httptest.NewRecorder()
	app2.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusInternalServerError {
		t.Fatalf("stdlib: want 500, got %d", rec2.Code)
	}
}
