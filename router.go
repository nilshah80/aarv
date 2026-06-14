package aarv

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// RouteInfo describes a registered route for introspection. It is the
// authoritative metadata surface that introspection consumers (e.g. the
// OpenAPI plugin) read; App.Routes() returns a deep copy so callers can
// freely mutate slices and maps without corrupting framework state.
type RouteInfo struct {
	// Method is the HTTP method registered for the route.
	Method string `json:"method"`
	// Pattern is the path pattern registered for the route.
	Pattern string `json:"pattern"`
	// Name is the optional application-defined route name.
	Name string `json:"name,omitempty"`
	// Tags contains optional route classification tags.
	Tags []string `json:"tags,omitempty"`
	// Summary is the optional short summary for the route.
	Summary string `json:"summary,omitempty"`
	// Description is the optional long-form route description.
	Description string `json:"description,omitempty"`
	// OperationID is the optional machine-readable identifier for the route.
	OperationID string `json:"operationId,omitempty"`
	// Deprecated reports whether the route is marked deprecated.
	Deprecated bool `json:"deprecated,omitempty"`
	// RequestType, when non-nil, is the value type of the request body.
	// Set via WithSchema, WithSchemaTypes, or implicitly by BindRoute /
	// BindGroupRoute. Always represents the value type (T), not *T.
	RequestType reflect.Type `json:"-"`
	// ResponseType, when non-nil, is the value type of the response body.
	// Same conventions as RequestType.
	ResponseType reflect.Type `json:"-"`
	// Responses maps HTTP status code → human description for documented
	// responses set via WithResponse. nil when no WithResponse was used.
	Responses map[int]string `json:"responses,omitempty"`
	// RequestContentType is the request body content type (default
	// "application/json", or as overridden by WithRequestContentType).
	// Empty when no body schema is declared.
	RequestContentType string `json:"requestContentType,omitempty"`

	// IdempotencyTTL is the TTL the idempotency middleware should
	// apply to cached responses for this route. nil means "use the
	// middleware's global TTL"; a non-nil value overrides it. Set
	// via WithRouteIdempotencyTTL. Distinct from a zero-valued
	// time.Duration (which the middleware would treat as "use
	// default" anyway) because nil-vs-set must be distinguishable
	// from set-to-zero so callers can audit configured routes
	// without false negatives.
	IdempotencyTTL *time.Duration `json:"idempotencyTTL,omitempty"`

	// Middleware lists the route-level and group middleware applied to this
	// route, in execution order (outermost first: group middleware precede
	// route-level middleware). It EXCLUDES app-global middleware registered
	// with App.Use — query those via App.GlobalMiddleware. Explicitly named
	// middleware (NamedMiddleware or NativeMiddleware.Name) appear by that
	// name; unnamed middleware fall back to a best-effort reflect-derived
	// label that is not a stable contract.
	Middleware []string `json:"middleware,omitempty"`
}

// RouteOption configures per-route metadata and behavior.
type RouteOption func(*routeConfig)

