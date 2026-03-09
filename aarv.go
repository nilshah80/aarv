package aarv

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
)

// App is the central framework instance.
type App struct {
	mux              *http.ServeMux
	server           *http.Server
	serverMu         sync.RWMutex
	config           *Config
	codec            Codec
	codecDecode      func(io.Reader, any) error
	codecEncode      func(io.Writer, any) error
	codecMarshal     func(any) ([]byte, error)
	codecUnmarshal   func([]byte, any) error
	codecContentType string
	errorHandler     ErrorHandler
	logger           *slog.Logger

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
	hooks         *hookRegistry
	hasOnRequest  bool // fast check to skip empty hook iteration
	hasOnResponse bool // fast check for OnResponse hooks
	hasOnSend     bool // fast check for OnSend hooks

	// Plugins
	plugins    []pluginEntry
	decorators map[string]any

	// Routes (for introspection)
	routes              []RouteInfo
	routesByKey         map[string]struct{} // "METHOD /path" for 405 detection
	routeHandlers       map[string]routeRuntimeHandler
	directDynamicRoutes map[string][]directDynamicRoute

	// Shutdown
	shutdownHooks []ShutdownHook
}

// New creates a new App with the given options.
func New(opts ...Option) *App {
	defaultCodec := StdJSONCodec{}
	a := &App{
		mux:                 http.NewServeMux(),
		config:              defaultConfig(),
		logger:              slog.New(slog.NewTextHandler(os.Stdout, nil)),
		hooks:               newHookRegistry(),
		decorators:          make(map[string]any),
		routesByKey:         make(map[string]struct{}),
		routeHandlers:       make(map[string]routeRuntimeHandler),
		directDynamicRoutes: make(map[string][]directDynamicRoute),
	}
	a.setCodec(defaultCodec)

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

// SetNotFoundHandler replaces the default 404 handler used for unmatched routes.
func (a *App) SetNotFoundHandler(h HandlerFunc) *App {
	if h != nil {
		a.notFoundHandler = h
	}
	return a
}

// SetMethodNotAllowedHandler replaces the default 405 handler used for method mismatches.
func (a *App) SetMethodNotAllowedHandler(h HandlerFunc) *App {
	if h != nil {
		a.methodNotAllowedHandler = h
	}
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
	if c == nil {
		return
	}
	a.ctxPool.Put(c)
}

func (a *App) setServer(server *http.Server) {
	a.serverMu.Lock()
	a.server = server
	a.serverMu.Unlock()
}

func (a *App) getServer() *http.Server {
	a.serverMu.RLock()
	defer a.serverMu.RUnlock()
	return a.server
}

func (a *App) effectiveTLSConfig(mutualTLS bool) *tls.Config {
	var tlsCfg *tls.Config
	if a.config.TLSConfig != nil {
		tlsCfg = a.config.TLSConfig.Clone()
	} else {
		tlsCfg = &tls.Config{}
	}

	// Enforce a secure minimum unless the caller already chose something stronger.
	if tlsCfg.MinVersion == 0 || tlsCfg.MinVersion < tls.VersionTLS12 {
		tlsCfg.MinVersion = tls.VersionTLS12
	}

	if mutualTLS {
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	if a.config.DisableHTTP2 {
		tlsCfg.NextProtos = []string{"http/1.1"}
	}

	return tlsCfg
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
		ctx, ok := contextFromRequest(r)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Update req/res to the mux-dispatched request so PathValue works
		// and response writes go through middleware wrappers (logger, etag, etc.).
		ctx.req = r
		ctx.res = w

		// Enforce body limit only when the request length is unknown or can exceed
		// the configured limit. Known-small bodies do not need the wrapper.
		if routeBodyLimit > 0 && r.Body != nil && (r.ContentLength < 0 || r.ContentLength > routeBodyLimit) {
			r.Body = http.MaxBytesReader(w, r.Body, routeBodyLimit)
		}

		if err := h(ctx); err != nil {
			ctx.app.handleError(ctx, err)
		}
	})
	internalHandler := routeRuntimeHandler(func(ctx *Context, w http.ResponseWriter, r *http.Request) {
		ctx.req = r
		ctx.res = w

		if routeBodyLimit > 0 && r.Body != nil && (r.ContentLength < 0 || r.ContentLength > routeBodyLimit) {
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
	if len(rc.middleware) == 0 {
		a.routeHandlers[muxPattern] = internalHandler
		if strings.Contains(pattern, "{") {
			a.directDynamicRoutes[method] = append(a.directDynamicRoutes[method], directDynamicRoute{
				handler: internalHandler,
				pattern: compileDirectPattern(pattern),
			})
		}
	}

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

// Get registers a GET route for the given pattern.
func (a *App) Get(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("GET", pattern, handler, opts...)
}

// Post registers a POST route for the given pattern.
func (a *App) Post(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("POST", pattern, handler, opts...)
}

// Put registers a PUT route for the given pattern.
func (a *App) Put(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("PUT", pattern, handler, opts...)
}

// Delete registers a DELETE route for the given pattern.
func (a *App) Delete(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("DELETE", pattern, handler, opts...)
}

// Patch registers a PATCH route for the given pattern.
func (a *App) Patch(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("PATCH", pattern, handler, opts...)
}

// Head registers a HEAD route for the given pattern.
func (a *App) Head(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("HEAD", pattern, handler, opts...)
}

// Options registers an OPTIONS route for the given pattern.
func (a *App) Options(pattern string, handler any, opts ...RouteOption) *App {
	return a.addRoute("OPTIONS", pattern, handler, opts...)
}

// Any registers the handler for the common HTTP methods on the same pattern.
func (a *App) Any(pattern string, handler any, opts ...RouteOption) *App {
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		a.addRoute(m, pattern, handler, opts...)
	}
	return a
}

// Use appends middleware to the global middleware chain.
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
	a.mux.Handle(p, stripPrefixPreserveContext(prefix, handler))

	return a
}

// Mount mounts a standard library http.Handler below the given prefix.
func (a *App) Mount(prefix string, handler http.Handler) *App {
	p := prefix
	if len(p) > 0 && p[len(p)-1] != '/' {
		p += "/"
	}
	a.mux.Handle(p, stripPrefixPreserveContext(prefix, handler))
	return a
}

// Routes returns the registered route metadata in registration order.
func (a *App) Routes() []RouteInfo {
	return a.routes
}

// --- Hooks ---

// AddHook registers a lifecycle hook.
func (a *App) AddHook(phase HookPhase, fn HookFunc) *App {
	if fn != nil {
		a.hooks.add(phase, fn)
	}
	return a
}

// AddHookWithPriority registers a hook with priority (lower = runs first).
func (a *App) AddHookWithPriority(phase HookPhase, priority int, fn HookFunc) *App {
	if fn != nil {
		a.hooks.addWithPriority(phase, priority, fn)
	}
	return a
}

// OnShutdown registers a shutdown hook.
func (a *App) OnShutdown(fn ShutdownHook) *App {
	if fn != nil {
		a.shutdownHooks = append(a.shutdownHooks, fn)
	}
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
		mux:           a.mux,
		app:           a,
		routesByKey:   a.routesByKey,
		routeHandlers: a.routeHandlers,
	}
	return buildChain(wrappedMux, a.globalMiddleware)
}

type routeRuntimeHandler func(*Context, http.ResponseWriter, *http.Request)

// routingMux wraps http.ServeMux to intercept 404/405 and call custom handlers.
type routingMux struct {
	mux           *http.ServeMux
	app           *App
	routesByKey   map[string]struct{}
	routeHandlers map[string]routeRuntimeHandler
}

func (m *routingMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h, pattern := m.mux.Handler(r); pattern != "" {
		if _, ok := m.routesByKey[pattern]; ok {
			if !strings.Contains(pattern, "{") {
				if c, ok := contextFromRequest(r); ok {
					if rh, ok := m.routeHandlers[pattern]; ok {
						rh(c, w, r)
						return
					}
				}
				h.ServeHTTP(w, r)
				return
			}
			m.mux.ServeHTTP(w, r)
			return
		}

		// stdlib-mounted handlers and internal redirect/canonicalization handlers
		// can return a non-empty pattern that is not one of aarv's tracked routes.
		// Preserve the old probe-based behavior for those cases.
		probe := &probeResponseWriter{ResponseWriter: w}
		m.mux.ServeHTTP(probe, r)
		if !probe.written {
			c, _ := contextFromRequest(r)
			if c != nil {
				if err := m.app.notFoundHandler(c); err != nil {
					m.app.handleError(c, err)
				}
			}
		}
		return
	}

	for key := range m.routesByKey {
		parts := strings.SplitN(key, " ", 2)
		if len(parts) != 2 {
			continue
		}
		if matchesPattern(parts[1], r.URL.Path) {
			c, _ := contextFromRequest(r)
			if c != nil {
				if err := m.app.methodNotAllowedHandler(c); err != nil {
					m.app.handleError(c, err)
				}
				return
			}
			break
		}
	}

	c, _ := contextFromRequest(r)
	if c == nil {
		m.mux.ServeHTTP(w, r)
		return
	}
	if err := m.app.notFoundHandler(c); err != nil {
		m.app.handleError(c, err)
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

// ServeHTTP implements http.Handler for the application.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Pre-build middleware chain once (thread-safe)
	a.handlerOnce.Do(func() {
		a.hooks.finalize()
		a.handler = a.buildHandler()
		a.hasOnRequest = len(a.hooks.hooks[OnRequest]) > 0
		a.hasOnResponse = len(a.hooks.hooks[OnResponse]) > 0
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

	if len(a.globalMiddleware) == 0 && !a.hasOnRequest && !a.hasOnResponse && !a.hasOnSend {
		c := a.ctxPool.Get().(*Context)
		c.reset(w, r)
		c.app = a
		defer a.ctxPool.Put(c)

		if a.serveDirect(c, w, r) {
			return
		}
	}

	var c *Context

	// Use buffered response writer if OnSend hooks are registered
	var bw *bufferedResponseWriter
	if a.hasOnSend {
		bw = acquireBufferedWriter(w)
		defer func() {
			// Run OnSend hooks before flushing
			_ = a.hooks.run(OnSend, c)
			bw.flush()
			releaseBufferedWriter(bw)
		}()
		w = bw
	}

	// Acquire context from pool
	c = a.ctxPool.Get().(*Context)
	c.reset(w, r)
	c.app = a
	r = withFrameworkContext(r, c)
	c.req = r
	c.rootReq = r
	storeRequestContext(r, c)

	defer func() {
		deleteRequestContext(c.rootReq)
		if c.req != c.rootReq {
			deleteRequestContext(c.req)
		}
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
	if a.hasOnResponse {
		_ = a.hooks.run(OnResponse, c)
	}
}

func (a *App) serveDirect(c *Context, w http.ResponseWriter, r *http.Request) bool {
	if rt, ok := a.routeHandlers[r.Method+" "+r.URL.Path]; ok {
		rt(c, w, r)
		return true
	}
	for _, route := range a.directDynamicRoutes[r.Method] {
		if route.pattern.match(r.URL.Path, c) {
			route.handler(c, w, r)
			return true
		}
	}
	h, pattern := a.mux.Handler(r)
	if pattern == "" {
		return false
	}
	if _, ok := a.routesByKey[pattern]; !ok {
		return false
	}
	if !strings.Contains(pattern, "{") {
		req := withFrameworkContext(r, c)
		c.req = req
		h.ServeHTTP(w, req)
		return true
	}
	// Direct dispatch only runs when there is no global middleware or hooks.
	// Dynamic routes still need a request-to-context association so the mux-served
	// handler can resolve the aarv Context, but they do not need request cloning
	// for middleware compatibility on this path.
	storeRequestContext(r, c)
	defer deleteRequestContext(r)
	a.mux.ServeHTTP(w, r)
	return true
}

type directDynamicRoute struct {
	handler routeRuntimeHandler
	pattern directPattern
}

type directPattern struct {
	parts []directPatternPart
}

type directPatternPart struct {
	literal  string
	name     string
	catchAll bool
}

func compileDirectPattern(pattern string) directPattern {
	trimmed := strings.Trim(pattern, "/")
	if trimmed == "" {
		return directPattern{}
	}
	segments := strings.Split(trimmed, "/")
	parts := make([]directPatternPart, 0, len(segments))
	for _, seg := range segments {
		if len(seg) >= 2 && seg[0] == '{' && seg[len(seg)-1] == '}' {
			name := seg[1 : len(seg)-1]
			part := directPatternPart{}
			if strings.HasSuffix(name, "...") {
				part.catchAll = true
				name = strings.TrimSuffix(name, "...")
			}
			part.name = name
			parts = append(parts, part)
			continue
		}
		parts = append(parts, directPatternPart{literal: seg})
	}
	return directPattern{parts: parts}
}

func (p directPattern) match(path string, c *Context) bool {
	remaining := strings.TrimPrefix(path, "/")
	var names [16]string
	var values [16]string
	n := 0
	for i, part := range p.parts {
		if part.catchAll {
			if part.name != "" {
				names[n] = part.name
				values[n] = decodePathValue(remaining)
				n++
			}
			for i := 0; i < n; i++ {
				c.setDirectPathParam(names[i], values[i])
			}
			return true
		}
		if remaining == "" {
			return false
		}
		seg := remaining
		if idx := strings.IndexByte(remaining, '/'); idx >= 0 {
			seg = remaining[:idx]
			remaining = remaining[idx+1:]
		} else {
			remaining = ""
		}
		if part.name != "" {
			names[n] = part.name
			values[n] = decodePathValue(seg)
			n++
		} else if part.literal != seg {
			return false
		}
		if i == len(p.parts)-1 {
			if remaining != "" {
				return false
			}
			for i := 0; i < n; i++ {
				c.setDirectPathParam(names[i], values[i])
			}
			return true
		}
	}
	return remaining == ""
}

func decodePathValue(seg string) string {
	if !strings.Contains(seg, "%") {
		return seg
	}
	decoded, err := url.PathUnescape(seg)
	if err != nil {
		return seg
	}
	return decoded
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
