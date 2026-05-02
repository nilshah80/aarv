package aarv

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

// Test seams: replaced via t.Cleanup in unit tests that need to inject
// signal-handling or cert-reload failures (e.g. exercising start-failure
// branches that production code cannot reach naturally). Not exported and
// must not be reassigned at runtime in production code — concurrent
// reassignment is unsynchronized and would race against any in-flight
// listener.
var (
	listenServerSignalNotify = signal.Notify
	listenServerSignalStop   = signal.Stop
	newCertReloader          = func(certFile, keyFile string, interval time.Duration, logger *slog.Logger) (certReloadController, error) {
		return NewCertReloader(certFile, keyFile, interval, logger)
	}
)

type certReloadController interface {
	Start(context.Context) error
	Stop()
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
}

// handleError routes framework errors through OnError hooks and the configured error handler.
func (a *App) handleError(c *Context, err error) {
	if c != nil && a.hasOnError {
		prev := c.hookErr
		c.hookErr = err
		_ = a.hooks.run(OnError, c)
		c.hookErr = prev
	}
	if a.errorHandler != nil {
		a.errorHandler(c, err)
		return
	}
	a.defaultErrorHandler(c, err)
}

func (a *App) defaultErrorHandler(c *Context, err error) {
	var appErr *AppError
	var valErr *ValidationErrors
	var bindErr *BindError

	switch {
	case errors.As(err, &valErr):
		_ = c.JSON(http.StatusUnprocessableEntity, map[string]any{
			"error":      "validation_failed",
			"message":    "Request validation failed",
			"details":    valErr.Errors,
			"request_id": c.RequestID(),
		})
	case errors.As(err, &bindErr):
		_ = c.JSON(http.StatusBadRequest, errorResponse{
			Error:     "bad_request",
			Message:   bindErr.Error(),
			RequestID: c.RequestID(),
		})
	case errors.As(err, &appErr):
		resp := errorResponse{
			Error:     appErr.Code(),
			Message:   appErr.Message(),
			Detail:    appErr.Detail(),
			RequestID: c.RequestID(),
		}
		if appErr.Internal() != nil {
			a.logger.Error("internal error",
				"error", appErr.Internal(),
				"request_id", c.RequestID(),
			)
		}
		_ = c.JSON(appErr.StatusCode(), resp)
	default:
		a.logger.Error("unhandled error",
			"error", err,
			"request_id", c.RequestID(),
		)
		_ = c.JSON(http.StatusInternalServerError, errorResponse{
			Error:     "internal_error",
			Message:   "Internal server error",
			RequestID: c.RequestID(),
		})
	}
}

// Listen starts the HTTP server and blocks until shutdown.
func (a *App) Listen(addr string) error {
	if a.config.CertReloadEnabled {
		a.logger.Warn("WithCertReload set but Listen is plain HTTP; reload has no effect")
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           a,
		ReadTimeout:       a.config.ReadTimeout,
		ReadHeaderTimeout: a.config.ReadHeaderTimeout,
		WriteTimeout:      a.config.WriteTimeout,
		IdleTimeout:       a.config.IdleTimeout,
		MaxHeaderBytes:    a.config.MaxHeaderBytes,
	}
	return a.ListenServer(server, func() error {
		return server.ListenAndServe()
	}, "HTTP")
}

// ListenTLS starts the HTTPS server with TLS.
//
// When WithCertReload is enabled, the cert/key paths are watched and
// re-loaded on change; ListenTLS returns ErrCertReloadConflict before
// serving if the caller-supplied TLSConfig already has GetCertificate set.
// The initial cert/key load runs synchronously before serving begins; a
// load failure returns the wrapped error and no traffic is served.
func (a *App) ListenTLS(addr, certFile, keyFile string) error {
	tlsCfg := a.effectiveTLSConfig(false)

	cleanup, certArg, keyArg, err := a.setupCertReload(tlsCfg, certFile, keyFile)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           a,
		TLSConfig:         tlsCfg,
		ReadTimeout:       a.config.ReadTimeout,
		ReadHeaderTimeout: a.config.ReadHeaderTimeout,
		WriteTimeout:      a.config.WriteTimeout,
		IdleTimeout:       a.config.IdleTimeout,
		MaxHeaderBytes:    a.config.MaxHeaderBytes,
	}
	return a.listenServerWithCleanup(server, func() error {
		return server.ListenAndServeTLS(certArg, keyArg)
	}, "HTTPS", cleanup)
}

