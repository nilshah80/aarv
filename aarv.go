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
	"strings"
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

	// Custom handlers
	notFoundHandler         HandlerFunc
	methodNotAllowedHandler HandlerFunc

	// Pool
	ctxPool sync.Pool

	// Middleware
	globalMiddleware []Middleware
	handler          http.Handler // pre-built middleware chain (built on first request)
	handlerOnce      sync.Once

	// Hooks
	hooks        *hookRegistry
	hasOnRequest bool // fast check to skip empty hook iteration
	hasOnSend    bool // fast check for OnSend hooks

	// Plugins
	plugins    []pluginEntry
	decorators map[string]any

	// Routes (for introspection)
	routes      []RouteInfo
	routesByKey map[string]struct{} // "METHOD /path" for 405 detection

	// Shutdown
	shutdownHooks []ShutdownHook
}

// New creates a new App with the given options.
func New(opts ...Option) *App {
	a := &App{
		mux:         http.NewServeMux(),
		config:      defaultConfig(),
		codec:       StdJSONCodec{},
		logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
		hooks:       newHookRegistry(),
		decorators:  make(map[string]any),
		routesByKey: make(map[string]struct{}),
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

	// Default 404 handler
	a.notFoundHandler = func(c *Context) error {
		return c.JSON(http.StatusNotFound, errorResponse{
			Error:     "not_found",
			Message:   "Resource not found",
			RequestID: c.RequestID(),
		})
	}

	// Default 405 handler
	a.methodNotAllowedHandler = func(c *Context) error {
		return c.JSON(http.StatusMethodNotAllowed, errorResponse{
			Error:     "method_not_allowed",
			Message:   "Method not allowed",
			RequestID: c.RequestID(),
		})
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// SetNotFoundHandler sets a custom 404 handler.
func (a *App) SetNotFoundHandler(h HandlerFunc) *App {
	a.notFoundHandler = h
	return a
}

// SetMethodNotAllowedHandler sets a custom 405 handler.
func (a *App) SetMethodNotAllowedHandler(h HandlerFunc) *App {
	a.methodNotAllowedHandler = h
	return a
}

// AcquireContext gets a Context from the pool. Must be followed by ReleaseContext.
func (a *App) AcquireContext(w http.ResponseWriter, r *http.Request) *Context {
	c := a.ctxPool.Get().(*Context)
	c.reset(w, r)
	c.app = a
	return c
}

// ReleaseContext returns a Context to the pool.
func (a *App) ReleaseContext(c *Context) {
	a.ctxPool.Put(c)
}

// --- Route Registration ---

func (a *App) addRoute(method, pattern string, handler any, opts ...RouteOption) *App {
	rc := &routeConfig{}
	for _, opt := range opts {
		opt(rc)
	}

	h := adaptHandler(handler)

	// Apply per-route body limit if configured
	routeBodyLimit := rc.maxBodySize
	if routeBodyLimit == 0 {
		routeBodyLimit = a.config.MaxBodySize
	}

	var httpHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := r.Context().Value(ctxKey{}).(*Context)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Update req/res to the mux-dispatched request so PathValue works
		// and response writes go through middleware wrappers (logger, etag, etc.).
		ctx.req = r
		ctx.res = w

		// Enforce body limit
		if routeBodyLimit > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, routeBodyLimit)
		}

		if err := h(ctx); err != nil {
			ctx.app.handleError(ctx, err)
		}
	})

	if len(rc.middleware) > 0 {
		httpHandler = buildChain(httpHandler, rc.middleware)
	}

	muxPattern := method + " " + pattern
	a.mux.Handle(muxPattern, httpHandler)

	// Track route for 405 detection
	a.routesByKey[muxPattern] = struct{}{}

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

	pc := newPluginContext(a, plugin.Name(), prefix)
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
	// Wrap mux to intercept 404/405 responses
	wrappedMux := &routingMux{
		mux:         a.mux,
		app:         a,
		routesByKey: a.routesByKey,
	}
	return buildChain(wrappedMux, a.globalMiddleware)
}

// routingMux wraps http.ServeMux to intercept 404/405 and call custom handlers.
type routingMux struct {
	mux         *http.ServeMux
	app         *App
	routesByKey map[string]struct{}
}