type routeConfig struct {
	name               string
	tags               []string
	description        string
	deprecated         bool
	maxBodySize        int64
	middleware         []middlewareSlot
	summary            string
	operationID        string
	schemaReq          reflect.Type
	schemaRes          reflect.Type
	responses          map[int]string
	requestContentType string
	idempotencyTTL     *time.Duration
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
// Accepts Middleware, NativeMiddleware, or func(http.Handler) http.Handler
// values; mixed types in one call are fine. Panics on nil arguments or
// unsupported types via coerceSlot.
//
// The caller's variadic slice is copied so subsequent mutations on the
// caller side do not leak into the route's middleware chain.
func WithRouteMiddleware(mw ...any) RouteOption {
	slots := coerceSlots(mw, "WithRouteMiddleware")
	// Defensive clone — coerceSlots already returns a fresh slice, but
	// re-assert ownership to make the contract explicit.
	owned := append([]middlewareSlot(nil), slots...)
	return func(rc *routeConfig) { rc.middleware = owned }
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

// WithSchema attaches request and response value types to the route's
// metadata, so introspection consumers (e.g. the OpenAPI plugin) can
// generate body and response schemas without re-deriving them from the
// handler closure.
//
// Pointer arguments are unwrapped: WithSchema(&Foo{}) and WithSchema(Foo{})
// both store reflect.TypeOf(Foo{}). Schemas always represent the value type
// T, never *T. A nil argument records "no schema this side"; passing nil
// for both panics at construction time because that is meaningless (omit
// the option instead).
//
// Use WithSchemaTypes when you already have reflect.Type values in hand
// (typed-nil pointer ergonomics around reflect.TypeOf are subtle; the
// generic BindRoute / BindGroupRoute helpers bypass this entirely).
func WithSchema(req, res any) RouteOption {
	if req == nil && res == nil {
		panic("aarv: WithSchema(nil, nil) is meaningless; omit the option")
	}
	return WithSchemaTypes(schemaTypeOf(req), schemaTypeOf(res))
}

// WithSchemaTypes is the precise form of WithSchema that takes pre-built
// reflect.Type values. nil on either side means "no schema this side".
// Pointer types are unwrapped to their element type.
func WithSchemaTypes(req, res reflect.Type) RouteOption {
	req = unwrapPtr(req)
	res = unwrapPtr(res)
	return func(rc *routeConfig) {
		rc.schemaReq = req
		rc.schemaRes = res
	}
}

// WithResponse documents an additional response status with a description.
// Multiple WithResponse calls accumulate; calling WithResponse(200, "...")
// overrides any prior 200 description. Status codes outside 100..599 are
// rejected at construction time.
func WithResponse(status int, description string) RouteOption {
	if status < 100 || status > 599 {
		panic("aarv: WithResponse status must be in [100, 599]")
	}
	return func(rc *routeConfig) {
		if rc.responses == nil {
			rc.responses = make(map[int]string)
		}
		rc.responses[status] = description
	}
}

// WithRequestContentType overrides the request body content type for this
// route (default is "application/json" once a request schema is set).
// Useful when a route accepts a non-JSON body (form, multipart, octet
// stream).
func WithRequestContentType(ct string) RouteOption {
	return func(rc *routeConfig) { rc.requestContentType = ct }
}

// WithRouteIdempotencyTTL overrides the global TTL the idempotency
// plugin uses when caching responses for this route. The value is
// stored on the route's metadata and exposed at request time via
// (*Context).RouteIdempotencyTTL — the idempotency middleware reads
// it and supplies it to Store.Save in place of its own configured
// TTL.
//
// Negative durations panic at construction (a negative TTL would
// cause stores to refuse to persist or to expire instantly, depending
// on backend, which is never what the caller intended). A zero
// duration is permitted and means "do not cache this route's
// responses" — the idempotency middleware treats it as a per-route
// disable.
func WithRouteIdempotencyTTL(d time.Duration) RouteOption {
	if d < 0 {
		panic("aarv: WithRouteIdempotencyTTL must not be negative")
	}
	return func(rc *routeConfig) {
		dd := d
		rc.idempotencyTTL = &dd
	}
}

func schemaTypeOf(v any) reflect.Type {
	if v == nil {
		return nil
	}
	return unwrapPtr(reflect.TypeOf(v))
}

func unwrapPtr(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

// routeInfoFromConfig converts an in-progress routeConfig into the RouteInfo
// snapshot stored on App.routes. The default RequestContentType, when a
// request schema is declared but the user did not call
// WithRequestContentType, is the App's active codec content type — so an
// App configured with WithCodec(NonJSONCodec{}) generates a spec that
// declares the right media type without per-route overrides.
//
// codecContentType is always populated by setCodec, which runs as part of
// App.New, so we can read it unconditionally here.
func (a *App) routeInfoFromConfig(method, pattern string, rc *routeConfig, mwSlots []middlewareSlot) RouteInfo {
	requestCT := rc.requestContentType
	if requestCT == "" && rc.schemaReq != nil {
		requestCT = a.codecContentType
	}
	return RouteInfo{
		Method:             method,
		Pattern:            pattern,
		Name:               rc.name,
		Tags:               rc.tags,
		Summary:            rc.summary,
		Description:        rc.description,
		OperationID:        rc.operationID,
		Deprecated:         rc.deprecated,
		RequestType:        rc.schemaReq,
		ResponseType:       rc.schemaRes,
		Responses:          rc.responses,
		RequestContentType: requestCT,
		IdempotencyTTL:     rc.idempotencyTTL,
		Middleware:         middlewareNames(mwSlots),
	}
}

// RouteGroup represents a group of routes with a common prefix and middleware.
type RouteGroup struct {
	prefix     string
	app        *App
	middleware []middlewareSlot
}

func (g *RouteGroup) addRoute(method, pattern string, handler any, opts ...RouteOption) {
	rc := &routeConfig{}
	for _, opt := range opts {
		opt(rc)
	}

	adapted := adaptHandler(handler)
	h := adapted.fn
	fullPattern := g.prefix + pattern
	routeBodyLimit := rc.maxBodySize
	if routeBodyLimit == 0 {
		routeBodyLimit = g.app.config.MaxBodySize
	}

	combinedMiddleware := make([]middlewareSlot, 0, len(g.middleware)+len(rc.middleware))
	if len(g.middleware) > 0 {
		combinedMiddleware = append(combinedMiddleware, g.middleware...)
	}
	if len(rc.middleware) > 0 {
		combinedMiddleware = append(combinedMiddleware, rc.middleware...)
	}
	baseNative := func(ctx *Context) error {
		if ctx.app.hasPreHandler && !adapted.preHandled {
			if err := ctx.app.hooks.run(PreHandler, ctx); err != nil {
				return err
			}
		}
		if routeBodyLimit > 0 && ctx.req.Body != nil && (ctx.req.ContentLength < 0 || ctx.req.ContentLength > routeBodyLimit) {
			ctx.req.Body = http.MaxBytesReader(ctx.res, ctx.req.Body, routeBodyLimit)
		}
		return h(ctx)
	}

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
		if ctx.app.hasPreHandler && !adapted.preHandled {
			if err := ctx.app.hooks.run(PreHandler, ctx); err != nil {
				ctx.app.handleError(ctx, err)
				return
			}
		}
		if routeBodyLimit > 0 && r.Body != nil && (r.ContentLength < 0 || r.ContentLength > routeBodyLimit) {
			r.Body = http.MaxBytesReader(w, r.Body, routeBodyLimit)
		}
		if err := h(ctx); err != nil {
			ctx.app.handleError(ctx, err)
		}
	})
	var nativeHandler routeRuntimeHandler
	if len(combinedMiddleware) == 0 {
		nativeHandler = func(ctx *Context, w http.ResponseWriter, r *http.Request) {
			if err := baseNative(ctx); err != nil {
				ctx.app.handleError(ctx, err)
			}
		}
	} else if nativeChain, ok := buildNativeChain(baseNative, combinedMiddleware); ok {
		nativeHandler = func(ctx *Context, w http.ResponseWriter, r *http.Request) {
			if err := nativeChain(ctx); err != nil {
				ctx.app.handleError(ctx, err)
			}
		}
	}

	if len(combinedMiddleware) > 0 {
		httpHandler = buildChain(httpHandler, combinedMiddleware)
	}

	muxPattern := method + " " + fullPattern
	directPattern := directPattern{}
	isDynamic := strings.Contains(fullPattern, "{")
	if isDynamic {
		directPattern = compileDirectPattern(fullPattern)
	}
	g.app.trackRedirectSlashPattern(fullPattern, isDynamic, directPattern)
	g.app.mux.Handle(muxPattern, httpHandler)
	g.app.routesByKey[muxPattern] = struct{}{}
	g.app.routes = append(g.app.routes, g.app.routeInfoFromConfig(method, fullPattern, rc, combinedMiddleware))
	g.app.trackMethodPattern(method, fullPattern, isDynamic, directPattern)
	g.app.recordRouteIdempotencyTTL(method, fullPattern, rc)

	if !isDynamic {
		if g.app.groupRouteHandlers[method] == nil {
			g.app.groupRouteHandlers[method] = make(map[string]http.Handler)
		}
		g.app.groupRouteHandlers[method][fullPattern] = httpHandler
		if nativeHandler != nil {
			if g.app.groupRouteNative[method] == nil {
				g.app.groupRouteNative[method] = make(map[string]routeRuntimeHandler)
			}
			g.app.groupRouteNative[method][fullPattern] = nativeHandler
		}
	} else {
		g.app.groupDynamicHandlers[method] = append(g.app.groupDynamicHandlers[method], directDynamicHTTPRoute{
			handler:    httpHandler,
			pattern:    directPattern,
			patternStr: fullPattern,
		})
		if nativeHandler != nil {
			g.app.groupDynamicNative[method] = append(g.app.groupDynamicNative[method], directDynamicRoute{
				handler:    nativeHandler,
				pattern:    directPattern,
				patternStr: fullPattern,
			})
		}
	}
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
// Accepts Middleware, NativeMiddleware, or func(http.Handler) http.Handler
// values; mixed types in one call are fine. Panics on nil arguments or
// unsupported types via coerceSlot.
func (g *RouteGroup) Use(middlewares ...any) *RouteGroup {
	g.middleware = append(g.middleware, coerceSlots(middlewares, "RouteGroup.Use")...)
	return g
}

// Group creates a nested route group under the current group's prefix.
// The sub-group inherits a snapshot of the parent's middleware at the
// time Group() is called — middleware added to the parent via Use AFTER
// Group() does NOT propagate to the sub-group. This is intentional and
// preserved across the v0.9.0 slot redesign.
func (g *RouteGroup) Group(prefix string, fn func(g *RouteGroup)) *RouteGroup {
	sub := &RouteGroup{
		prefix: g.prefix + prefix,
		app:    g.app,
	}
	if len(g.middleware) > 0 {
		sub.middleware = append([]middlewareSlot(nil), g.middleware...)
	}
	fn(sub)
	return g
}

// ctxKey is the context key for storing the aarv Context in request context.
type ctxKey struct{}

// requestContextRegistry is a sharded map to avoid global mutex contention.
// 64 shards reduces contention by ~64x under high concurrency.
// The padding reduces false sharing across adjacent shards.
const registryShards = 64

type registryShard struct {
	mu sync.RWMutex
	m  map[*http.Request]*Context
	_  [24]byte
}

var requestContextRegistry [registryShards]registryShard

func init() {
	for i := range requestContextRegistry {
		requestContextRegistry[i].m = make(map[*http.Request]*Context)
	}
}

// shardFor returns the shard index for a given request pointer.
// Uses the pointer address shifted right by 4 to discard tag bits.
func shardFor(r *http.Request) *registryShard {
	h := uintptr(unsafe.Pointer(r)) >> 4
	return &requestContextRegistry[h&(registryShards-1)]
}

func storeRequestContext(r *http.Request, c *Context) {
	if r != nil && c != nil {
		s := shardFor(r)
		s.mu.Lock()
		s.m[r] = c
		s.mu.Unlock()
	}
}

func deleteRequestContext(r *http.Request) {
	if r != nil {
		s := shardFor(r)
		s.mu.Lock()
		delete(s.m, r)
		s.mu.Unlock()
	}
}

func contextFromRequest(r *http.Request) (*Context, bool) {
	if r == nil {
		return nil, false
	}
	if c, ok := r.Context().Value(ctxKey{}).(*Context); ok {
		return c, true
	}
	s := shardFor(r)
	s.mu.RLock()
	c, ok := s.m[r]
	s.mu.RUnlock()
	if ok {
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
	return r.WithContext(withAarvContext(r.Context(), c))
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
		if _, ok := req.Context().Value(ctxKey{}).(*Context); !ok {
			if c, ok := contextFromRequest(r); ok {
				storeRequestContext(req, c)
				defer deleteRequestContext(req)
			}
		}
		h.ServeHTTP(w, req)
	})
}

// FromRequest extracts the aarv Context from an http.Request.
// This is exported so sub-packages (plugins) can access the aarv Context.
func FromRequest(r *http.Request) (*Context, bool) {
	if r == nil {
		return nil, false
	}
	return contextFromRequest(r)
}