// ListenMutualTLS starts the server with mutual TLS authentication.
//
// When WithCertReload is enabled, the server cert/key paths are watched
// and re-loaded on change. The clientCAFile is loaded once at startup —
// reload does not extend to the client CA. Returns ErrCertReloadConflict
// if the caller-supplied TLSConfig already has GetCertificate set.
func (a *App) ListenMutualTLS(addr, certFile, keyFile, clientCAFile string) error {
	clientCACert, err := os.ReadFile(clientCAFile)
	if err != nil {
		return fmt.Errorf("aarv: failed to read client CA: %w", err)
	}

	tlsCfg := a.effectiveTLSConfig(true)

	// Use crypto/x509 to parse the client CA
	pool := tlsCfg.ClientCAs
	if pool == nil {
		pool = newCertPool()
	}
	pool.AppendCertsFromPEM(clientCACert)
	tlsCfg.ClientCAs = pool

	cleanup, certArg, keyArg, err := a.setupCertReload(tlsCfg, certFile, keyFile)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           a,
		TLSConfig:         tlsCfg,
		ReadTimeout:       a.config.ReadTimeout,
		ReadHeaderTimeout: a.config.ReadHeaderTimeout,
		WriteTimeout:      a.config.WriteTimeout,
		IdleTimeout:       a.config.IdleTimeout,
		MaxHeaderBytes:    a.config.MaxHeaderBytes,
	}
	return a.listenServerWithCleanup(server, func() error {
		return server.ListenAndServeTLS(certArg, keyArg)
	}, "mTLS", cleanup)
}

// setupCertReload, when WithCertReload is enabled, validates that the
// caller's TLSConfig has no GetCertificate (returning ErrCertReloadConflict
// otherwise), constructs and starts a CertReloader, wires its
// GetCertificate into tlsCfg, and returns a cleanup that stops the
// reloader. When reload is disabled, all returned values pass through
// unchanged.
//
// On success the returned certArg/keyArg are empty strings when reload is
// active (the stdlib accepts empty filenames when GetCertificate is set),
// or the originals otherwise.
func (a *App) setupCertReload(tlsCfg *tls.Config, certFile, keyFile string) (cleanup func(), certArg, keyArg string, err error) {
	if !a.config.CertReloadEnabled {
		return nil, certFile, keyFile, nil
	}

	if a.config.TLSConfig != nil && a.config.TLSConfig.GetCertificate != nil {
		return nil, "", "", ErrCertReloadConflict
	}

	reloader, err := newCertReloader(certFile, keyFile, a.reloadInterval(), a.logger)
	if err != nil {
		return nil, "", "", err
	}
	if startErr := reloader.Start(context.Background()); startErr != nil {
		return nil, "", "", startErr
	}

	tlsCfg.GetCertificate = reloader.GetCertificate
	return reloader.Stop, "", "", nil
}

