package aarv

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// RouteInfo describes a registered route for introspection.
type RouteInfo struct {
	// Method is the HTTP method registered for the route.
	Method string `json:"method"`
	// Pattern is the path pattern registered for the route.
	Pattern string `json:"pattern"`
	// Name is the optional application-defined route name.
	Name string `json:"name,omitempty"`
	// Tags contains optional route classification tags.
	Tags []string `json:"tags,omitempty"`
	// Description is the optional long-form route description.
	Description string `json:"description,omitempty"`
	// Deprecated reports whether the route is marked deprecated.
	Deprecated bool `json:"deprecated,omitempty"`
}

// RouteOption configures per-route metadata and behavior.
type RouteOption func(*routeConfig)

type routeConfig struct {
	name        string
	tags        []string
	description string
	deprecated  bool
	maxBodySize int64
	middleware  []Middleware
	summary     string
	operationID string
}

// WithName sets a stable human-readable name for the route.
func WithName(name string) RouteOption {
	return func(rc *routeConfig) { rc.name = name }
}

// WithTags associates one or more tags with the route.
func WithTags(tags ...string) RouteOption {
	return func(rc *routeConfig) { rc.tags = tags }
}

// WithDescription sets the long-form description for the route.
func WithDescription(desc string) RouteOption {
	return func(rc *routeConfig) { rc.description = desc }
}

// WithDeprecated marks the route as deprecated in route metadata.
func WithDeprecated() RouteOption {
	return func(rc *routeConfig) { rc.deprecated = true }
}

// WithRouteMiddleware attaches middleware that runs only for this route.
func WithRouteMiddleware(mw ...Middleware) RouteOption {
	return func(rc *routeConfig) { rc.middleware = mw }
}

// WithRouteMaxBodySize overrides the global body limit for this route.
func WithRouteMaxBodySize(bytes int64) RouteOption {
	return func(rc *routeConfig) { rc.maxBodySize = bytes }
}

// WithOperationID sets a machine-readable identifier for the route.
func WithOperationID(id string) RouteOption {
	return func(rc *routeConfig) { rc.operationID = id }
}

// WithSummary sets a short summary for the route.
func WithSummary(s string) RouteOption {
	return func(rc *routeConfig) { rc.summary = s }
}

// RouteGroup represents a group of routes with a common prefix and middleware.
type RouteGroup struct {
	mux        *http.ServeMux
	prefix     string
	app        *App
	middleware []Middleware
	routes     []RouteInfo
}

func (g *RouteGroup) addRoute(method, pattern string, handler any, opts ...RouteOption) {
	rc := &routeConfig{}
	for _, opt := range opts {
		opt(rc)
	}

	h := adaptHandler(handler)

	// Build the route handler: wrap with route-level middleware
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
		if err := h(ctx); err != nil {
			ctx.app.handleError(ctx, err)
		}
	})

	if len(rc.middleware) > 0 {
		httpHandler = buildChain(httpHandler, rc.middleware)
	}

	muxPattern := method + " " + pattern
	g.mux.Handle(muxPattern, httpHandler)

	fullPattern := g.prefix + pattern
	g.routes = append(g.routes, RouteInfo{
		Method:      method,
		Pattern:     fullPattern,
		Name:        rc.name,
		Tags:        rc.tags,
		Description: rc.description,
		Deprecated:  rc.deprecated,
	})
	g.app.routes = append(g.app.routes, RouteInfo{
		Method:      method,
		Pattern:     fullPattern,
		Name:        rc.name,
		Tags:        rc.tags,
		Description: rc.description,
		Deprecated:  rc.deprecated,
	})
}

// Get registers a GET route within the group.
func (g *RouteGroup) Get(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("GET", pattern, handler, opts...)
	return g
}

// Post registers a POST route within the group.
func (g *RouteGroup) Post(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("POST", pattern, handler, opts...)
	return g
}

// Put registers a PUT route within the group.
func (g *RouteGroup) Put(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("PUT", pattern, handler, opts...)
	return g
}

