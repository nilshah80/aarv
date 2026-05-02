package autocert

import (
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
	"golang.org/x/crypto/acme/autocert"
)

func newSilentApp(opts ...aarv.Option) *aarv.App {
	base := []aarv.Option{
		aarv.WithBanner(false),
		aarv.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	return aarv.New(append(base, opts...)...)
}

func TestManagerRequiresHostPolicy(t *testing.T) {
	if _, err := Manager(Config{}); !errors.Is(err, ErrHostPolicyRequired) {
		t.Fatalf("expected ErrHostPolicyRequired, got %v", err)
	}
}

func TestManagerPopulatesFields(t *testing.T) {
	cacheDir := t.TempDir()
	mgr, err := Manager(Config{
		HostPolicy:   autocert.HostWhitelist("example.com"),
		CacheDir:     cacheDir,
		Email:        "ops@example.com",
		RenewBefore:  10 * 24 * time.Hour,
		DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
	})
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}
	if mgr.Email != "ops@example.com" {
		t.Errorf("Email: got %q", mgr.Email)
	}
	if mgr.RenewBefore != 10*24*time.Hour {
		t.Errorf("RenewBefore: got %v", mgr.RenewBefore)
	}
	if mgr.Client == nil || mgr.Client.DirectoryURL != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Errorf("DirectoryURL: client=%+v", mgr.Client)
	}
	if mgr.Cache == nil {
		t.Error("Cache must be set")
	}
	if _, err := os.Stat(cacheDir); err != nil {
		t.Errorf("cache dir: %v", err)
	}
}

func TestManagerEmptyDirectoryURLLeavesClientNil(t *testing.T) {
	mgr, err := Manager(Config{
		HostPolicy: autocert.HostWhitelist("example.com"),
		CacheDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}
	if mgr.Client != nil {
		t.Fatalf("expected nil Client when DirectoryURL is empty, got %+v", mgr.Client)
	}
}

func TestManagerCacheDirCreateFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "cache-parent")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	_, err := Manager(Config{
		HostPolicy: autocert.HostWhitelist("example.com"),
		CacheDir:   filepath.Join(blocker, "child"),
	})
	if err == nil || !strings.Contains(err.Error(), "create cache dir") {
		t.Fatalf("expected cache-dir creation error, got %v", err)
	}
}

func TestManagerDefaultCacheDirCreated(t *testing.T) {
	// Steer os.UserCacheDir into a tmp root we control by setting XDG_CACHE_HOME.
	tmpHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpHome)
	t.Setenv("HOME", tmpHome) // macOS/BSD UserCacheDir consults $HOME

	mgr, err := Manager(Config{
		HostPolicy: autocert.HostWhitelist("example.com"),
	})
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}
	if mgr.Cache == nil {
		t.Fatal("expected non-nil cache")
	}
	// We don't assert the exact path (UserCacheDir varies by platform); we
	// only assert SOMEthing was created on disk.
	dir := resolveCacheDir("")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("default cache dir not created: %v", err)
	}
}

func TestBuildTLSConfigConfigureTLSCannotWeakenHardening(t *testing.T) {
	app := newSilentApp(aarv.WithDisableHTTP2(true))

	cfg := Config{
		HostPolicy: autocert.HostWhitelist("example.com"),
		CacheDir:   t.TempDir(),
		ConfigureTLS: func(c *tls.Config) {
			c.MinVersion = tls.VersionTLS10       // attempt to weaken
			c.NextProtos = []string{"h2", "evil"} // attempt to re-enable HTTP/2
		},
	}
	mgr, err := Manager(cfg)
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}

	got := buildTLSConfig(app, cfg, mgr)

	if got.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion floor not re-applied; got %v", got.MinVersion)
	}
	// Final NextProtos must be exactly ["http/1.1", "acme-tls/1"] — h2 and
	// "evil" must not survive the rebuild because the base App had HTTP/2
	// disabled (NextProtos == ["http/1.1"]) and the plugin re-applies that
	// policy after ConfigureTLS.
	want := []string{"http/1.1", "acme-tls/1"}
	if !reflect.DeepEqual(got.NextProtos, want) {
		t.Errorf("NextProtos: got %v want %v", got.NextProtos, want)
	}
	if got.GetCertificate == nil {
		t.Error("GetCertificate must be wired to manager")
	}
}

