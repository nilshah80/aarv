package aarv

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
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
	hooks            *hookRegistry
	hasOnRequest     bool // fast check to skip empty hook iteration
	hasPreRouting    bool // fast check for PreRouting hooks
	hasPreParsing    bool // fast check for PreParsing hooks
	hasPreValidation bool // fast check for PreValidation hooks
	hasPreHandler    bool // fast check for PreHandler hooks
	hasOnError       bool // fast check for OnError hooks
	hasOnResponse    bool // fast check for OnResponse hooks
	hasOnSend        bool // fast check for OnSend hooks

	// Plugins
	plugins    []pluginEntry
	decorators map[string]any

	// Routes (for introspection)
	routes                  []RouteInfo
	routesByKey             map[string]struct{}                       // "METHOD /path" for 405 detection
	routeMethodsExact       map[string]map[string]struct{}            // [path][method] exact paths for 405 detection
	routeMethodsDynamic     []dynamicRouteMethods                     // dynamic patterns for 405 detection
	routeHandlerFast        map[string]map[string]routeRuntimeHandler // [method][path] two-level lookup (zero-alloc)
	routeChainFast          map[string]map[string]http.Handler        // [method][path] prebuilt exact-route middleware chain
	routeChainFastNative    map[string]map[string]routeRuntimeHandler // [method][path] prebuilt exact-route native middleware chain
	groupRouteHandlers      map[string]map[string]http.Handler        // [method][path] grouped exact routes with group middleware applied
	groupRouteNative        map[string]map[string]routeRuntimeHandler // [method][path] grouped exact routes with group native middleware applied
	groupRouteChainFast     map[string]map[string]http.Handler        // [method][path] grouped exact routes with global middleware applied
	groupRouteChainNative   map[string]map[string]routeRuntimeHandler // [method][path] grouped exact routes with global native middleware applied
	groupDynamicHandlers    map[string][]directDynamicHTTPRoute       // [method] grouped dynamic routes with group middleware applied
	groupDynamicNative      map[string][]directDynamicRoute           // [method] grouped dynamic routes with group native middleware applied
	groupDynamicChainFast   map[string][]directDynamicHTTPRoute       // [method] grouped dynamic routes with global middleware applied
	groupDynamicChainNative map[string][]directDynamicRoute           // [method] grouped dynamic routes with global native middleware applied
	directDynamicRoutes     map[string][]directDynamicRoute
	redirectSlashExact      map[string]struct{}
	redirectSlashDynamic    []directPattern

	// routeIdempotencyTTL maps [method][pattern]→TTL for routes
	// registered with WithRouteIdempotencyTTL. Lookup is keyed on
	// the method and pattern Context.RoutePattern() exposes, so
	// every dispatch path that sets c.routePattern (fast, mux,
	// dynamic, group) is automatically supported with no per-path
	// plumbing. Read-only after the App has started serving;
	// concurrent reads are safe.
	routeIdempotencyTTL map[string]map[string]time.Duration
	trustedProxyCIDRs   []*net.IPNet
	trustedProxyIPs     map[string]struct{}

	// Shutdown
	shutdownHooks []ShutdownHook
}