// Shutdown gracefully shuts down the server.
func (a *App) Shutdown(ctx context.Context) error {
	server := a.getServer()
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

// ListenServer runs srv with the full aarv lifecycle: registers the server,
// runs OnStartup hooks, finalizes dispatch chains, optionally prints the
// startup banner, launches serve in a goroutine, traps SIGINT/SIGTERM, then
// runs OnShutdown hooks (registry + legacy) and calls server.Shutdown bounded
// by ShutdownTimeout.
//
// Plugin packages constructing a custom *http.Server (e.g. autocert, h2c)
// should call ListenServer rather than reimplementing the lifecycle, so
// app.Shutdown(ctx) and OnStartup/OnShutdown hooks behave consistently.
//
// Custom serve func contract: when a signal arrives, ListenServer calls
// srv.Shutdown(ctx) and waits for the serve goroutine to return. If the
// serve func does not honor srv.Shutdown (or its own cancellation signal),
// ListenServer returns context.DeadlineExceeded after ShutdownTimeout and
// the serve goroutine continues running until process exit. Any custom
// serve func wired through ListenServer MUST return when srv.Shutdown is
// called, otherwise it leaks a goroutine on graceful shutdown.
//
// Lifecycle contract: routes and middleware must be registered before
// ListenServer is called. OnStartup hooks run before final dispatch-chain
// readiness (ensureReady). OnShutdown hooks run after any serve return —
// nil, error, or signal.
//
// The protocol argument is display text only (banner + startup log). It has
// no transport semantics.
//
// Returns ErrNilServer / ErrNilServeFunc on nil arguments. Returns the
// startup hook error if OnStartup fails (in which case serve and OnShutdown
// do not run).
func (a *App) ListenServer(srv *http.Server, serve func() error, protocol string) error {
	return a.listenServerWithCleanup(srv, serve, protocol, nil)
}

// listenServerWithCleanup is the unexported variant that accepts an optional
// cleanup closure. cleanup runs AFTER the listener has fully drained — i.e.
// after OnShutdown hooks have run and srv.Shutdown has returned — so
// transport-coupled resources (e.g. CertReloader.Stop) can release safely
// without racing in-flight TLS handshakes that may still call GetCertificate.
//
// OnShutdown hooks themselves run BEFORE srv.Shutdown, preserving pre-PR-12.0
// hook semantics: hooks see a still-open listener and may, e.g., emit
// "shutting down" signals or drain external queues that depend on the
// service still accepting requests.
//
// If OnStartup fails, cleanup is invoked before the error is returned so
// that any resource registered via setupCertReload (or analogous helpers)
// does not leak.
func (a *App) listenServerWithCleanup(srv *http.Server, serve func() error, protocol string, cleanup func()) error {
	if srv == nil {
		if cleanup != nil {
			cleanup()
		}
		return ErrNilServer
	}
	if serve == nil {
		if cleanup != nil {
			cleanup()
		}
		return ErrNilServeFunc
	}

	a.setServer(srv)
	a.applyServerTLSPolicy(srv)

	// OnStartup hooks run before ensureReady so they may still register
	// routes or middleware. Sort by priority first — ensureReady's finalize()
	// has not run yet at this point, so hooks would otherwise fire in
	// registration order rather than priority order (unlike every other
	// phase, which is sorted by ensureReady before any request fires).
	a.hooks.sortPhase(OnStartup)
	if err := a.hooks.run(OnStartup, nil); err != nil {
		// Don't leak transport-coupled resources (e.g. cert reloader
		// goroutines) when startup aborts before serving.
		if cleanup != nil {
			cleanup()
		}
		return fmt.Errorf("aarv: startup hook failed: %w", err)
	}

	// Finalize hooks and build dispatch chains before serving begins. This
	// gives sync.Once happens-before edges to both the request goroutines
	// (which call ensureReady themselves) and to the OnShutdown read below,
	// closing the race that fires when Shutdown is triggered while a request
	// is in flight.
	a.ensureReady()

	if a.config.Banner {
		a.printBanner(srv.Addr, protocol)
	}

	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("starting server", "addr", srv.Addr, "protocol", protocol)
		if err := serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	quit := make(chan os.Signal, 1)
	listenServerSignalNotify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer listenServerSignalStop(quit)

	ctx, cancel := context.WithTimeout(context.Background(), a.config.ShutdownTimeout)
	defer cancel()

	var (
		serveErr       error
		signalReceived bool
	)
	select {
	case sig := <-quit:
		signalReceived = true
		a.logger.Info("shutdown signal received", "signal", sig.String())
	case err := <-errCh:
		if err != nil {
			serveErr = fmt.Errorf("aarv: server error: %w", err)
		}
	}

	// Run OnShutdown hooks while the listener may still be open. This
	// matches pre-PR-12.0 behavior: hooks may emit a "shutting down" signal
	// or drain external dependencies that themselves rely on the service
	// still accepting traffic.
	if len(a.hooks.hooks[OnShutdown]) > 0 {
		// OnShutdown hooks receive nil context (they can use the shutdown ctx via closure)
		_ = a.hooks.run(OnShutdown, nil)
	}
	for _, hook := range a.shutdownHooks {
		if err := hook(ctx); err != nil {
			a.logger.Error("shutdown hook error", "error", err)
		}
	}

	// Drain the transport. On the signal path this is the first call; on
	// the serve-return path it's a no-op against an already-shut-down
	// server (returns http.ErrServerClosed, which we filter).
	shutdownErr := srv.Shutdown(ctx)
	if signalReceived {
		// Wait for the serve goroutine to actually return so cleanup
		// observes a closed listener. Do not wait forever for custom serve
		// functions that are not actually tied to srv.
		select {
		case err := <-errCh:
			if err != nil {
				serveErr = fmt.Errorf("aarv: server error: %w", err)
			}
		case <-ctx.Done():
			if shutdownErr == nil {
				shutdownErr = ctx.Err()
			}
		}
	}

	// Transport-coupled cleanup runs last so resources like CertReloader.Stop
	// cannot race against handshakes in flight before srv.Shutdown drained.
	if cleanup != nil {
		cleanup()
	}

	if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) && serveErr == nil {
		return shutdownErr
	}
	return serveErr
}

func (a *App) applyServerTLSPolicy(srv *http.Server) {
	if !a.config.DisableHTTP2 {
		return
	}
	// net/http's ServeTLS may append "h2" back into TLSConfig.NextProtos
	// unless HTTP/2 is disabled at the server policy layer too. An empty
	// TLSNextProto map is the pre-Go-1.24 compatible opt-out mechanism.
	//
	// Respect a caller-supplied TLSNextProto: if the user set it themselves
	// they own the upgrade-protocol policy (custom protocol upgrades, etc).
	// Setting WithDisableHTTP2 alongside a non-nil TLSNextProto means the
	// user is responsible for ensuring "h2" is not in their map.
	if srv.TLSNextProto != nil {
		return
	}
	srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
}

func (a *App) printBanner(addr, protocol string) {
	fmt.Printf("\n")
	fmt.Printf("     _   _   ___ __   __\n")
	fmt.Printf("    / \\ / \\ | _ \\\\ \\ / /\n")
	fmt.Printf("   / _ \\/ _ \\|   / \\ V / \n")
	fmt.Printf("  /_/ \\_\\_/ \\_\\_|_\\  \\_/  \n")
	fmt.Printf("\n")
	fmt.Printf("  The peaceful sound of minimal Go.\n")
	fmt.Printf("\n")
	fmt.Printf("  %s server on %s\n", protocol, addr)
	fmt.Printf("  Go %s | %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Printf("\n")
}