func TestBuildTLSConfigPreservesH2WhenAppAllowsIt(t *testing.T) {
	app := newSilentApp() // HTTP/2 not disabled

	cfg := Config{
		HostPolicy: autocert.HostWhitelist("example.com"),
		CacheDir:   t.TempDir(),
	}
	mgr, err := Manager(cfg)
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}

	got := buildTLSConfig(app, cfg, mgr)

	if !slices.Contains(got.NextProtos, "acme-tls/1") {
		t.Errorf("expected acme-tls/1 in NextProtos, got %v", got.NextProtos)
	}
	// The default base TLSConfig has nil NextProtos when HTTP/2 is allowed,
	// so we should NOT have forced ["http/1.1"]. We allow either nil + acme
	// or any longer slice — the only invariant is that "h2" is not stripped
	// by the plugin in this branch.
	if reflect.DeepEqual(got.NextProtos, []string{"http/1.1"}) {
		t.Errorf("plugin must not force http/1.1-only when App allows HTTP/2")
	}
}

func TestBuildServerCopiesTimeouts(t *testing.T) {
	app := newSilentApp()
	cfg := Config{
		HostPolicy:        autocert.HostWhitelist("example.com"),
		CacheDir:          t.TempDir(),
		ReadTimeout:       time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       4 * time.Second,
		MaxHeaderBytes:    8192,
	}
	mgr, err := Manager(cfg)
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}

	srv := buildServer(app, ":443", mgr, cfg)
	if srv.ReadTimeout != cfg.ReadTimeout ||
		srv.ReadHeaderTimeout != cfg.ReadHeaderTimeout ||
		srv.WriteTimeout != cfg.WriteTimeout ||
		srv.IdleTimeout != cfg.IdleTimeout ||
		srv.MaxHeaderBytes != cfg.MaxHeaderBytes {
		t.Fatalf("server timeouts not copied: %#v", srv)
	}
}

func TestListenWithManagerRejectsNilManager(t *testing.T) {
	app := newSilentApp()
	if err := ListenWithManager(app, ":443", nil, Config{}); !errors.Is(err, ErrNilManager) {
		t.Fatalf("expected ErrNilManager, got %v", err)
	}
}

func TestListenRejectsNilApp(t *testing.T) {
	if err := Listen(nil, ":443", Config{HostPolicy: autocert.HostWhitelist("example.com")}); !errors.Is(err, ErrNilApp) {
		t.Fatalf("expected ErrNilApp from Listen, got %v", err)
	}
}

func TestListenWithManagerRejectsNilApp(t *testing.T) {
	cfg := Config{HostPolicy: autocert.HostWhitelist("example.com"), CacheDir: t.TempDir()}
	mgr, err := Manager(cfg)
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}
	if err := ListenWithManager(nil, ":443", mgr, cfg); !errors.Is(err, ErrNilApp) {
		t.Fatalf("expected ErrNilApp from ListenWithManager, got %v", err)
	}
}

func TestListenPropagatesManagerError(t *testing.T) {
	app := newSilentApp()
	if err := Listen(app, "127.0.0.1:0", Config{}); !errors.Is(err, ErrHostPolicyRequired) {
		t.Fatalf("expected HostPolicy error, got %v", err)
	}
}

