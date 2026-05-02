package aarv

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// generateCertPEM writes a self-signed cert + key (PEM, ECDSA P-256) to the
// given paths. Each call produces a distinct certificate so reload behavior
// can be observed.
func generateCertPEM(t *testing.T, certPath, keyPath, cn string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

func newCertPaths(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
}

func leafCN(t *testing.T, cert *tls.Certificate) string {
	t.Helper()
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatal("empty certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return leaf.Subject.CommonName
}

func TestNewCertReloaderInitialLoad(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-initial")

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cn := leafCN(t, cert); cn != "cn-initial" {
		t.Fatalf("unexpected CN: %q", cn)
	}
}

func TestNewCertReloaderDefaultInterval(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-default-interval")

	r, err := NewCertReloader(certPath, keyPath, 0, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	if r.interval != defaultCertReloadInterval {
		t.Fatalf("expected default interval %v, got %v", defaultCertReloadInterval, r.interval)
	}
}

func TestNewCertReloaderInitialLoadFailure(t *testing.T) {
	r, err := NewCertReloader("/no/such/cert", "/no/such/key", time.Second, nil)
	if err == nil {
		t.Fatal("expected initial load failure")
	}
	if r != nil {
		t.Fatal("expected nil reloader on failure")
	}
}

func TestNewCertReloaderInitialKeyStatFailure(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	if err := os.WriteFile(certPath, []byte("placeholder"), 0600); err != nil {
		t.Fatalf("write cert placeholder: %v", err)
	}

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "initial stat (key)") {
		t.Fatalf("expected key stat failure, got reloader=%v err=%v", r, err)
	}
}

func TestNewCertReloaderInitialLoadFailureAfterStat(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	if err := os.WriteFile(certPath, []byte("not a cert"), 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("not a key"), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "initial load") {
		t.Fatalf("expected initial load failure, got reloader=%v err=%v", r, err)
	}
}

func TestCertReloaderGetCertificateWithoutLoadedCert(t *testing.T) {
	r := &CertReloader{}
	cert, err := r.GetCertificate(nil)
	if cert != nil {
		t.Fatalf("expected nil cert, got %v", cert)
	}
	if !errors.Is(err, ErrCertReloaderEmpty) {
		t.Fatalf("expected ErrCertReloaderEmpty, got %v", err)
	}
}

func TestCertReloaderReloadOnceNoChange(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-stable")

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	if err := r.reloadOnce(); err != nil {
		t.Fatalf("reloadOnce with unchanged files must be nil, got %v", err)
	}
	c, _ := r.GetCertificate(nil)
	if cn := leafCN(t, c); cn != "cn-stable" {
		t.Fatalf("unchanged reload should preserve cert, got CN %q", cn)
	}
}

func TestCertReloaderReloadOnceStatFailures(t *testing.T) {
	t.Run("cert stat", func(t *testing.T) {
		certPath, keyPath := newCertPaths(t)
		generateCertPEM(t, certPath, keyPath, "cn")
		r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
		if err != nil {
			t.Fatalf("NewCertReloader: %v", err)
		}
		if err := os.Remove(certPath); err != nil {
			t.Fatalf("remove cert: %v", err)
		}
		if err := r.reloadOnce(); err == nil {
			t.Fatal("expected cert stat error")
		}
	})

	t.Run("key stat", func(t *testing.T) {
		certPath, keyPath := newCertPaths(t)
		generateCertPEM(t, certPath, keyPath, "cn")
		r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
		if err != nil {
			t.Fatalf("NewCertReloader: %v", err)
		}
		if err := os.Remove(keyPath); err != nil {
			t.Fatalf("remove key: %v", err)
		}
		if err := r.reloadOnce(); err == nil {
			t.Fatal("expected key stat error")
		}
	})
}

func TestCertReloaderReloadOnMtimeChange(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-v1")

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	c1, _ := r.GetCertificate(nil)
	if cn := leafCN(t, c1); cn != "cn-v1" {
		t.Fatalf("initial CN: %q", cn)
	}

	// Force the new cert's mtime forward and content distinct, then trigger
	// a single reload synchronously.
	generateCertPEM(t, certPath, keyPath, "cn-v2")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatalf("chtimes cert: %v", err)
	}
	if err := os.Chtimes(keyPath, future, future); err != nil {
		t.Fatalf("chtimes key: %v", err)
	}

	if err := r.reloadOnce(); err != nil {
		t.Fatalf("reloadOnce: %v", err)
	}
	c2, _ := r.GetCertificate(nil)
	if cn := leafCN(t, c2); cn != "cn-v2" {
		t.Fatalf("reloaded CN: %q", cn)
	}
}

func TestCertReloaderReloadOnSizeChange(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	// Short CN; cert serialization length is dominated by the Subject DN.
	generateCertPEM(t, certPath, keyPath, "a")

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	origCertInfo, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("stat cert: %v", err)
	}
	origKeyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}

	// Replace with a much longer CN so the cert's encoded byte length
	// changes deterministically (Subject CommonName flows directly into
	// the DER encoding length). Pin mtime to the original so the only
	// detectable change is Size.
	const longCN = "this-is-a-deliberately-long-common-name-to-force-a-byte-length-delta"
	generateCertPEM(t, certPath, keyPath, longCN)

	newCertInfo, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("re-stat cert: %v", err)
	}
	if newCertInfo.Size() == origCertInfo.Size() {
		t.Fatalf("test setup: expected cert size delta, got identical sizes %d", newCertInfo.Size())
	}

	if err := os.Chtimes(certPath, origCertInfo.ModTime(), origCertInfo.ModTime()); err != nil {
		t.Fatalf("chtimes cert: %v", err)
	}
	if err := os.Chtimes(keyPath, origKeyInfo.ModTime(), origKeyInfo.ModTime()); err != nil {
		t.Fatalf("chtimes key: %v", err)
	}

	if err := r.reloadOnce(); err != nil {
		t.Fatalf("reloadOnce: %v", err)
	}
	c, _ := r.GetCertificate(nil)
	if cn := leafCN(t, c); cn != longCN {
		t.Fatalf("reloaded CN: %q", cn)
	}
}

