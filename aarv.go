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
	"sync"
	"syscall"
)

// App is the central framework instance.
type App struct {
	mux          *http.ServeMux
	server       *http.Server
	config       *Config
	codec        Codec
	errorHandler ErrorHandler
	logger       *slog.Logger

	// Pool
	ctxPool sync.Pool

	// Middleware
	globalMiddleware []Middleware
	handler          http.Handler // pre-built middleware chain (built on first request)
	handlerOnce      sync.Once

	// Hooks
	hooks        *hookRegistry
	hasOnRequest bool // fast check to skip empty hook iteration

	// Plugins
	plugins    []pluginEntry
	decorators map[string]any

	// Routes (for introspection)
	routes []RouteInfo

	// Shutdown
	shutdownHooks []ShutdownHook
}

// New creates a new App with the given options.
func New(opts ...Option) *App {
	a := &App{
		mux:        http.NewServeMux(),
		config:     defaultConfig(),
		codec:      StdJSONCodec{},
		logger:     slog.New(slog.NewTextHandler(os.Stdout, nil)),
		hooks:      newHookRegistry(),
		decorators: make(map[string]any),
	}

	a.ctxPool = sync.Pool{
		New: func() any {
			return &Context{
				store: make(map[string]any, 4),
			}
		},
	}

	// Default error handler
	a.errorHandler = a.defaultErrorHandler

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// --- Route Registration ---

func (a *App) addRoute(method, pattern string, handler any, opts ...RouteOption) *App {
	rc := &routeConfig{}
	for _, opt := range opts {
		opt(rc)
	}

	h := adaptHandler(handler)

	var httpHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := r.Context().Value(ctxKey{}).(*Context)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Update req to the mux-dispatched request so PathValue works
		// even when middleware created new request objects via WithContext.
		ctx.req = r
		if err := h(ctx); err != nil {
			ctx.app.handleError(ctx, err)
		}
	})

	if len(rc.middleware) > 0 {
		httpHandler = buildChain(httpHandler, rc.middleware)
	}

	muxPattern := method + " " + pattern
	a.mux.Handle(muxPattern, httpHandler)

	a.routes = append(a.routes, RouteInfo{
		Method:      method,
		Pattern:     pattern,
		Name:        rc.name,
		Tags:        rc.tags,
		Description: rc.description,
		Deprecated:  rc.deprecated,
	})

	return a
}

func (a *App) Get(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("GET", pattern, handler, opts...)
}

func (a *App) Post(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("POST", pattern, handler, opts...)
}

func (a *App) Put(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("PUT", pattern, handler, opts...)
}

func (a *App) Delete(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("DELETE", pattern, handler, opts...)
}

func (a *App) Patch(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("PATCH", pattern, handler, opts...)
}

func (a *App) Head(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("HEAD", pattern, handler, opts...)
}

func (a *App) Options(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("OPTIONS", pattern, handler, opts...)
}

func (a *App) Any(pattern string, handler any, opts ...RouteOption) *App {
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		a.addRoute(m, pattern, handler, opts...)
	}
	return a
}

// Use adds global middleware.
func (a *App) Use(middlewares ...Middleware) *App {
	a.globalMiddleware = append(a.globalMiddleware, middlewares...)
	return a
}

// Group creates a route group with a prefix.
func (a *App) Group(prefix string, fn func(g *RouteGroup)) *App {
	group := &RouteGroup{
		mux:    http.NewServeMux(),
		prefix: prefix,
		app:    a,
	}

	fn(group)

	var handler http.Handler = group.mux
	if len(group.middleware) > 0 {
		handler = buildChain(handler, group.middleware)
	}

	p := prefix
	if len(p) > 0 && p[len(p)-1] != '/' {
		p += "/"
	}
	a.mux.Handle(p, http.StripPrefix(prefix, handler))

	return a
}

// Mount mounts an http.Handler at the given prefix.
func (a *App) Mount(prefix string, handler http.Handler) *App {
	p := prefix
	if len(p) > 0 && p[len(p)-1] != '/' {
		p += "/"
	}
	a.mux.Handle(p, http.StripPrefix(prefix, handler))
	return a
}

