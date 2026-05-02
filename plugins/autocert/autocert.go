// Package autocert wires Let's Encrypt / ACME automatic certificate
// management into the aarv lifecycle via golang.org/x/crypto/acme/autocert.
//
// Use [Listen] for a TLS-ALPN-01-only HTTPS listener. To also serve HTTP-01
// challenges from the :80 redirect listener, build one shared manager via
// [Manager], pass it to [ListenWithManager], and set RedirectConfig.ACMEHandler
// to that same manager.
//
// Pair the HTTPS listener with [ListenRedirect] (or [RedirectServer]) on
// :80 to satisfy the ACME HTTP-01 challenge and to redirect plain HTTP
// requests to HTTPS.
package autocert

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/nilshah80/aarv"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

var userCacheDir = os.UserCacheDir

// ErrHostPolicyRequired is returned by [Manager] and [Listen] when
// Config.HostPolicy is nil. A nil host policy would let the ACME manager
// issue certificates for any name — leaving that off requires explicit
// developer intent, not silent permissiveness.
var ErrHostPolicyRequired = errors.New("autocert: HostPolicy is required (use autocert.HostWhitelist(...))")

// ErrNilManager is returned by [ListenWithManager] when its manager
// argument is nil. Exposed as a sentinel so callers can match via errors.Is.
var ErrNilManager = errors.New("autocert: manager is nil")

// ErrNilApp is returned by [Listen] and [ListenWithManager] when their app
// argument is nil. Without this guard the framework would panic on the
// first method call against the nil App.
var ErrNilApp = errors.New("autocert: app is nil")

// Config configures the embedded autocert.Manager and the TLS layer that
// wraps it. HostPolicy is required; everything else has a documented default.
type Config struct {
	// HostPolicy gates which hostnames the manager will request certificates
	// for. Required. Use autocert.HostWhitelist or a custom policy.
	HostPolicy autocert.HostPolicy

	// CacheDir is the on-disk cache directory passed to autocert.DirCache.
	// Empty selects a default under os.UserCacheDir ("aarv-autocert"). The
	// directory is created with mode 0700 (best effort; cross-platform mode
	// guarantees vary).
	CacheDir string

	// Email is the ACME account contact email. Optional but strongly
	// recommended — Let's Encrypt sends expiration notices here.
	Email string

	// DirectoryURL overrides the default ACME directory endpoint, e.g.
	// "https://acme-staging-v02.api.letsencrypt.org/directory" for
	// Let's Encrypt staging. Empty leaves autocert.Manager.Client unset
	// (using autocert's default endpoint).
	DirectoryURL string

	// RenewBefore is forwarded to autocert.Manager.RenewBefore. Zero leaves
	// autocert's default (currently 30 days before expiry).
	RenewBefore time.Duration

	// ConfigureTLS, when non-nil, runs once during [Listen] after the base
	// *tls.Config has been cloned from app.TLSConfig() and BEFORE the
	// autocert-specific fields (GetCertificate, "acme-tls/1" ALPN) are
	// forced. Use it to set CipherSuites, CurvePreferences, etc.
	//
	// Any reduction in MinVersion or any HTTP/2 disable policy from the
	// base App config is re-applied after ConfigureTLS runs, so this hook
	// cannot weaken framework-wide TLS hardening.
	ConfigureTLS func(*tls.Config)

	// ReadTimeout is copied to the *http.Server used by Listen /
	// ListenWithManager. Zero means NO read timeout — the listener will
	// accept slow / never-completing request bodies, exposing it to
	// slowloris-style attacks. The autocert listener does NOT inherit
	// the App's WithReadTimeout; for production set this explicitly,
	// typically matching your App config.
	ReadTimeout time.Duration

	// ReadHeaderTimeout is copied to the *http.Server used by Listen /
	// ListenWithManager. Zero means NO header-read timeout. Same
	// slowloris caveat as ReadTimeout — set explicitly in production.
	ReadHeaderTimeout time.Duration

	// WriteTimeout is copied to the *http.Server used by Listen /
	// ListenWithManager. Zero means NO write timeout. For long-running
	// streaming responses, leaving this zero is intentional; for typical
	// request/response workloads, set it to bound response generation.
	WriteTimeout time.Duration

	// IdleTimeout is copied to the *http.Server used by Listen /
	// ListenWithManager. Zero falls back to ReadTimeout (net/http
	// default behavior); both zero means idle connections are never
	// closed by timer. Set to bound keep-alive lifetime.
	IdleTimeout time.Duration

	// MaxHeaderBytes is copied to the *http.Server used by Listen /
	// ListenWithManager. Zero leaves the net/http default (1 MiB).
	MaxHeaderBytes int
}

