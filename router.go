package aarv

import (
	"context"
	"net/http"
	"strings"
)

// RouteInfo describes a registered route for introspection.
type RouteInfo struct {
	Method      string   `json:"method"`
	Pattern     string   `json:"pattern"`
	Name        string   `json:"name,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Description string   `json:"description,omitempty"`
	Deprecated  bool     `json:"deprecated,omitempty"`
}

// RouteOption configures per-route metadata.
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

func WithName(name string) RouteOption {
	return func(rc *routeConfig) { rc.name = name }
}

func WithTags(tags ...string) RouteOption {
	return func(rc *routeConfig) { rc.tags = tags }
}

func WithDescription(desc string) RouteOption {
	return func(rc *routeConfig) { rc.description = desc }
}

func WithDeprecated() RouteOption {
	return func(rc *routeConfig) { rc.deprecated = true }
}

func WithRouteMiddleware(mw ...Middleware) RouteOption {
	return func(rc *routeConfig) { rc.middleware = mw }
}

func WithRouteMaxBodySize(bytes int64) RouteOption {
	return func(rc *routeConfig) { rc.maxBodySize = bytes }
}

func WithOperationID(id string) RouteOption {
	return func(rc *routeConfig) { rc.operationID = id }
}

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

func (g *RouteGroup) Get(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("GET", pattern, handler, opts...)
	return g
}

func (g *RouteGroup) Post(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("POST", pattern, handler, opts...)
	return g
}

func (g *RouteGroup) Put(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("PUT", pattern, handler, opts...)
	return g
}

func (g *RouteGroup) Delete(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("DELETE", pattern, handler, opts...)
	return g
}

func (g *RouteGroup) Patch(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("PATCH", pattern, handler, opts...)
	return g
}

func (g *RouteGroup) Head(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("HEAD", pattern, handler, opts...)
	return g
}

func (g *RouteGroup) Options(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	g.addRoute("OPTIONS", pattern, handler, opts...)
	return g
}

func (g *RouteGroup) Any(pattern string, handler any, opts ...RouteOption) *RouteGroup {
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		g.addRoute(m, pattern, handler, opts...)
	}
	return g
}

func (g *RouteGroup) Use(middlewares ...Middleware) *RouteGroup {
	g.middleware = append(g.middleware, middlewares...)
	return g
}

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
	g.mux.Handle(p, http.StripPrefix(prefix, wrapped))
	return g
}

// ctxKey is the context key for storing the aarv Context in request context.
type ctxKey struct{}

// contextWithAarv stores the aarv Context into the request's context.Context.
func contextWithAarv(ctx context.Context, c *Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// FromRequest extracts the aarv Context from an http.Request.
// This is exported so sub-packages (plugins) can access the aarv Context.
func FromRequest(r *http.Request) (*Context, bool) {
	c, ok := r.Context().Value(ctxKey{}).(*Context)
	return c, ok
}