// Delete registers a DELETE route within the group.
func (g *RouteGroup) Delete(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("DELETE", pattern, handler, opts...)
	return g
}

// Patch registers a PATCH route within the group.
func (g *RouteGroup) Patch(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("PATCH", pattern, handler, opts...)
	return g
}

// Head registers a HEAD route within the group.
func (g *RouteGroup) Head(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("HEAD", pattern, handler, opts...)
	return g
}

// Options registers an OPTIONS route within the group.
func (g *RouteGroup) Options(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("OPTIONS", pattern, handler, opts...)
	return g
}

// Any registers the handler for the common HTTP methods within the group.
func (g *RouteGroup) Any(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		g.addRoute(m, pattern, handler, opts...)
	}
	return g
}

// Use appends middleware scoped to routes registered on this group.
func (g *RouteGroup) Use(middlewares ...Middleware) *RouteGroup {
	g.middleware = append(g.middleware, middlewares...)
	return g
}

// Group creates a nested route group under the current group's prefix.
func (g *RouteGroup) Group(prefix string, fn func(g *RouteGroup)) *RouteGroup {
	sub := &RouteGroup{
		mux:    http.NewServeMux(),
		prefix: g.prefix + prefix,
		app:    g.app,
	}
	fn(sub)

	var handler http.Handler = sub.mux
	if len(sub.middleware) > 0 {
		handler = buildChain(handler, sub.middleware)
	}

	// Wrap to inject context
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	})

	p := prefix
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	g.mux.Handle(p, stripPrefixPreserveContext(prefix, wrapped))
	return g
}

// ctxKey is the context key for storing the aarv Context in request context.
type ctxKey struct{}

var requestContextRegistry = struct {
	mu sync.RWMutex
	m  map[*http.Request]*Context
}{
	m: make(map[*http.Request]*Context),
}

func storeRequestContext(r *http.Request, c *Context) {
	if r != nil && c != nil {
		requestContextRegistry.mu.Lock()
		requestContextRegistry.m[r] = c
		requestContextRegistry.mu.Unlock()
	}
}

func deleteRequestContext(r *http.Request) {
	if r != nil {
		requestContextRegistry.mu.Lock()
		delete(requestContextRegistry.m, r)
		requestContextRegistry.mu.Unlock()
	}
}

func contextFromRequest(r *http.Request) (*Context, bool) {
	if r == nil {
		return nil, false
	}
	requestContextRegistry.mu.RLock()
	c, ok := requestContextRegistry.m[r]
	requestContextRegistry.mu.RUnlock()
	if ok {
		return c, true
	}
	if c, ok := r.Context().Value(ctxKey{}).(*Context); ok {
		return c, true
	}
	return nil, false
}

func withFrameworkContext(r *http.Request, c *Context) *http.Request {
	if r == nil || c == nil {
		return r
	}
	if existing, ok := r.Context().Value(ctxKey{}).(*Context); ok && existing == c {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, c))
}

func stripPrefixPreserveContext(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !strings.HasPrefix(path, prefix) {
			http.NotFound(w, r)
			return
		}

		trimmedPath := strings.TrimPrefix(path, prefix)
		if trimmedPath == "" || trimmedPath[0] != '/' {
			trimmedPath = "/" + trimmedPath
		}

		req := new(http.Request)
		*req = *r
		urlCopy := new(url.URL)
		*urlCopy = *r.URL
		urlCopy.Path = trimmedPath
		req.URL = urlCopy
		req.RequestURI = trimmedPath
		if c, ok := contextFromRequest(r); ok {
			storeRequestContext(req, c)
			defer deleteRequestContext(req)
		}
		h.ServeHTTP(w, req)
	})
}

// FromRequest extracts the aarv Context from an http.Request.
// This is exported so sub-packages (plugins) can access the aarv Context.
func FromRequest(r *http.Request) (*Context, bool) {
	return contextFromRequest(r)
}
