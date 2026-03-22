package aarv

import (
	"fmt"
	"net/http"
	"strings"
)

// PluginOption configures plugin registration.
type PluginOption func(*string)

// WithPrefix sets the plugin's route prefix.
func WithPrefix(prefix string) PluginOption {
	return func(s *string) { *s = prefix }
}

func (a *App) addRoute(method, pattern string, handler any, opts ...RouteOption) *App {
	rc := &routeConfig{}
	for _, opt := range opts {
		opt(rc)
	}

	adapted := adaptHandler(handler)
	h := adapted.fn

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

		if ctx.app.hasPreHandler && !adapted.preHandled {
			if err := ctx.app.hooks.run(PreHandler, ctx); err != nil {
				ctx.app.handleError(ctx, err)
				return
			}
		}

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

	if len(rc.middleware) > 0 {
		httpHandler = buildChain(httpHandler, rc.middleware)
	}

	muxPattern := method + " " + pattern
	directPattern := directPattern{}
	isDynamic := strings.Contains(pattern, "{")
	if isDynamic {
		directPattern = compileDirectPattern(pattern)
	}
	a.trackRedirectSlashPattern(pattern, isDynamic, directPattern)
	a.mux.Handle(muxPattern, httpHandler)
	if len(rc.middleware) == 0 {
		if isDynamic {
			a.directDynamicRoutes[method] = append(a.directDynamicRoutes[method], directDynamicRoute{
				handler: internalHandler,
				pattern: directPattern,
			})
		} else {
			if a.routeHandlerFast[method] == nil {
				a.routeHandlerFast[method] = make(map[string]routeRuntimeHandler)
			}
			a.routeHandlerFast[method][pattern] = internalHandler
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
	a.trackMethodPattern(method, pattern, isDynamic, directPattern)

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
		prefix: prefix,
		app:    a,
	}

	fn(group)
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

// AddHook registers a lifecycle hook.
func (a *App) AddHook(phase HookPhase, fn HookFunc) *App {
	if fn != nil {
		a.hooks.add(phase, fn)
		a.setHookFlag(phase)
	}
	return a
}

// AddHookWithPriority registers a hook with priority (lower = runs first).
func (a *App) AddHookWithPriority(phase HookPhase, priority int, fn HookFunc) *App {
	if fn != nil {
		a.hooks.addWithPriority(phase, priority, fn)
		a.setHookFlag(phase)
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

func (a *App) setHookFlag(phase HookPhase) {
	switch phase {
	case OnRequest:
		a.hasOnRequest = true
	case PreRouting:
		a.hasPreRouting = true
	case PreParsing:
		a.hasPreParsing = true
	case PreValidation:
		a.hasPreValidation = true
	case PreHandler:
		a.hasPreHandler = true
	case OnError:
		a.hasOnError = true
	case OnResponse:
		a.hasOnResponse = true
	case OnSend:
		a.hasOnSend = true
	}
}

func (a *App) trackRedirectSlashPattern(pattern string, isDynamic bool, compiled directPattern) {
	if isDynamic {
		a.redirectSlashDynamic = append(a.redirectSlashDynamic, compiled)
		return
	}
	a.redirectSlashExact[pattern] = struct{}{}
}

func (a *App) trackMethodPattern(method, pattern string, isDynamic bool, compiled directPattern) {
	if isDynamic {
		for i := range a.routeMethodsDynamic {
			if a.routeMethodsDynamic[i].patternString == pattern {
				a.routeMethodsDynamic[i].methods[method] = struct{}{}
				return
			}
		}
		a.routeMethodsDynamic = append(a.routeMethodsDynamic, dynamicRouteMethods{
			patternString: pattern,
			pattern:       compiled,
			methods:       map[string]struct{}{method: {}},
		})
		return
	}
	methods := a.routeMethodsExact[pattern]
	if methods == nil {
		methods = make(map[string]struct{})
		a.routeMethodsExact[pattern] = methods
	}
	methods[method] = struct{}{}
}
