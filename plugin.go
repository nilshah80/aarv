package aarv

import "log/slog"

// Plugin is the interface for framework plugins.
type Plugin interface {
	Name() string
	Register(app *PluginContext) error
}

// PluginWithVersion is an optional interface for plugins that declare a version.
type PluginWithVersion interface {
	Plugin
	Version() string
}

// PluginWithDeps is an optional interface for plugins that declare dependencies.
type PluginWithDeps interface {
	Plugin
	Dependencies() []string
}

// PluginFunc adapts a function to the Plugin interface.
type PluginFunc func(app *PluginContext) error

func (f PluginFunc) Name() string                       { return "anonymous" }
func (f PluginFunc) Register(app *PluginContext) error { return f(app) }

// PluginContext is a scoped view of the App given to plugins during registration.
type PluginContext struct {
	app        *App
	prefix     string
	group      *RouteGroup
	decorators map[string]any
	logger     *slog.Logger
}

func newPluginContext(app *App, prefix string) *PluginContext {
	// Group prefix is empty — PluginContext methods prepend prefix themselves
	g := &RouteGroup{
		mux:    app.mux,
		prefix: "",
		app:    app,
	}
	return &PluginContext{
		app:        app,
		prefix:     prefix,
		group:      g,
		decorators: app.decorators,
		logger:     app.logger.With("plugin", prefix),
	}
}

// routeOpts merges plugin-level middleware into per-route options.
func (pc *PluginContext) routeOpts(opts []RouteOption) []RouteOption {
	if len(pc.group.middleware) == 0 {
		return opts
	}
	// Prepend plugin middleware so it wraps around any route-level middleware.
	combined := make([]RouteOption, 0, len(opts)+1)
	mw := make([]Middleware, len(pc.group.middleware))
	copy(mw, pc.group.middleware)
	combined = append(combined, WithRouteMiddleware(mw...))
	combined = append(combined, opts...)
	return combined
}

// Get registers a GET route scoped to this plugin.
func (pc *PluginContext) Get(pattern string, handler any, opts ...RouteOption) *PluginContext {
	pc.group.Get(pc.prefix+pattern, handler, pc.routeOpts(opts)...)
	return pc
}

// Post registers a POST route scoped to this plugin.
func (pc *PluginContext) Post(pattern string, handler any, opts ...RouteOption) *PluginContext {
	pc.group.Post(pc.prefix+pattern, handler, pc.routeOpts(opts)...)
	return pc
}

// Put registers a PUT route scoped to this plugin.
func (pc *PluginContext) Put(pattern string, handler any, opts ...RouteOption) *PluginContext {
	pc.group.Put(pc.prefix+pattern, handler, pc.routeOpts(opts)...)
	return pc
}

// Delete registers a DELETE route scoped to this plugin.
func (pc *PluginContext) Delete(pattern string, handler any, opts ...RouteOption) *PluginContext {
	pc.group.Delete(pc.prefix+pattern, handler, pc.routeOpts(opts)...)
	return pc
}

// Use adds middleware scoped to this plugin's routes.
func (pc *PluginContext) Use(middlewares ...Middleware) *PluginContext {
	pc.group.Use(middlewares...)
	return pc
}

// Group creates a nested route group within the plugin.
func (pc *PluginContext) Group(prefix string, fn func(g *RouteGroup)) *PluginContext {
	pc.group.Group(pc.prefix+prefix, fn)
	return pc
}

// AddHook adds a lifecycle hook from this plugin.
func (pc *PluginContext) AddHook(phase HookPhase, fn HookFunc) {
	pc.app.hooks.add(phase, fn)
}

// Decorate registers a shared service by key.
func (pc *PluginContext) Decorate(key string, value any) {
	pc.decorators[key] = value
}

// Resolve retrieves a decorated service by key.
func (pc *PluginContext) Resolve(key string) (any, bool) {
	v, ok := pc.decorators[key]
	return v, ok
}

// Register registers a nested plugin.
func (pc *PluginContext) Register(plugin Plugin) error {
	return plugin.Register(pc)
}

// Logger returns the plugin-scoped logger.
func (pc *PluginContext) Logger() *slog.Logger {
	return pc.logger
}

// App returns the underlying App (for advanced use).
func (pc *PluginContext) App() *App {
	return pc.app
}

type pluginEntry struct {
	name   string
	plugin Plugin
}