func TestCertReloaderMalformedReloadPreservesPreviousCert(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-good")

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	r, err := NewCertReloader(certPath, keyPath, time.Second, logger)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	// Truncate the cert to invalid PEM but bump mtime so the poll attempts
	// a load.
	if err := os.WriteFile(certPath, []byte("not a real cert"), 0600); err != nil {
		t.Fatalf("write bad cert: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(certPath, future, future)

	if err := r.reloadOnce(); err == nil {
		t.Fatal("expected reloadOnce to fail on malformed cert")
	}

	// Previous cert still served.
	c, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after bad reload: %v", err)
	}
	if cn := leafCN(t, c); cn != "cn-good" {
		t.Fatalf("expected previous cert preserved, got CN %q", cn)
	}
	if !strings.Contains(logBuf.String(), "load failed") {
		t.Fatalf("expected warn log for failed reload, got: %s", logBuf.String())
	}
}

func TestCertReloaderStartStopLifecycle(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-lifecycle")

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	// Stop on never-started reloader is a no-op (no panic).
	r.Stop()

	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start after Stop-without-prior-Start should still succeed because state was notStarted, got %v", err)
	}

	// Start while already running -> ErrReloaderStarted.
	if err := r.Start(context.Background()); !errors.Is(err, ErrReloaderStarted) {
		t.Fatalf("expected ErrReloaderStarted, got %v", err)
	}

	// Stop blocks until polling goroutine exits.
	stopReturned := make(chan struct{})
	go func() {
		r.Stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within deadline")
	}

	// Start after Stop -> ErrReloaderStopped (one-shot).
	if err := r.Start(context.Background()); !errors.Is(err, ErrReloaderStopped) {
		t.Fatalf("expected ErrReloaderStopped on restart, got %v", err)
	}

	// Stop is idempotent.
	r.Stop()
}

func TestCertReloaderStartNilContext(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-nil-context")

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	// Hide the literal nil from staticcheck SA1012; we are deliberately
	// exercising the nil-context guard.
	var ctx context.Context
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start(nil): %v", err)
	}
	r.Stop()
}