func TestListenAndListenWithManagerDelegateToAppLifecycle(t *testing.T) {
	startupErr := errors.New("abort before serving")

	t.Run("Listen", func(t *testing.T) {
		app := newSilentApp()
		app.AddHook(aarv.OnStartup, func(*aarv.Context) error {
			return startupErr
		})
		err := Listen(app, "127.0.0.1:0", Config{
			HostPolicy: autocert.HostWhitelist("example.com"),
			CacheDir:   t.TempDir(),
		})
		if !errors.Is(err, startupErr) {
			t.Fatalf("expected startup error, got %v", err)
		}
	})

	t.Run("ListenWithManager", func(t *testing.T) {
		app := newSilentApp()
		app.AddHook(aarv.OnStartup, func(*aarv.Context) error {
			return startupErr
		})
		cfg := Config{
			HostPolicy: autocert.HostWhitelist("example.com"),
			CacheDir:   t.TempDir(),
		}
		mgr, err := Manager(cfg)
		if err != nil {
			t.Fatalf("Manager: %v", err)
		}
		err = ListenWithManager(app, "127.0.0.1:0", mgr, cfg)
		if !errors.Is(err, startupErr) {
			t.Fatalf("expected startup error, got %v", err)
		}
	})
}

func TestListenWithManagerServeErrorSurfaces(t *testing.T) {
	app := newSilentApp()
	cfg := Config{
		HostPolicy: autocert.HostWhitelist("example.com"),
		CacheDir:   t.TempDir(),
	}
	mgr, err := Manager(cfg)
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}

	err = ListenWithManager(app, "127.0.0.1:bad-port", mgr, cfg)
	if err == nil {
		t.Fatal("expected invalid address error from ListenAndServeTLS")
	}
}

func TestAppendUnique(t *testing.T) {
	got := appendUnique([]string{"a", "b"}, "b")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("duplicate appended: %v", got)
	}
	got = appendUnique([]string{"a"}, "c")
	if !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("missing appended: %v", got)
	}
}

func TestRedirectHandlerDefaultStatus(t *testing.T) {
	h := RedirectHandler(RedirectConfig{TargetHost: "example.com"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://anything.test/foo?x=1", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("status: got %d want 308", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://example.com/foo?x=1" {
		t.Fatalf("Location: %q", loc)
	}
}

func TestRedirectHandlerCustomCodeAndHostFromRequest(t *testing.T) {
	h := RedirectHandler(RedirectConfig{Code: http.StatusFound})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://my-host.test/path", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("status: %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Host != "my-host.test" || u.Scheme != "https" {
		t.Fatalf("location: %s", loc)
	}
}

func TestRedirectHandlerPreservesEscapedPath(t *testing.T) {
	cases := []struct {
		name     string
		rawURI   string
		wantPath string // post-encoding portion of Location after the host
	}{
		{"slash escape", "/a%2Fb/c?z=1", "/a%2Fb/c?z=1"},
		{"unicode", "/cafe%CC%81?q=test", "/cafe%CC%81?q=test"},
		{"plus and space", "/q?x=hello+world", "/q?x=hello+world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := RedirectHandler(RedirectConfig{TargetHost: "example.com"})
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://example.com"+tc.rawURI, nil)
			h.ServeHTTP(rec, r)
			loc := rec.Header().Get("Location")
			want := "https://example.com" + tc.wantPath
			if loc != want {
				t.Fatalf("Location: got %q want %q", loc, want)
			}
		})
	}
}

func TestRedirectHandlerHTTPSPort(t *testing.T) {
	cases := []struct {
		host     string
		port     int
		wantHost string
	}{
		{"example.com", 8443, "example.com:8443"},
		{"example.com:80", 8443, "example.com:8443"}, // strips inbound port
		{"[::1]:80", 8443, "[::1]:8443"},             // IPv6 literal with port
		{"::1", 8443, "[::1]:8443"},                  // bare IPv6 literal re-bracketed
		{"example.com:80", 0, "example.com"},         // strips default inbound HTTP port
		{"example.com:443", 0, "example.com"},        // strips default inbound HTTPS port
		{"example.com:8080", 0, "example.com:8080"},  // preserves non-default inbound port
		{"example.com", 443, "example.com"},          // 443 omitted
		{"example.com", 0, "example.com"},            // zero omitted
		{"::1", 0, "[::1]"},                          // bare IPv6 literal must be bracketed (regression)
		{"2001:db8::1", 0, "[2001:db8::1]"},          // longer bare IPv6 literal also bracketed
		{"[2001:db8::1]", 0, "[2001:db8::1]"},        // already-bracketed IPv6 unchanged
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			h := RedirectHandler(RedirectConfig{TargetHost: tc.host, HTTPSPort: tc.port})
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://anything.test/", nil)
			h.ServeHTTP(rec, r)
			loc := rec.Header().Get("Location")
			u, err := url.Parse(loc)
			if err != nil {
				t.Fatalf("parse %q: %v", loc, err)
			}
			if u.Host != tc.wantHost {
				t.Fatalf("Host: got %q want %q (Location=%s)", u.Host, tc.wantHost, loc)
			}
		})
	}
}