// New creates a new App with the given options.
func New(opts ...Option) *App {
	defaultCodec := NewStdJSONCodec()
	a := &App{
		mux:                     http.NewServeMux(),
		config:                  defaultConfig(),
		logger:                  slog.New(slog.NewTextHandler(os.Stdout, nil)),
		hooks:                   newHookRegistry(),
		decorators:              make(map[string]any),
		routesByKey:             make(map[string]struct{}),
		routeMethodsExact:       make(map[string]map[string]struct{}),
		routeHandlerFast:        make(map[string]map[string]routeRuntimeHandler),
		routeChainFast:          make(map[string]map[string]http.Handler),
		routeChainFastNative:    make(map[string]map[string]routeRuntimeHandler),
		groupRouteHandlers:      make(map[string]map[string]http.Handler),
		groupRouteNative:        make(map[string]map[string]routeRuntimeHandler),
		groupRouteChainFast:     make(map[string]map[string]http.Handler),
		groupRouteChainNative:   make(map[string]map[string]routeRuntimeHandler),
		groupDynamicHandlers:    make(map[string][]directDynamicHTTPRoute),
		groupDynamicNative:      make(map[string][]directDynamicRoute),
		groupDynamicChainFast:   make(map[string][]directDynamicHTTPRoute),
		groupDynamicChainNative: make(map[string][]directDynamicRoute),
		directDynamicRoutes:     make(map[string][]directDynamicRoute),
		redirectSlashExact:      make(map[string]struct{}),
		trustedProxyIPs:         make(map[string]struct{}),
		routeIdempotencyTTL:     make(map[string]map[string]time.Duration),
	}
	a.setCodec(defaultCodec)

	a.ctxPool = sync.Pool{
		New: func() any {
			return &Context{app: a}
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

	a.rebuildTrustedProxies()

	return a
}

func (a *App) rebuildTrustedProxies() {
	a.trustedProxyCIDRs = a.trustedProxyCIDRs[:0]
	clear(a.trustedProxyIPs)
	for _, entry := range a.config.TrustedProxies {
		if _, network, err := net.ParseCIDR(entry); err == nil {
			a.trustedProxyCIDRs = append(a.trustedProxyCIDRs, network)
			continue
		}
		a.trustedProxyIPs[entry] = struct{}{}
	}
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
	return c
}

// ReleaseContext returns a Context to the pool.
func (a *App) ReleaseContext(c *Context) {
	if c == nil {
		return
	}
	a.ctxPool.Put(c)
}

// CodecContentType returns the content type advertised by the active codec
// (set via WithCodec; defaults to "application/json"). Used by the OpenAPI
// plugin so generated request bodies declare the correct media type when
// the App swaps in a non-JSON codec.
func (a *App) CodecContentType() string {
	return a.codecContentType
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

	// WithDisableHTTP2 disables "h2" specifically. Forcing the slice to exactly
	// ["http/1.1"] (rather than filtering "h2" out of NextProtos) is required:
	// a nil or empty NextProtos lets the stdlib auto-configure HTTP/2.
	// Plugins that need to negotiate other ALPN tokens (e.g. autocert appending
	// "acme-tls/1" for ACME TLS-ALPN-01) may extend this slice without
	// re-enabling "h2".
	if a.config.DisableHTTP2 {
		tlsCfg.NextProtos = []string{"http/1.1"}
	}

	return tlsCfg
}

// TLSConfig returns a hardened *tls.Config suitable for server-auth HTTPS
// listeners. It is a clone of the configured TLS settings with:
//   - MinVersion floored at TLS 1.2.
//   - When WithDisableHTTP2 is set, NextProtos forced to exactly
//     []string{"http/1.1"}. Filtering "h2" alone is insufficient because nil
//     or empty NextProtos allows the stdlib to negotiate HTTP/2 implicitly.
//
// Plugin packages that build their own *http.Server (e.g. autocert) should
// start from TLSConfig (or MutualTLSConfig) so framework-wide TLS hardening
// applies. The returned value is a tls.Config.Clone(): top-level fields are
// independent, but pointer fields (ClientCAs, Certificates slice elements)
// follow stdlib clone semantics — treat nested objects as shared.
func (a *App) TLSConfig() *tls.Config {
	return a.effectiveTLSConfig(false)
}

// MutualTLSConfig returns a *tls.Config like TLSConfig but additionally sets
// ClientAuth = tls.RequireAndVerifyClientCert for mutual TLS authentication.
// The caller is still responsible for populating ClientCAs.
//
// Same clone semantics as TLSConfig.
func (a *App) MutualTLSConfig() *tls.Config {
	return a.effectiveTLSConfig(true)
}
