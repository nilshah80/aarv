package aarv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

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
	server := &http.Server{
		Addr:              addr,
		Handler:           a,
		ReadTimeout:       a.config.ReadTimeout,
		ReadHeaderTimeout: a.config.ReadHeaderTimeout,
		WriteTimeout:      a.config.WriteTimeout,
		IdleTimeout:       a.config.IdleTimeout,
		MaxHeaderBytes:    a.config.MaxHeaderBytes,
	}
	a.setServer(server)

	// Run OnStartup hooks
	if err := a.hooks.run(OnStartup, nil); err != nil {
		return fmt.Errorf("aarv: startup hook failed: %w", err)
	}

	if a.config.Banner {
		a.printBanner(addr, "HTTP")
	}

	return a.listenAndShutdown(server, func() error {
		return server.ListenAndServe()
	})
}

// ListenTLS starts the HTTPS server with TLS.
func (a *App) ListenTLS(addr, certFile, keyFile string) error {
	tlsCfg := a.effectiveTLSConfig(false)

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
	a.setServer(server)

	if err := a.hooks.run(OnStartup, nil); err != nil {
		return fmt.Errorf("aarv: startup hook failed: %w", err)
	}

	if a.config.Banner {
		a.printBanner(addr, "HTTPS")
	}

	return a.listenAndShutdown(server, func() error {
		return server.ListenAndServeTLS(certFile, keyFile)
	})
}

// ListenMutualTLS starts the server with mutual TLS authentication.
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
	a.setServer(server)

	if err := a.hooks.run(OnStartup, nil); err != nil {
		return fmt.Errorf("aarv: startup hook failed: %w", err)
	}

	if a.config.Banner {
		a.printBanner(addr, "mTLS")
	}

	return a.listenAndShutdown(server, func() error {
		return server.ListenAndServeTLS(certFile, keyFile)
	})
}

// Shutdown gracefully shuts down the server.
func (a *App) Shutdown(ctx context.Context) error {
	server := a.getServer()
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

func (a *App) listenAndShutdown(server *http.Server, serve func() error) error {
	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("server started", "addr", server.Addr)
		if err := serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		a.logger.Info("shutdown signal received", "signal", sig.String())
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("aarv: server error: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), a.config.ShutdownTimeout)
	defer cancel()

	// Run OnShutdown hooks registered via AddHook
	if len(a.hooks.hooks[OnShutdown]) > 0 {
		// OnShutdown hooks receive nil context (they can use the shutdown ctx via closure)
		_ = a.hooks.run(OnShutdown, nil)
	}

	// Run legacy shutdown hooks registered via OnShutdown()
	for _, hook := range a.shutdownHooks {
		if err := hook(ctx); err != nil {
			a.logger.Error("shutdown hook error", "error", err)
		}
	}

	return server.Shutdown(ctx)
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