func TestRedirectHandlerRejectsControlChars(t *testing.T) {
	cases := []struct{ host string }{
		{"evil.com\r\nSet-Cookie: x"},
		{"good.com\x00null"},
		{"tab\tinside"},
	}
	for _, tc := range cases {
		t.Run(strings.TrimSpace(tc.host), func(t *testing.T) {
			h := RedirectHandler(RedirectConfig{TargetHost: tc.host})
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
			h.ServeHTTP(rec, r)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for control char, got %d", rec.Code)
			}
			if rec.Header().Get("Location") != "" {
				t.Fatal("must not emit Location for invalid host")
			}
		})
	}
}

func TestRedirectHandlerRejectsEmptyHost(t *testing.T) {
	h := RedirectHandler(RedirectConfig{}) // no TargetHost
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	r.Host = "" // simulate HTTP/1.0 client without Host header
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty host, got %d", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Fatal("must not emit Location for empty host")
	}
}

func TestRedirectHandlerRejectsPortOnlyHost(t *testing.T) {
	h := RedirectHandler(RedirectConfig{TargetHost: ":8443"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for port-only host, got %d", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Fatal("must not emit Location for port-only host")
	}
}

// fakeACMEHandler implements ACMEChallengeHandler so the delegation path is
// testable without standing up a real autocert.Manager.
type fakeACMEHandler struct {
	tag string
}

func (f *fakeACMEHandler) HTTPHandler(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
			w.Header().Set("X-Acme-Tag", f.tag)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("acme"))
			return
		}
		fallback.ServeHTTP(w, r)
	})
}

func TestRedirectHandlerDelegatesACMEChallenge(t *testing.T) {
	fake := &fakeACMEHandler{tag: "delegated"}
	h := RedirectHandler(RedirectConfig{TargetHost: "example.com", ACMEHandler: fake})

	t.Run("acme path bypasses redirect", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://example.com/.well-known/acme-challenge/abc", nil)
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("acme path should return 200, got %d", rec.Code)
		}
		if rec.Header().Get("X-Acme-Tag") != "delegated" {
			t.Fatal("ACMEChallengeHandler not invoked")
		}
	})

	t.Run("non-acme path still redirects", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusPermanentRedirect {
			t.Fatalf("expected redirect, got %d", rec.Code)
		}
	})
}

func TestRedirectServerReturnsConfiguredServer(t *testing.T) {
	srv := RedirectServer(":12345", RedirectConfig{TargetHost: "example.com"})
	if srv.Addr != ":12345" {
		t.Errorf("Addr: %q", srv.Addr)
	}
	if srv.Handler == nil {
		t.Error("Handler must be set")
	}
}

func TestRedirectServerAppliesSlowlorisTimeoutDefaults(t *testing.T) {
	srv := RedirectServer(":12345", RedirectConfig{TargetHost: "example.com"})
	if srv.ReadHeaderTimeout != defaultRedirectReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout default: got %v want %v", srv.ReadHeaderTimeout, defaultRedirectReadHeaderTimeout)
	}
	if srv.ReadTimeout != defaultRedirectReadTimeout {
		t.Errorf("ReadTimeout default: got %v want %v", srv.ReadTimeout, defaultRedirectReadTimeout)
	}
	if srv.IdleTimeout != defaultRedirectIdleTimeout {
		t.Errorf("IdleTimeout default: got %v want %v", srv.IdleTimeout, defaultRedirectIdleTimeout)
	}
}

