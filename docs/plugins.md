# Plugins guide

Aarv plugins are the extension boundary for reusable middleware, routes,
hooks, and decorated services. Root-module plugins stay stdlib-only;
submodule plugins carry third-party dependencies behind separate
`go.mod` files.

## Plugin layers

| Layer | Examples | Install |
|---|---|---|
| Core/root features | routing, binding, validation, hooks, TLS, codecs | `go get github.com/nilshah80/aarv@v0.9.0` |
| Root plugins | `jwt`, `bearer`, `session`, `problem`, `rbac`, `idempotency`, `requestid`, `recover`, `secure` | included with root module |
| Submodule plugins | `prometheus`, `otel`, `openapi`, `openapi-ui`, `autocert`, `sanitize`, Redis stores | separate `go get` per module |

Submodules keep optional dependencies out of the root module. Add them
only when you use them:

```bash
go get github.com/nilshah80/aarv/plugins/openapi@v0.9.0
go get github.com/nilshah80/aarv/plugins/openapi-ui@v0.9.0
go get github.com/nilshah80/aarv/plugins/prometheus@v0.9.0
```

## Using middleware plugins

Most plugins expose `New(Config)` or `New(...)` and return
`aarv.NativeMiddleware` — a struct bundling the stdlib middleware path
with its aarv-native counterpart. `App.Use(...)` accepts the value
directly. The only stdlib-only outlier in the root tree is
`plugins/timeout.New(d)`, which returns `aarv.Middleware` because its
per-request goroutine implementation has no native-path analog (its
sibling `timeout.Context(d)` returns `aarv.NativeMiddleware`).

```go
app.Use(
    requestid.New(),
    secure.New(),
    bodylimit.New(2<<20),
)
```

Route-specific plugins can be attached with `WithRouteMiddleware`:

```go
app.Post("/admin/reindex", reindex,
    aarv.WithRouteMiddleware(authz.RequireRoles("admin")))
```

## Using app plugins

App plugins implement:

```go
type Plugin interface {
    Name() string
    Version() string
    Register(*aarv.PluginContext) error
}
```

Register with optional scoping:

```go
app.Register(myPlugin, aarv.WithPrefix("/internal"))
```

Inside `Register`, a plugin can add middleware, routes, hooks, and
decorated values:

```go
func (p *AuditPlugin) Register(pc *aarv.PluginContext) error {
    pc.Decorate("clock", time.Now)
    pc.Use(p.middleware())
    pc.Get("/status", p.status)
    return nil
}
```

Use `aarv.PluginFunc` for small one-off plugins:

```go
app.Register(aarv.PluginFunc(func(pc *aarv.PluginContext) error {
    pc.Get("/debug/routes", listRoutes)
    return nil
}))
```

## Dependencies

Plugins can declare dependencies by name:

```go
func (p *MetricsPlugin) Dependencies() []string {
    return []string{"rate-limiter"}
}
```

Use dependencies for plugins that require decorators or routes created
by another plugin. Keep dependency names stable and human-readable.

## Writing middleware plugins

Prefer dual registration when a plugin can implement both stdlib and
native paths:

```go
return aarv.RegisterNativeMiddleware(stdlib, native)
```

Native middleware can use `*aarv.Context` directly and avoids the
stdlib adapter overhead. Stdlib middleware keeps compatibility with the
Go ecosystem and plain `net/http` handlers.

When a stdlib path retrieves `*aarv.Context` from a request that may
have been cloned or rewritten upstream, call `c.BindRequest(r)` before
reading request-derived values from `c`.

## Configuration discipline

For security-sensitive plugins:

- panic on missing required config at construction time
- fail closed at request time
- return stable error bodies that do not leak secrets
- keep context storage keys internal and expose typed `From` helpers
- document native-vs-stdlib response parity if the stdlib path writes
  directly

Existing auth plugins follow this pattern.

## Plugin curation

Prefer a new plugin only when it owns a distinct concern, dependency boundary,
or deployment profile. If a feature overlaps an existing plugin, document the
selection rule in the plugin README instead of relying on names alone. For
example, `logger`, `verboselog`, `prometheus`, and `otel` all touch
observability, but they serve different jobs: request logs, structured verbose
diagnostics, scrape metrics, and traces/metrics through OpenTelemetry.

Keep dependency-heavy integrations as submodules. Keep small security and HTTP
policy middleware in the root module when they only use the standard library.

## Release rule

Root plugins ship under the root tag:

```bash
go get github.com/nilshah80/aarv@v0.9.0
```

Submodule plugins use path-prefixed tags:

```bash
go get github.com/nilshah80/aarv/plugins/openapi@v0.9.0
```

When releasing a new root version, submodule `go.mod` files should be
aligned to the new root tag and verified from outside the repo with
`go list -m`.