func TestCertReloaderCtxCancelTransitionsToStopped(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-ctx")

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	cancel()

	// Wait for the polling goroutine to exit and transition state.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		state := r.state
		r.mu.Unlock()
		if state == reloaderStopped {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	r.mu.Lock()
	state := r.state
	r.mu.Unlock()
	if state != reloaderStopped {
		t.Fatalf("expected state=stopped after ctx cancel, got %d", state)
	}

	// One-shot contract: post-cancel Start must return ErrReloaderStopped.
	if err := r.Start(context.Background()); !errors.Is(err, ErrReloaderStopped) {
		t.Fatalf("expected ErrReloaderStopped after ctx cancel, got %v", err)
	}

	// Stop after ctx cancel must be idempotent and not deadlock.
	r.Stop()
}

func TestCertReloaderInitialStatBeforeLoad(t *testing.T) {
	// Verify that a fast replacement between the initial stat and the
	// initial load is observed by the next reloadOnce (the saved
	// signature points at the PRE-load state, so post-load changes are
	// detectable).
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-pre-load")

	r, err := NewCertReloader(certPath, keyPath, time.Second, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	// Replace the cert AFTER the constructor returned (simulating a
	// hot replacement at startup). The saved signatures still point at
	// the pre-load state, so the next reload must observe the change.
	generateCertPEM(t, certPath, keyPath, "cn-replaced")
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(certPath, future, future)
	_ = os.Chtimes(keyPath, future, future)

	if err := r.reloadOnce(); err != nil {
		t.Fatalf("reloadOnce: %v", err)
	}
	c, _ := r.GetCertificate(nil)
	if cn := leafCN(t, c); cn != "cn-replaced" {
		t.Fatalf("expected reload to observe post-construct replacement, got CN %q", cn)
	}
}

func TestCertReloaderPollingTriggersReload(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-poll-v1")

	// Use minimum interval (1s) to keep test bounded but avoid excessively
	// tight scheduling; verify the polling goroutine actually picks up a
	// change.
	r, err := NewCertReloader(certPath, keyPath, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	// Constructor floors interval at 1s; verify the loop still picks up
	// changes within a couple of ticks.
	if r.interval != minCertReloadInterval {
		t.Fatalf("expected interval floor to %v, got %v", minCertReloadInterval, r.interval)
	}

	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop()

	// Replace cert content + bump mtime forward.
	generateCertPEM(t, certPath, keyPath, "cn-poll-v2")
	future := time.Now().Add(5 * time.Second)
	_ = os.Chtimes(certPath, future, future)
	_ = os.Chtimes(keyPath, future, future)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		c, _ := r.GetCertificate(nil)
		if leafCN(t, c) == "cn-poll-v2" {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("polling goroutine did not reload cert within deadline")
}

func TestWithCertReloadDefaultsAndFloor(t *testing.T) {
	app := newSilentApp(WithCertReload(0))
	if !app.config.CertReloadEnabled {
		t.Fatal("expected CertReloadEnabled true")
	}
	if app.reloadInterval() != defaultCertReloadInterval {
		t.Fatalf("expected default interval %v, got %v", defaultCertReloadInterval, app.reloadInterval())
	}

	app2 := newSilentApp(WithCertReload(10 * time.Millisecond))
	if app2.reloadInterval() != minCertReloadInterval {
		t.Fatalf("expected floor %v, got %v", minCertReloadInterval, app2.reloadInterval())
	}

	app3 := newSilentApp(WithCertReload(45 * time.Second))
	if app3.reloadInterval() != 45*time.Second {
		t.Fatalf("expected pass-through 45s, got %v", app3.reloadInterval())
	}
}

func TestSetupCertReloadDisabledPassesThrough(t *testing.T) {
	app := newSilentApp()
	tlsCfg := app.TLSConfig()

	cleanup, certArg, keyArg, err := app.setupCertReload(tlsCfg, "server.crt", "server.key")
	if err != nil {
		t.Fatalf("setupCertReload: %v", err)
	}
	if cleanup != nil {
		t.Fatal("cleanup must be nil when reload is disabled")
	}
	if certArg != "server.crt" || keyArg != "server.key" {
		t.Fatalf("expected cert/key args to pass through, got %q %q", certArg, keyArg)
	}
	if tlsCfg.GetCertificate != nil {
		t.Fatal("GetCertificate must remain unset when reload is disabled")
	}
}

func TestSetupCertReloadEnabledWiresCertificateProvider(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-setup")
	app := newSilentApp(WithCertReload(time.Second))
	tlsCfg := app.TLSConfig()

	cleanup, certArg, keyArg, err := app.setupCertReload(tlsCfg, certPath, keyPath)
	if err != nil {
		t.Fatalf("setupCertReload: %v", err)
	}
	defer cleanup()

	if certArg != "" || keyArg != "" {
		t.Fatalf("expected empty cert/key args when GetCertificate is set, got %q %q", certArg, keyArg)
	}
	if tlsCfg.GetCertificate == nil {
		t.Fatal("GetCertificate must be wired when reload is enabled")
	}
	cert, err := tlsCfg.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cn := leafCN(t, cert); cn != "cn-setup" {
		t.Fatalf("unexpected cert CN %q", cn)
	}
}

type failingStartReloader struct{}

func (failingStartReloader) Start(context.Context) error {
	return errors.New("forced start failure")
}
func (failingStartReloader) Stop() {}
func (failingStartReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return nil, errors.New("unused")
}

func TestSetupCertReloadStartFailure(t *testing.T) {
	orig := newCertReloader
	t.Cleanup(func() { newCertReloader = orig })
	newCertReloader = func(string, string, time.Duration, *slog.Logger) (certReloadController, error) {
		return failingStartReloader{}, nil
	}

	app := newSilentApp(WithCertReload(time.Second))
	_, _, _, err := app.setupCertReload(app.TLSConfig(), "cert", "key")
	if err == nil || !strings.Contains(err.Error(), "forced start failure") {
		t.Fatalf("expected start failure, got %v", err)
	}
}

func TestListenTLSConflictsWithUserGetCertificate(t *testing.T) {
	base := &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return nil, errors.New("user provider")
		},
	}
	app := newSilentApp(WithTLSConfig(base), WithCertReload(time.Second))
	err := app.ListenTLS("127.0.0.1:0", "ignored.crt", "ignored.key")
	if !errors.Is(err, ErrCertReloadConflict) {
		t.Fatalf("expected ErrCertReloadConflict, got %v", err)
	}
}

func TestListenMutualTLSConflictsWithUserGetCertificate(t *testing.T) {
	certPath, keyPath := newCertPaths(t)
	generateCertPEM(t, certPath, keyPath, "cn-conflict")
	caPath := filepath.Join(filepath.Dir(certPath), "ca.crt")
	if err := os.WriteFile(caPath, []byte{}, 0600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	base := &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return nil, errors.New("user provider")
		},
	}
	app := newSilentApp(WithTLSConfig(base), WithCertReload(time.Second))
	err := app.ListenMutualTLS("127.0.0.1:0", certPath, keyPath, caPath)
	if !errors.Is(err, ErrCertReloadConflict) {
		t.Fatalf("expected ErrCertReloadConflict, got %v", err)
	}
}

func TestListenTLSInitialLoadFailureBeforeServe(t *testing.T) {
	app := newSilentApp(WithCertReload(time.Second))
	var startupRan atomic.Bool
	app.AddHook(OnStartup, func(*Context) error {
		startupRan.Store(true)
		return nil
	})

	err := app.ListenTLS("127.0.0.1:0", "/no/such/cert", "/no/such/key")
	if err == nil {
		t.Fatal("expected initial-load failure")
	}
	if !strings.Contains(err.Error(), "cert reload initial") {
		t.Fatalf("expected wrapped initial-load/stat error, got %v", err)
	}
	// Conflict / load failures must short-circuit before ListenServer
	// runs OnStartup.
	if startupRan.Load() {
		t.Fatal("OnStartup must not run when initial cert load fails")
	}
}

func TestWithCertReloadOnPlainListenWarnsOnce(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	app := New(WithBanner(false), WithLogger(logger), WithCertReload(time.Second))

	// Stop Listen quickly via OnStartup error so we don't actually serve.
	app.AddHook(OnStartup, func(*Context) error { return errors.New("abort") })

	_ = app.Listen("127.0.0.1:0")

	out := logBuf.String()
	count := strings.Count(out, "WithCertReload set but Listen is plain HTTP")
	if count != 1 {
		t.Fatalf("expected exactly one WithCertReload-on-Listen warning, got %d:\n%s", count, out)
	}
}