func TestRedirectServerHonorsExplicitTimeouts(t *testing.T) {
	srv := RedirectServer(":12345", RedirectConfig{
		TargetHost:        "example.com",
		ReadHeaderTimeout: 250 * time.Millisecond,
		ReadTimeout:       500 * time.Millisecond,
		IdleTimeout:       2 * time.Second,
	})
	if srv.ReadHeaderTimeout != 250*time.Millisecond ||
		srv.ReadTimeout != 500*time.Millisecond ||
		srv.IdleTimeout != 2*time.Second {
		t.Fatalf("explicit timeouts not honored: %#v", srv)
	}
}

func TestRedirectServerNegativeTimeoutsDisableCap(t *testing.T) {
	srv := RedirectServer(":12345", RedirectConfig{
		TargetHost:        "example.com",
		ReadHeaderTimeout: -1,
		ReadTimeout:       -1,
		IdleTimeout:       -1,
	})
	if srv.ReadHeaderTimeout != 0 || srv.ReadTimeout != 0 || srv.IdleTimeout != 0 {
		t.Fatalf("negative durations should disable (zero out) the timeouts: %#v", srv)
	}
}

func TestRedirectTimeoutNormalization(t *testing.T) {
	cases := []struct {
		name       string
		configured time.Duration
		fallback   time.Duration
		want       time.Duration
	}{
		{"zero uses fallback", 0, 5 * time.Second, 5 * time.Second},
		{"positive passes through", 7 * time.Second, 5 * time.Second, 7 * time.Second},
		{"negative disables", -1, 5 * time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redirectTimeout(tc.configured, tc.fallback); got != tc.want {
				t.Fatalf("redirectTimeout(%v,%v) = %v, want %v", tc.configured, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestListenRedirectInvalidAddrReturns(t *testing.T) {
	err := ListenRedirect("127.0.0.1:bad-port", RedirectConfig{TargetHost: "example.com"})
	if err == nil {
		t.Fatal("expected ListenRedirect to surface net.Listen error")
	}
}

func TestResolveCacheDirFallback(t *testing.T) {
	dir := resolveCacheDir("")
	// Must contain "aarv-autocert" regardless of which fallback fired.
	if filepath.Base(dir) != "aarv-autocert" {
		t.Errorf("default base should be aarv-autocert, got %q", dir)
	}

	if got := resolveCacheDir("/explicit/path"); got != "/explicit/path" {
		t.Errorf("explicit path not honored: %q", got)
	}
}

func TestResolveCacheDirUsesTempFallbackWhenUserCacheDirFails(t *testing.T) {
	orig := userCacheDir
	t.Cleanup(func() { userCacheDir = orig })
	userCacheDir = func() (string, error) {
		return "", errors.New("no user cache")
	}

	dir := resolveCacheDir("")
	want := filepath.Join(os.TempDir(), "aarv-autocert")
	if dir != want {
		t.Fatalf("fallback dir: got %q want %q", dir, want)
	}
}

func TestSplitHostPortBestEffort(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantHost string
		wantPort string
	}{
		{"split host port", "example.com:8443", "example.com", "8443"},
		{"bracketed ipv6 port", "[2001:db8::1]:8443", "2001:db8::1", "8443"},
		{"bare ipv6", "2001:db8::1", "2001:db8::1", ""},
		{"host only", "example.com", "example.com", ""},
		{"empty port", "example.com:", "example.com", ""},
		{"port only", ":8443", "", "8443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port := splitHostPortBestEffort(tc.in)
			if host != tc.wantHost || port != tc.wantPort {
				t.Fatalf("splitHostPortBestEffort(%q) = (%q,%q), want (%q,%q)", tc.in, host, port, tc.wantHost, tc.wantPort)
			}
		})
	}
}
