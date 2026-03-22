package aarv

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// buildHandler pre-builds the middleware chain. Called once.
func (a *App) buildHandler() http.Handler {
	// Wrap mux to intercept 404/405 responses
	wrappedMux := &routingMux{
		mux:                 a.mux,
		app:                 a,
		routesByKey:         a.routesByKey,
		routeMethodsExact:   a.routeMethodsExact,
		routeMethodsDynamic: a.routeMethodsDynamic,
		routeHandlerFast:    a.routeHandlerFast,
		directDynamicRoutes: a.directDynamicRoutes,
	}
	return buildChain(wrappedMux, a.globalMiddleware)
}

func (a *App) buildRouteChainFast() {
	globalNative := true
	for _, mw := range a.globalMiddleware {
		if _, ok := nativeMiddlewareFunc(mw); !ok {
			globalNative = false
			break
		}
	}
	if len(a.globalMiddleware) > 0 {
		for method, routes := range a.routeHandlerFast {
			if len(routes) == 0 {
				continue
			}
			if a.routeChainFast[method] == nil {
				a.routeChainFast[method] = make(map[string]http.Handler, len(routes))
			}
			for path, rh := range routes {
				if globalNative {
					if a.routeChainFastNative[method] == nil {
						a.routeChainFastNative[method] = make(map[string]routeRuntimeHandler, len(routes))
					}
					nativeHandler, ok := buildNativeChain(func(ctx *Context) error {
						rh(ctx, ctx.res, ctx.req)
						return nil
					}, a.globalMiddleware)
					if ok {
						a.routeChainFastNative[method][path] = func(ctx *Context, w http.ResponseWriter, r *http.Request) {
							if err := nativeHandler(ctx); err != nil {
								ctx.app.handleError(ctx, err)
							}
						}
						continue
					}
				}
				routeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx, ok := contextFromRequest(r)
					if !ok {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					rh(ctx, w, r)
				})
				a.routeChainFast[method][path] = buildChain(routeHandler, a.globalMiddleware)
			}
		}
	}

	for method, routes := range a.groupRouteHandlers {
		if len(routes) == 0 {
			continue
		}
		if a.groupRouteChainFast[method] == nil {
			a.groupRouteChainFast[method] = make(map[string]http.Handler, len(routes))
		}
		for path, h := range routes {
			if len(a.globalMiddleware) == 0 {
				a.groupRouteChainFast[method][path] = h
			} else {
				a.groupRouteChainFast[method][path] = buildChain(h, a.globalMiddleware)
			}
		}
	}

	for method, routes := range a.groupRouteNative {
		if len(routes) == 0 {
			continue
		}
		if a.groupRouteChainNative[method] == nil {
			a.groupRouteChainNative[method] = make(map[string]routeRuntimeHandler, len(routes))
		}
		for path, rh := range routes {
			if len(a.globalMiddleware) == 0 {
				a.groupRouteChainNative[method][path] = rh
				continue
			}
			if !globalNative {
				continue
			}
			nativeHandler, ok := buildNativeChain(func(ctx *Context) error {
				rh(ctx, ctx.res, ctx.req)
				return nil
			}, a.globalMiddleware)
			if ok {
				a.groupRouteChainNative[method][path] = func(ctx *Context, w http.ResponseWriter, r *http.Request) {
					if err := nativeHandler(ctx); err != nil {
						ctx.app.handleError(ctx, err)
					}
				}
			}
		}
	}

	for method, routes := range a.groupDynamicHandlers {
		if len(routes) == 0 {
			continue
		}
		if len(a.globalMiddleware) == 0 {
			a.groupDynamicChainFast[method] = append([]directDynamicHTTPRoute(nil), routes...)
			continue
		}
		out := make([]directDynamicHTTPRoute, 0, len(routes))
		for _, route := range routes {
			out = append(out, directDynamicHTTPRoute{
				pattern: route.pattern,
				handler: buildChain(route.handler, a.globalMiddleware),
			})
		}
		a.groupDynamicChainFast[method] = out
	}

	for method, routes := range a.groupDynamicNative {
		if len(routes) == 0 {
			continue
		}
		if len(a.globalMiddleware) == 0 {
			a.groupDynamicChainNative[method] = append([]directDynamicRoute(nil), routes...)
			continue
		}
		if !globalNative {
			continue
		}
		out := make([]directDynamicRoute, 0, len(routes))
		for _, route := range routes {
			rh := route.handler
			nativeHandler, _ := buildNativeChain(func(ctx *Context) error {
				rh(ctx, ctx.res, ctx.req)
				return nil
			}, a.globalMiddleware)
			out = append(out, directDynamicRoute{
				pattern: route.pattern,
				handler: func(ctx *Context, w http.ResponseWriter, r *http.Request) {
					if err := nativeHandler(ctx); err != nil {
						ctx.app.handleError(ctx, err)
					}
				},
			})
		}
		if len(out) > 0 {
			a.groupDynamicChainNative[method] = out
		}
	}
}