// Routes returns all registered routes.
func (a *App) Routes() []RouteInfo {
	return a.routes
}

// --- Hooks ---

// AddHook registers a lifecycle hook.
func (a *App) AddHook(phase HookPhase, fn HookFunc) *App {
	a.hooks.add(phase, fn)
	return a
}

// AddHookWithPriority registers a hook with priority (lower = runs first).
func (a *App) AddHookWithPriority(phase HookPhase, priority int, fn HookFunc) *App {
	a.hooks.addWithPriority(phase, priority, fn)
	return a
}

// OnShutdown registers a shutdown hook.
func (a *App) OnShutdown(fn ShutdownHook) *App {
	a.shutdownHooks = append(a.shutdownHooks, fn)
	return a
}

// --- Plugin Registration ---

// Register registers a plugin with the framework.
func (a *App) Register(plugin Plugin, opts ...PluginOption) *App {
	// Check dependencies
	if dep, ok := plugin.(PluginWithDeps); ok {
		for _, d := range dep.Dependencies() {
			if !a.hasPlugin(d) {
				panic(fmt.Sprintf("aarv: plugin %q requires %q to be registered first", plugin.Name(), d))
			}
		}
	}

	prefix := ""
	for _, opt := range opts {
		opt(&prefix)
	}

	pc := newPluginContext(a, prefix)
	if err := plugin.Register(pc); err != nil {
		panic(fmt.Sprintf("aarv: plugin %q registration failed: %v", plugin.Name(), err))
	}

	a.plugins = append(a.plugins, pluginEntry{name: plugin.Name(), plugin: plugin})
	return a
}

func (a *App) hasPlugin(name string) bool {
	for _, p := range a.plugins {
		if p.name == name {
			return true
		}
	}
	return false
}

// PluginOption configures plugin registration.
type PluginOption func(*string)

// WithPrefix sets the plugin's route prefix.
func WithPrefix(prefix string) PluginOption {
	return func(s *string) { *s = prefix }
}

// buildHandler pre-builds the middleware chain. Called once.
func (a *App) buildHandler() http.Handler {
	return buildChain(a.mux, a.globalMiddleware)
}

// --- ServeHTTP (the main entry point) ---

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Pre-build middleware chain once (thread-safe)
	a.handlerOnce.Do(func() {
		a.hooks.finalize()
		a.handler = a.buildHandler()
		a.hasOnRequest = len(a.hooks.hooks[OnRequest]) > 0
	})

	// Acquire context from pool
	c := a.ctxPool.Get().(*Context)
	c.reset(w, r)
	c.app = a

	// Embed aarv context in request context for middleware access
	// Use context.WithValue on the existing context — avoids cloning *http.Request
	ctx := context.WithValue(r.Context(), ctxKey{}, c)
	r = r.WithContext(ctx)
	c.req = r

	defer func() {
		a.ctxPool.Put(c)
	}()

	// Run OnRequest hooks (skip if none registered)
	if a.hasOnRequest {
		if err := a.hooks.run(OnRequest, c); err != nil {
			a.handleError(c, err)
			return
		}
	}

	// Execute pre-built handler chain
	a.handler.ServeHTTP(w, r)
}

// --- Error Handling ---

func (a *App) handleError(c *Context, err error) {
	if a.errorHandler != nil {
		a.errorHandler(c, err)
		return
	}
	a.defaultErrorHandler(c, err)
}