func (m *routingMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check if any route matches this path with any method
	path := r.URL.Path
	var hasPathMatch bool
	var hasExactMatch bool

	for key := range m.routesByKey {
		parts := strings.SplitN(key, " ", 2)
		if len(parts) != 2 {
			continue
		}
		method, pattern := parts[0], parts[1]
		if matchesPattern(pattern, path) {
			hasPathMatch = true
			if method == r.Method {
				hasExactMatch = true
				break
			}
		}
	}

	// If exact match exists, let mux handle it
	if hasExactMatch {
		m.mux.ServeHTTP(w, r)
		return
	}

	// If path matches but method doesn't -> 405
	if hasPathMatch {
		c, _ := r.Context().Value(ctxKey{}).(*Context)
		if c != nil {
			if err := m.app.methodNotAllowedHandler(c); err != nil {
				m.app.handleError(c, err)
			}
			return
		}
	}

	// Use a probe writer to detect if mux writes a response
	probe := &probeResponseWriter{ResponseWriter: w}
	m.mux.ServeHTTP(probe, r)

	// If mux didn't write anything (404 from ServeMux), call custom 404 handler
	if !probe.written {
		c, _ := r.Context().Value(ctxKey{}).(*Context)
		if c != nil {
			if err := m.app.notFoundHandler(c); err != nil {
				m.app.handleError(c, err)
			}
		}
	}
}

// probeResponseWriter detects if anything was written.
type probeResponseWriter struct {
	http.ResponseWriter
	written bool
}

func (p *probeResponseWriter) WriteHeader(code int) {
	p.written = true
	p.ResponseWriter.WriteHeader(code)
}

func (p *probeResponseWriter) Write(b []byte) (int, error) {
	p.written = true
	return p.ResponseWriter.Write(b)
}

// --- ServeHTTP (the main entry point) ---

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Pre-build middleware chain once (thread-safe)
	a.handlerOnce.Do(func() {
		a.hooks.finalize()
		a.handler = a.buildHandler()
		a.hasOnRequest = len(a.hooks.hooks[OnRequest]) > 0
		a.hasOnSend = len(a.hooks.hooks[OnSend]) > 0
	})

	// Handle trailing slash redirect if enabled
	if a.config.RedirectTrailingSlash && len(r.URL.Path) > 1 {
		if a.shouldRedirectTrailingSlash(r) {
			var target string
			if strings.HasSuffix(r.URL.Path, "/") {
				target = strings.TrimSuffix(r.URL.Path, "/")
			} else {
				target = r.URL.Path + "/"
			}
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
	}

	// Use buffered response writer if OnSend hooks are registered
	var bw *bufferedResponseWriter
	if a.hasOnSend {
		bw = acquireBufferedWriter(w)
		defer func() {
			// Run OnSend hooks before flushing
			if c, ok := r.Context().Value(ctxKey{}).(*Context); ok {
				_ = a.hooks.run(OnSend, c)
			}
			bw.flush()
			releaseBufferedWriter(bw)
		}()
		w = bw
	}

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

	// Execute pre-built handler chain (middleware + routing mux)
	// The routingMux wrapper handles 404/405 detection after middleware runs
	a.handler.ServeHTTP(w, r)

	// Run OnResponse hooks after handler completes
	if len(a.hooks.hooks[OnResponse]) > 0 {
		_ = a.hooks.run(OnResponse, c)
	}
}

// shouldRedirectTrailingSlash checks if we should redirect based on trailing slash.
func (a *App) shouldRedirectTrailingSlash(r *http.Request) bool {
	path := r.URL.Path
	hasSlash := strings.HasSuffix(path, "/")

	// Check if the opposite version exists
	var altPath string
	if hasSlash {
		altPath = strings.TrimSuffix(path, "/")
	} else {
		altPath = path + "/"
	}

	// Check if any route matches the alternative path
	for _, route := range a.routes {
		if matchesPattern(route.Pattern, altPath) {
			return true
		}
	}
	return false
}

// matchesPattern checks if a path matches a pattern (simple matching).
func matchesPattern(pattern, path string) bool {
	// Exact match
	if pattern == path {
		return true
	}

	// Handle wildcard patterns like /users/{id}
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if len(patternParts) != len(pathParts) {
		// Check for ... wildcard at end
		if len(patternParts) > 0 && strings.HasSuffix(patternParts[len(patternParts)-1], "...}") {
			if len(pathParts) >= len(patternParts)-1 {
				// Check prefix matches
				for i := 0; i < len(patternParts)-1; i++ {
					if !matchPatternPart(patternParts[i], pathParts[i]) {
						return false
					}
				}
				return true
			}
		}
		return false
	}

	for i, pp := range patternParts {
		if !matchPatternPart(pp, pathParts[i]) {
			return false
		}
	}
	return true
}

func matchPatternPart(pattern, path string) bool {
	if pattern == path {
		return true
	}
	// {param} matches anything
	if strings.HasPrefix(pattern, "{") && strings.HasSuffix(pattern, "}") {
		return true
	}
	return false
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

	// Disable HTTP/2 if configured
	if a.config.DisableHTTP2 {
		tlsCfg.NextProtos = []string{"http/1.1"}
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

	// Disable HTTP/2 if configured
	if a.config.DisableHTTP2 {
		tlsCfg.NextProtos = []string{"http/1.1"}
	}

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

	if err := a.hooks.run(OnStartup, nil); err != nil {
		return fmt.Errorf("aarv: startup hook failed: %w", err)
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