type routeRuntimeHandler func(*Context, http.ResponseWriter, *http.Request)

// routingMux wraps http.ServeMux to intercept 404/405 and call custom handlers.
type routingMux struct {
	mux                 *http.ServeMux
	app                 *App
	routesByKey         map[string]struct{}
	routeMethodsExact   map[string]map[string]struct{}
	routeMethodsDynamic []dynamicRouteMethods
	routeHandlerFast    map[string]map[string]routeRuntimeHandler
	directDynamicRoutes map[string][]directDynamicRoute
}

type dynamicRouteMethods struct {
	patternString string
	pattern       directPattern
	methods       map[string]struct{}
}

func (m *routingMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c, ok := contextFromRequest(r); ok {
		if methods := m.routeHandlerFast[r.Method]; methods != nil {
			if rh, ok := methods[r.URL.Path]; ok {
				rh(c, w, r)
				return
			}
		}
		for _, route := range m.directDynamicRoutes[r.Method] {
			if route.pattern.match(r.URL.Path, c) {
				route.handler(c, w, r)
				return
			}
		}
	}

	if h, pattern := m.mux.Handler(r); pattern != "" {
		if _, ok := m.routesByKey[pattern]; ok {
			if !strings.Contains(pattern, "{") {
				h.ServeHTTP(w, r)
				return
			}
			m.mux.ServeHTTP(w, r)
			return
		}

		// stdlib-mounted handlers and internal redirect/canonicalization handlers
		// can return a non-empty pattern that is not one of aarv's tracked routes.
		// Preserve the old probe-based behavior for those cases.
		probe := acquireProbeResponseWriter(w)
		defer releaseProbeResponseWriter(probe)
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

	if methods := m.routeMethodsExact[r.URL.Path]; methods != nil {
		if _, ok := methods[r.Method]; !ok {
			c, _ := contextFromRequest(r)
			if c != nil {
				if err := m.app.methodNotAllowedHandler(c); err != nil {
					m.app.handleError(c, err)
				}
				return
			}
		}
	}
	for _, route := range m.routeMethodsDynamic {
		if route.pattern.match(r.URL.Path, nil) {
			if _, ok := route.methods[r.Method]; !ok {
				c, _ := contextFromRequest(r)
				if c != nil {
					if err := m.app.methodNotAllowedHandler(c); err != nil {
						m.app.handleError(c, err)
					}
					return
				}
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

var probeResponseWriterPool = sync.Pool{
	New: func() any { return &probeResponseWriter{} },
}

func acquireProbeResponseWriter(w http.ResponseWriter) *probeResponseWriter {
	p := probeResponseWriterPool.Get().(*probeResponseWriter)
	p.ResponseWriter = w
	p.written = false
	return p
}

func releaseProbeResponseWriter(p *probeResponseWriter) {
	if p == nil {
		return
	}
	p.ResponseWriter = nil
	p.written = false
	probeResponseWriterPool.Put(p)
}

func (p *probeResponseWriter) WriteHeader(code int) {
	p.written = true
	p.ResponseWriter.WriteHeader(code)
}

func (p *probeResponseWriter) Write(b []byte) (int, error) {
	p.written = true
	return p.ResponseWriter.Write(b)
}

// ServeHTTP implements http.Handler for the application.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Pre-build middleware chain once (thread-safe)
	a.handlerOnce.Do(func() {
		a.hooks.finalize()
		a.handler = a.buildHandler()
		a.buildRouteChainFast()
		a.hasOnRequest = len(a.hooks.hooks[OnRequest]) > 0
		a.hasPreRouting = len(a.hooks.hooks[PreRouting]) > 0
		a.hasPreParsing = len(a.hooks.hooks[PreParsing]) > 0
		a.hasPreValidation = len(a.hooks.hooks[PreValidation]) > 0
		a.hasPreHandler = len(a.hooks.hooks[PreHandler]) > 0
		a.hasOnError = len(a.hooks.hooks[OnError]) > 0
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

	// Acquire context once and reuse across fast and middleware paths.
	c := a.ctxPool.Get().(*Context)
	c.reset(w, r)

	// Fast path: no middleware, no hooks — dispatch directly without
	// context bridging or defer overhead.
	if len(a.globalMiddleware) == 0 && !a.hasOnRequest && !a.hasPreRouting && !a.hasOnResponse && !a.hasOnSend {
		if a.serveDirect(c, w, r) {
			a.ctxPool.Put(c)
			return
		}
		// serveDirect missed — fall through to middleware path, reuse c.
	}

	needsRegistryCleanup := false

	// Use buffered response writer if OnSend hooks are registered
	if a.hasOnSend {
		bw := acquireBufferedWriter(w)
		defer func() {
			// Run OnSend hooks before flushing
			_ = a.hooks.run(OnSend, c)
			bw.flush()
			releaseBufferedWriter(bw)
			if needsRegistryCleanup {
				deleteRequestContext(c.req)
			}
			a.ctxPool.Put(c)
		}()
		w = bw
		c.res = w
	} else {
		defer func() {
			if needsRegistryCleanup {
				deleteRequestContext(c.req)
			}
			a.ctxPool.Put(c)
		}()
	}

	// Run OnRequest hooks (skip if none registered)
	if a.hasOnRequest {
		if err := a.hooks.run(OnRequest, c); err != nil {
			a.handleError(c, err)
			return
		}
	}

	if a.hasPreRouting {
		if err := a.hooks.run(PreRouting, c); err != nil {
			a.handleError(c, err)
			return
		}
	}

	if methods := a.routeChainFastNative[r.Method]; methods != nil {
		if rh, ok := methods[r.URL.Path]; ok {
			rh(c, w, r)
			if a.hasOnResponse {
				_ = a.hooks.run(OnResponse, c)
			}
			return
		}
	}
	if methods := a.groupRouteChainNative[r.Method]; methods != nil {
		if rh, ok := methods[r.URL.Path]; ok {
			rh(c, w, r)
			if a.hasOnResponse {
				_ = a.hooks.run(OnResponse, c)
			}
			return
		}
	}
	for _, route := range a.groupDynamicChainNative[r.Method] {
		if route.pattern.match(r.URL.Path, c) {
			route.handler(c, w, r)
			if a.hasOnResponse {
				_ = a.hooks.run(OnResponse, c)
			}
			return
		}
	}

	// Set up context bridge for stdlib middleware/plugin access via FromRequest.
	if a.config.RequestContextBridge {
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, c))
		c.req = r
	} else {
		storeRequestContext(r, c)
		c.req = r
		needsRegistryCleanup = true
	}

	// Execute pre-built handler chain (middleware + routing mux)
	if methods := a.routeChainFast[r.Method]; methods != nil {
		if h, ok := methods[r.URL.Path]; ok {
			h.ServeHTTP(w, r)
			if a.hasOnResponse {
				_ = a.hooks.run(OnResponse, c)
			}
			return
		}
	}
	if methods := a.groupRouteChainFast[r.Method]; methods != nil {
		if h, ok := methods[r.URL.Path]; ok {
			h.ServeHTTP(w, r)
			if a.hasOnResponse {
				_ = a.hooks.run(OnResponse, c)
			}
			return
		}
	}
	for _, route := range a.groupDynamicChainFast[r.Method] {
		if route.pattern.match(r.URL.Path, c) {
			route.handler.ServeHTTP(w, r)
			if a.hasOnResponse {
				_ = a.hooks.run(OnResponse, c)
			}
			return
		}
	}
	a.handler.ServeHTTP(w, r)

	// Run OnResponse hooks after handler completes
	if a.hasOnResponse {
		_ = a.hooks.run(OnResponse, c)
	}
}

func (a *App) serveDirect(c *Context, w http.ResponseWriter, r *http.Request) bool {
	if methods := a.routeHandlerFast[r.Method]; methods != nil {
		if rt, ok := methods[r.URL.Path]; ok {
			rt(c, w, r)
			return true
		}
	}
	if methods := a.groupRouteChainNative[r.Method]; methods != nil {
		if rt, ok := methods[r.URL.Path]; ok {
			rt(c, w, r)
			return true
		}
	}
	for _, route := range a.groupDynamicChainNative[r.Method] {
		if route.pattern.match(r.URL.Path, c) {
			route.handler(c, w, r)
			return true
		}
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

type directDynamicHTTPRoute struct {
	handler http.Handler
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
			if c != nil {
				for i := 0; i < n; i++ {
					c.setDirectPathParam(names[i], values[i])
				}
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
			if c != nil {
				for i := 0; i < n; i++ {
					c.setDirectPathParam(names[i], values[i])
				}
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

	if _, ok := a.redirectSlashExact[altPath]; ok {
		return true
	}
	for _, pattern := range a.redirectSlashDynamic {
		if pattern.match(altPath, nil) {
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