func (a *App) defaultErrorHandler(c *Context, err error) {
	// Run OnError hooks
	_ = a.hooks.run(OnError, c)

	var appErr *AppError
	var valErr *ValidationErrors
	var bindErr *BindError

	switch {
	case errors.As(err, &valErr):
		c.JSON(http.StatusUnprocessableEntity, map[string]any{
			"error":      "validation_failed",
			"message":    "Request validation failed",
			"details":    valErr.Errors,
			"request_id": c.RequestID(),
		})
	case errors.As(err, &bindErr):
		c.JSON(http.StatusBadRequest, errorResponse{
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
		c.JSON(appErr.StatusCode(), resp)
	default:
		a.logger.Error("unhandled error",
			"error", err,
			"request_id", c.RequestID(),
		)
		c.JSON(http.StatusInternalServerError, errorResponse{
			Error:     "internal_error",
			Message:   "Internal server error",
			RequestID: c.RequestID(),
		})
	}
}

// --- Server Lifecycle ---

// Listen starts the HTTP server and blocks until shutdown.
func (a *App) Listen(addr string) error {
	a.server = &http.Server{
		Addr:              addr,
		Handler:           a,
		ReadTimeout:       a.config.ReadTimeout,
		ReadHeaderTimeout: a.config.ReadHeaderTimeout,
		WriteTimeout:      a.config.WriteTimeout,
		IdleTimeout:       a.config.IdleTimeout,
		MaxHeaderBytes:    a.config.MaxHeaderBytes,
	}

	// Run OnStartup hooks
	if err := a.hooks.run(OnStartup, nil); err != nil {
		return fmt.Errorf("aarv: startup hook failed: %w", err)
	}

	if a.config.Banner {
		a.printBanner(addr, "HTTP")
	}

	return a.listenAndShutdown(func() error {
		return a.server.ListenAndServe()
	})
}

// ListenTLS starts the HTTPS server with TLS.
func (a *App) ListenTLS(addr, certFile, keyFile string) error {
	tlsCfg := a.config.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	a.server = &http.Server{
		Addr:              addr,
		Handler:           a,
		TLSConfig:         tlsCfg,
		ReadTimeout:       a.config.ReadTimeout,
		ReadHeaderTimeout: a.config.ReadHeaderTimeout,
		WriteTimeout:      a.config.WriteTimeout,
		IdleTimeout:       a.config.IdleTimeout,
		MaxHeaderBytes:    a.config.MaxHeaderBytes,
	}

	if err := a.hooks.run(OnStartup, nil); err != nil {
		return fmt.Errorf("aarv: startup hook failed: %w", err)
	}

	if a.config.Banner {
		a.printBanner(addr, "HTTPS")
	}

	return a.listenAndShutdown(func() error {
		return a.server.ListenAndServeTLS(certFile, keyFile)
	})
}

// ListenMutualTLS starts the server with mutual TLS authentication.
func (a *App) ListenMutualTLS(addr, certFile, keyFile, clientCAFile string) error {
	clientCACert, err := os.ReadFile(clientCAFile)
	if err != nil {
		return fmt.Errorf("aarv: failed to read client CA: %w", err)
	}

	tlsCfg := a.config.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}
	tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert

	// Use crypto/x509 to parse the client CA
	pool := tlsCfg.ClientCAs
	if pool == nil {
		pool = newCertPool()
	}
	pool.AppendCertsFromPEM(clientCACert)
	tlsCfg.ClientCAs = pool

	a.server = &http.Server{
		Addr:              addr,
		Handler:           a,
		TLSConfig:         tlsCfg,
		ReadTimeout:       a.config.ReadTimeout,
		ReadHeaderTimeout: a.config.ReadHeaderTimeout,
		WriteTimeout:      a.config.WriteTimeout,
		IdleTimeout:       a.config.IdleTimeout,
		MaxHeaderBytes:    a.config.MaxHeaderBytes,
	}

	if a.config.Banner {
		a.printBanner(addr, "mTLS")
	}

	return a.listenAndShutdown(func() error {
		return a.server.ListenAndServeTLS(certFile, keyFile)
	})
}

// Shutdown gracefully shuts down the server.
func (a *App) Shutdown(ctx context.Context) error {
	if a.server == nil {
		return nil
	}
	return a.server.Shutdown(ctx)
}

func (a *App) listenAndShutdown(serve func() error) error {
	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("server started", "addr", a.server.Addr)
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

	// Run shutdown hooks
	for _, hook := range a.shutdownHooks {
		if err := hook(ctx); err != nil {
			a.logger.Error("shutdown hook error", "error", err)
		}
	}

	return a.server.Shutdown(ctx)
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