// Manager constructs a configured *autocert.Manager from cfg. It validates
// HostPolicy is non-nil and provisions the cache directory.
func Manager(cfg Config) (*autocert.Manager, error) {
	if cfg.HostPolicy == nil {
		return nil, ErrHostPolicyRequired
	}

	cacheDir := resolveCacheDir(cfg.CacheDir)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("autocert: create cache dir %q: %w", cacheDir, err)
	}

	mgr := &autocert.Manager{
		Cache:      autocert.DirCache(cacheDir),
		Prompt:     autocert.AcceptTOS,
		HostPolicy: cfg.HostPolicy,
		Email:      cfg.Email,
	}
	if cfg.RenewBefore > 0 {
		mgr.RenewBefore = cfg.RenewBefore
	}
	if cfg.DirectoryURL != "" {
		mgr.Client = &acme.Client{DirectoryURL: cfg.DirectoryURL}
	}
	return mgr, nil
}

// Listen builds an autocert.Manager from cfg, wires its GetCertificate into
// a hardened *tls.Config (see [buildTLSConfig] for the build recipe), and
// runs the resulting *http.Server through [aarv.App.ListenServer] so
// OnStartup/OnShutdown hooks and graceful shutdown behave consistently.
//
// Returns the same errors as [Manager] for misconfiguration, plus any
// error surfaced by ListenServer.
func Listen(app *aarv.App, addr string, cfg Config) error {
	if app == nil {
		return ErrNilApp
	}
	mgr, err := Manager(cfg)
	if err != nil {
		return err
	}
	return ListenWithManager(app, addr, mgr, cfg)
}

// ListenWithManager is like [Listen] but uses a caller-supplied manager.
// Use this when the same manager must also serve ACME HTTP-01 challenges
// through [RedirectHandler]:
//
//	mgr, _ := autocert.Manager(cfg)
//	go autocert.ListenRedirect(":80", autocert.RedirectConfig{ACMEHandler: mgr})
//	_ = autocert.ListenWithManager(app, ":443", mgr, cfg)
func ListenWithManager(app *aarv.App, addr string, mgr *autocert.Manager, cfg Config) error {
	if app == nil {
		return ErrNilApp
	}
	if mgr == nil {
		return ErrNilManager
	}
	srv := buildServer(app, addr, mgr, cfg)
	return app.ListenServer(srv, func() error {
		return srv.ListenAndServeTLS("", "")
	}, "HTTPS+ACME")
}

func buildServer(app *aarv.App, addr string, mgr *autocert.Manager, cfg Config) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           app,
		TLSConfig:         buildTLSConfig(app, cfg, mgr),
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}
}

// buildTLSConfig assembles the *tls.Config used by Listen. Extracted so the
// build sequence is testable without standing up an ACME server:
//
//  1. Clone app.TLSConfig() — already hardened (TLS 1.2 floor, NextProtos
//     respects WithDisableHTTP2).
//  2. Run cfg.ConfigureTLS for user-controlled tunables.
//  3. Re-floor MinVersion at TLS 1.2 if ConfigureTLS lowered it.
//  4. Re-apply the WithDisableHTTP2 policy if the base App had it set —
//     detected by the base NextProtos being exactly ["http/1.1"]. We do
//     not consult unexported aarv state.
//  5. Force GetCertificate to the manager.
//  6. Append "acme-tls/1" to NextProtos for TLS-ALPN-01 challenge support
//     (idempotent; never re-enables "h2" if the App disabled it).
func buildTLSConfig(app *aarv.App, cfg Config, mgr *autocert.Manager) *tls.Config {
	tlsCfg := app.TLSConfig()
	baseDisablesH2 := slices.Equal(tlsCfg.NextProtos, []string{"http/1.1"})

	if cfg.ConfigureTLS != nil {
		cfg.ConfigureTLS(tlsCfg)
	}

	if tlsCfg.MinVersion < tls.VersionTLS12 {
		tlsCfg.MinVersion = tls.VersionTLS12
	}
	if baseDisablesH2 {
		tlsCfg.NextProtos = []string{"http/1.1"}
	}

	tlsCfg.GetCertificate = mgr.GetCertificate
	tlsCfg.NextProtos = appendUnique(tlsCfg.NextProtos, "acme-tls/1")
	return tlsCfg
}

// appendUnique appends s to slice if not already present. Stable order.
func appendUnique(slice []string, s string) []string {
	if slices.Contains(slice, s) {
		return slice
	}
	return append(slice, s)
}

// resolveCacheDir defaults to filepath.Join(os.UserCacheDir(), "aarv-autocert").
// If os.UserCacheDir fails (e.g. embedded environments), falls back to
// filepath.Join(os.TempDir(), "aarv-autocert") so callers don't need to
// know platform conventions to get a reasonable default.
func resolveCacheDir(configured string) string {
	if configured != "" {
		return configured
	}
	if base, err := userCacheDir(); err == nil {
		return filepath.Join(base, "aarv-autocert")
	}
	return filepath.Join(os.TempDir(), "aarv-autocert")
}

// Compile-time assertion that *autocert.Manager satisfies the
// ACMEChallengeHandler interface used by the redirect package — keeps the
// interface definition honest if the autocert API ever changes shape.
var _ ACMEChallengeHandler = (*autocert.Manager)(nil)
