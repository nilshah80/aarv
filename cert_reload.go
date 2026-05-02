package aarv

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultCertReloadInterval = 30 * time.Second
	minCertReloadInterval     = time.Second
)

// CertReloader watches a cert/key file pair on disk and serves the latest
// loaded certificate via GetCertificate, suitable for assignment to
// tls.Config.GetCertificate.
//
// CertReloader is one-shot: after Stop returns (or after the context passed
// to Start is canceled and the polling goroutine exits), calling Start
// again returns ErrReloaderStopped. Construct a new CertReloader to reload
// again on the same App.
//
// Polling compares (ModTime, Size) for both files; reload triggers when
// either changes on either file. Malformed reloads (e.g. truncated PEM
// during a non-atomic write) preserve the previous certificate and log a
// warning.
type CertReloader struct {
	certFile string
	keyFile  string
	interval time.Duration
	logger   *slog.Logger

	cert atomic.Pointer[tls.Certificate]

	mu        sync.Mutex
	state     reloaderState
	stopCh    chan struct{}
	stoppedCh chan struct{}
	stopOnce  sync.Once

	sigMu       sync.Mutex
	lastCertSig fileSig
	lastKeySig  fileSig
}

type reloaderState int

const (
	reloaderNotStarted reloaderState = iota
	reloaderRunning
	reloaderStopped
)

type fileSig struct {
	modTime time.Time
	size    int64
}

// NewCertReloader constructs a CertReloader, performs the initial cert/key
// load, and records the file signatures used by subsequent polls. It does
// not start the polling goroutine — call Start.
//
// Returns an error if the initial LoadX509KeyPair fails, so callers can
// abort startup before serving any traffic.
//
// interval is normalized: zero becomes the 30s default, then any value
// below 1s is raised to 1s. A nil logger falls back to slog.Default().
func NewCertReloader(certFile, keyFile string, interval time.Duration, logger *slog.Logger) (*CertReloader, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if interval == 0 {
		interval = defaultCertReloadInterval
	}
	if interval < minCertReloadInterval {
		interval = minCertReloadInterval
	}

	r := &CertReloader{
		certFile: certFile,
		keyFile:  keyFile,
		interval: interval,
		logger:   logger,
	}

	// Stat BEFORE loading. If the files are replaced between the stat and
	// the load, LoadX509KeyPair sees the new bytes and we record the OLD
	// signatures — the next poll observes the change and re-loads (a
	// no-op if the bytes already match what we just loaded). The reverse
	// order would let a fast-replacement land where the stored cert is
	// stale but the saved signature already matches the replacement, so
	// the change is never observed.
	certInfo, err := os.Stat(certFile)
	if err != nil {
		return nil, fmt.Errorf("aarv: cert reload initial stat (cert): %w", err)
	}
	keyInfo, err := os.Stat(keyFile)
	if err != nil {
		return nil, fmt.Errorf("aarv: cert reload initial stat (key): %w", err)
	}
	r.lastCertSig = fileSig{certInfo.ModTime(), certInfo.Size()}
	r.lastKeySig = fileSig{keyInfo.ModTime(), keyInfo.Size()}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("aarv: cert reload initial load: %w", err)
	}
	r.cert.Store(&cert)

	return r, nil
}

// Start begins the polling goroutine. The goroutine exits when Stop is
// called or ctx is canceled. After either, the reloader is permanently
// in the stopped state.
//
// Returns ErrReloaderStarted if Start has already been called and the
// poller has not yet exited. Returns ErrReloaderStopped if the poller has
// previously exited (via Stop or ctx cancellation).
func (r *CertReloader) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	switch r.state {
	case reloaderRunning:
		return ErrReloaderStarted
	case reloaderStopped:
		return ErrReloaderStopped
	}

	r.state = reloaderRunning
	r.stopCh = make(chan struct{})
	r.stoppedCh = make(chan struct{})
	r.stopOnce = sync.Once{}
	go r.poll(ctx)
	return nil
}

// Stop halts the polling goroutine and blocks until it has exited. Idempotent
// across all states: not-yet-started, running, already-stopped, and stopped
// via ctx cancellation are all safe to call (no panics, no double-close).
//
// Stop on a never-started reloader is a no-op — it does NOT transition state
// to stopped, so a later Start can still succeed. Once Start has been called,
// the reloader becomes one-shot.
func (r *CertReloader) Stop() {
	r.mu.Lock()

	if r.state == reloaderNotStarted {
		r.mu.Unlock()
		return
	}

	// Either currently running, or the poll goroutine exited via ctx
	// cancellation and transitioned state to stopped on its way out.
	stoppedCh := r.stoppedCh
	if r.state == reloaderRunning {
		r.state = reloaderStopped
	}
	r.mu.Unlock()

	// Idempotent close — guards against the case where Stop and the poll
	// goroutine race to close stopCh.
	r.stopOnce.Do(func() { close(r.stopCh) })
	<-stoppedCh
}

// GetCertificate returns the most recently loaded certificate. Suitable for
// tls.Config.GetCertificate. Returns ErrCertReloaderEmpty when called on a
// CertReloader whose initial load has not run (i.e., a zero-value struct).
func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := r.cert.Load()
	if cert == nil {
		return nil, ErrCertReloaderEmpty
	}
	return cert, nil
}

func (r *CertReloader) poll(ctx context.Context) {
	defer func() {
		// Transition state to stopped if the poll exited on its own
		// (e.g. ctx cancellation) so a subsequent Start sees the
		// reloader as truly stopped rather than spuriously running.
		// If Stop already transitioned, this is a no-op.
		r.mu.Lock()
		if r.state == reloaderRunning {
			r.state = reloaderStopped
		}
		r.mu.Unlock()
		close(r.stoppedCh)
	}()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = r.reloadOnce()
		}
	}
}

// reloadOnce performs a single poll-and-reload cycle. Exposed unexported for
// tests; production code reaches it only via the polling goroutine.
func (r *CertReloader) reloadOnce() error {
	certInfo, err := os.Stat(r.certFile)
	if err != nil {
		r.logger.Warn("cert reload: stat cert failed; keeping previous cert",
			"file", r.certFile, "error", err)
		return err
	}
	keyInfo, err := os.Stat(r.keyFile)
	if err != nil {
		r.logger.Warn("cert reload: stat key failed; keeping previous cert",
			"file", r.keyFile, "error", err)
		return err
	}

	certSig := fileSig{certInfo.ModTime(), certInfo.Size()}
	keySig := fileSig{keyInfo.ModTime(), keyInfo.Size()}

	r.sigMu.Lock()
	unchanged := certSig == r.lastCertSig && keySig == r.lastKeySig
	r.sigMu.Unlock()
	if unchanged {
		return nil
	}

	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		r.logger.Warn("cert reload: load failed; keeping previous cert",
			"cert", r.certFile, "key", r.keyFile, "error", err)
		return err
	}

	r.cert.Store(&cert)
	r.sigMu.Lock()
	r.lastCertSig = certSig
	r.lastKeySig = keySig
	r.sigMu.Unlock()

	r.logger.Info("cert reloaded", "cert", r.certFile, "key", r.keyFile)
	return nil
}

// reloadInterval returns the effective poll interval per WithCertReload's
// normalization rules: zero → default 30s, then floor at 1s.
func (a *App) reloadInterval() time.Duration {
	interval := a.config.CertReloadInterval
	if interval == 0 {
		interval = defaultCertReloadInterval
	}
	if interval < minCertReloadInterval {
		interval = minCertReloadInterval
	}
	return interval
}
