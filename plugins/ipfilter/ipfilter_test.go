package ipfilter

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func makeApp(t *testing.T, mw aarv.Middleware) *aarv.App {
	t.Helper()
	app := aarv.New()
	app.Use(mw)
	app.Get("/", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})
	app.Get("/skip", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "skipped")
	})
	return app
}

func do(t *testing.T, app *aarv.App, remoteAddr, path string) *httptest.ResponseRecorder {
	t.Helper()
	if path == "" {
		path = "/"
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestAllowlist_ExactIPv4(t *testing.T) {
	app := makeApp(t, New(Config{Mode: ModeAllowlist, CIDRs: []string{"10.0.0.1"}}))
	if rec := do(t, app, "10.0.0.1:1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec := do(t, app, "10.0.0.2:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestAllowlist_CIDRIPv4(t *testing.T) {
	app := makeApp(t, New(Config{Mode: ModeAllowlist, CIDRs: []string{"10.0.0.0/24"}}))
	if rec := do(t, app, "10.0.0.42:1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec := do(t, app, "10.0.1.1:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestAllowlist_ExactIPv6(t *testing.T) {
	app := makeApp(t, New(Config{Mode: ModeAllowlist, CIDRs: []string{"2001:db8::1"}}))
	if rec := do(t, app, "[2001:db8::1]:1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec := do(t, app, "[2001:db8::2]:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestAllowlist_CIDRIPv6(t *testing.T) {
	app := makeApp(t, New(Config{Mode: ModeAllowlist, CIDRs: []string{"2001:db8::/32"}}))
	if rec := do(t, app, "[2001:db8::dead:beef]:1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec := do(t, app, "[2001:db9::1]:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestDenylist_BlockAndPass(t *testing.T) {
	app := makeApp(t, New(Config{Mode: ModeDenylist, CIDRs: []string{"192.168.0.0/16"}}))
	if rec := do(t, app, "192.168.5.5:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if rec := do(t, app, "10.0.0.1:1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestDenylist_Empty_IsNoOp(t *testing.T) {
	app := makeApp(t, New(Config{Mode: ModeDenylist}))
	if rec := do(t, app, "10.0.0.1:1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestPanic_InvalidCIDR(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic on invalid CIDR")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "ipfilter: invalid CIDR") {
			t.Fatalf("unexpected panic message: %v", r)
		}
	}()
	_ = New(Config{Mode: ModeDenylist, CIDRs: []string{"not-a-cidr"}})
}

func TestPanic_AllowlistEmpty(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic on empty allowlist")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "ModeAllowlist requires at least one CIDR") {
			t.Fatalf("unexpected panic message: %v", r)
		}
	}()
	_ = New(Config{Mode: ModeAllowlist})
}

func TestIPFunc_Override(t *testing.T) {
	app := makeApp(t, New(Config{
		Mode:  ModeAllowlist,
		CIDRs: []string{"203.0.113.5"},
		IPFunc: func(c *aarv.Context) string {
			return c.Header("X-Real-Client")
		},
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Real-Client", "203.0.113.5")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.1:1234"
	req2.Header.Set("X-Real-Client", "203.0.113.99")
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec2.Code)
	}
}

func TestSkipper_Bypasses(t *testing.T) {
	app := makeApp(t, New(Config{
		Mode:  ModeAllowlist,
		CIDRs: []string{"127.0.0.1"},
		Skipper: func(c *aarv.Context) bool {
			return c.Path() == "/skip"
		},
	}))
	if rec := do(t, app, "10.0.0.1:1234", "/skip"); rec.Code != http.StatusOK {
		t.Fatalf("want 200 on skipped path, got %d", rec.Code)
	}
	if rec := do(t, app, "10.0.0.1:1234", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 on non-skipped path, got %d", rec.Code)
	}
}

func TestSkipPaths_Bypasses(t *testing.T) {
	app := makeApp(t, New(Config{
		Mode:      ModeAllowlist,
		CIDRs:     []string{"127.0.0.1"},
		SkipPaths: []string{"/skip"},
	}))
	if rec := do(t, app, "10.0.0.1:1234", "/skip"); rec.Code != http.StatusOK {
		t.Fatalf("want 200 on skipped path, got %d", rec.Code)
	}
}

func TestUnparseableSourceIP_FailsClosedAllowlist(t *testing.T) {
	app := makeApp(t, New(Config{
		Mode:  ModeAllowlist,
		CIDRs: []string{"10.0.0.0/8"},
		IPFunc: func(c *aarv.Context) string {
			return "totally-not-an-ip"
		},
	}))
	if rec := do(t, app, "10.0.0.1:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("allowlist should fail closed on unparseable IP, got %d", rec.Code)
	}
}

func TestUnparseableSourceIP_FailsOpenDenylist(t *testing.T) {
	app := makeApp(t, New(Config{
		Mode:  ModeDenylist,
		CIDRs: []string{"10.0.0.0/8"},
		IPFunc: func(c *aarv.Context) string {
			return ""
		},
	}))
	if rec := do(t, app, "10.0.0.1:1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("denylist should fail open on empty IP, got %d", rec.Code)
	}
}

func TestErrorHandler_Custom(t *testing.T) {
	called := false
	app := makeApp(t, New(Config{
		Mode:  ModeAllowlist,
		CIDRs: []string{"10.0.0.1"},
		ErrorHandler: func(c *aarv.Context, ip net.IP) error {
			called = true
			return c.Text(http.StatusTeapot, "blocked")
		},
	}))
	rec := do(t, app, "10.0.0.99:1234", "")
	if !called {
		t.Fatal("ErrorHandler was not called")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("want 418, got %d", rec.Code)
	}
}

func TestBareIPv4_AcceptedAsSlash32(t *testing.T) {
	app := makeApp(t, New(Config{Mode: ModeAllowlist, CIDRs: []string{"10.0.0.1"}}))
	if rec := do(t, app, "10.0.0.0:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 (only /32 should match), got %d", rec.Code)
	}
}

func TestBareIPv6_AcceptedAsSlash128(t *testing.T) {
	app := makeApp(t, New(Config{Mode: ModeAllowlist, CIDRs: []string{"::1"}}))
	if rec := do(t, app, "[::1]:1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec := do(t, app, "[::2]:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

// nonNativeMW is a stdlib-only middleware that does NOT register a
// native pair. Inserting it ahead of an aarv-native middleware forces
// the runtime onto the stdlib path, exposing those branches to tests.
func nonNativeMW() aarv.Middleware {
	return aarv.Middleware(func(next http.Handler) http.Handler { return next })
}

func TestStdlibPath_AllowAndBlock(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{Mode: ModeAllowlist, CIDRs: []string{"10.0.0.0/24"}}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })

	if rec := do(t, app, "10.0.0.5:1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("stdlib allow: %d", rec.Code)
	}
	if rec := do(t, app, "192.168.1.1:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("stdlib block: %d", rec.Code)
	}
}

func TestStdlibPath_SkipPathsAndSkipper(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Mode:      ModeAllowlist,
		CIDRs:     []string{"127.0.0.1"},
		SkipPaths: []string{"/skip"},
		Skipper: func(c *aarv.Context) bool {
			return c.Header("X-Bypass") != ""
		},
	}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	app.Get("/skip", func(c *aarv.Context) error { return c.Text(http.StatusOK, "skipped") })

	if rec := do(t, app, "10.0.0.1:1234", "/skip"); rec.Code != http.StatusOK {
		t.Fatalf("stdlib SkipPaths: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Bypass", "1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib Skipper: %d", rec.Code)
	}
}

func TestStdlibPath_CustomErrorHandler(t *testing.T) {
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Mode:  ModeAllowlist,
		CIDRs: []string{"10.0.0.1"},
		ErrorHandler: func(c *aarv.Context, ip net.IP) error {
			return c.Text(http.StatusTeapot, "blocked stdlib")
		},
	}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	if rec := do(t, app, "10.0.0.99:1234", ""); rec.Code != http.StatusTeapot {
		t.Fatalf("stdlib custom handler: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStdlibPath_CustomErrorHandlerError(t *testing.T) {
	// ErrorHandler returns a non-nil error — the stdlib reject path
	// falls back to the JSON 403 writer.
	app := aarv.New()
	app.Use(nonNativeMW())
	app.Use(New(Config{
		Mode:  ModeAllowlist,
		CIDRs: []string{"10.0.0.1"},
		ErrorHandler: func(c *aarv.Context, ip net.IP) error {
			return aarv.ErrInternal(nil)
		},
	}))
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	rec := do(t, app, "10.0.0.99:1234", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stdlib handler-error fallback: %d", rec.Code)
	}
}

func TestDirectIPFromRemoteAddr(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"10.0.0.1:1234", "10.0.0.1"},
		{"[::1]:1234", "::1"},
		{"nohost", "nohost"},
	}
	for _, tc := range cases {
		if got := directIPFromRemoteAddr(tc.in); got != tc.want {
			t.Fatalf("in=%q want=%q got=%q", tc.in, tc.want, got)
		}
	}
}

func TestParseCIDROrIP_Empty(t *testing.T) {
	if _, err := parseCIDROrIP("   "); err == nil {
		t.Fatal("expected error on whitespace input")
	}
}

func TestParseCIDROrIP_InvalidCIDRMask(t *testing.T) {
	// Contains '/' so the CIDR branch runs; mask is out of range so
	// net.ParseCIDR errors — exercises the error-return path.
	if _, err := parseCIDROrIP("10.0.0.0/99"); err == nil {
		t.Fatal("expected error on invalid CIDR mask")
	}
}

func TestStdlibPath_NoContext_DirectAddr(t *testing.T) {
	// Drive the stdlib middleware directly, with no aarv.Context wired
	// into the request. Forces the directIPFromRemoteAddr branch.
	mw := New(Config{Mode: ModeAllowlist, CIDRs: []string{"10.0.0.0/8"}})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Allowed.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-context allow: %d", rec.Code)
	}

	// Blocked.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "192.168.1.1:1234"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("no-context block: %d", rec2.Code)
	}
}

func TestDefaultConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.StatusCode != http.StatusForbidden || cfg.Message != "forbidden" {
		t.Fatalf("DefaultConfig: %+v", cfg)
	}
}

func TestCodeForStatus_AllBranches(t *testing.T) {
	cases := map[int]string{
		http.StatusForbidden:       "forbidden",
		http.StatusUnauthorized:    "unauthorized",
		http.StatusTooManyRequests: "too_many_requests",
		http.StatusTeapot:          http.StatusText(http.StatusTeapot),
	}
	for status, want := range cases {
		if got := codeForStatus(status); got != want {
			t.Fatalf("codeForStatus(%d): %q != %q", status, got, want)
		}
	}
}

func TestNormalize_DefensiveCopyCIDRs(t *testing.T) {
	cidrs := []string{"10.0.0.0/8"}
	mw := New(Config{Mode: ModeAllowlist, CIDRs: cidrs})
	cidrs[0] = "0.0.0.0/0" // mutate caller's slice after construction
	app := aarv.New()
	app.Use(mw)
	app.Get("/", func(c *aarv.Context) error { return c.Text(http.StatusOK, "ok") })
	if rec := do(t, app, "192.168.1.1:1234", ""); rec.Code != http.StatusForbidden {
		t.Fatal("post-construction mutation of CIDRs leaked into middleware")
	}
}
