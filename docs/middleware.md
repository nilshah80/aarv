# Middleware guide

Aarv accepts ordinary `net/http` middleware and Aarv-native middleware.
The key production concern is order: middleware runs in registration
order on the way in and unwinds in reverse on the way out.

## Middleware forms

Stdlib middleware works unchanged:

```go
func trace(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Trace", "on")
        next.ServeHTTP(w, r)
    })
}

app.Use(trace)
```

Native middleware receives `*aarv.Context` directly:

```go
app.Use(aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
    return func(c *aarv.Context) error {
        c.SetHeader("X-Trace", "on")
        return next(c)
    }
}))
```

Plugins that can provide both paths should use
`aarv.RegisterNativeMiddleware(stdlib, native)`. Aarv uses the native
fast path when every middleware in the chain supports it.

The native middleware registry is process-wide and keyed by the registered
middleware function pointer. Register the stdlib and native pair once at
middleware construction time and pass the returned middleware value through the
app or route chain. Wrapping, recreating, or copying the stdlib function later
creates a different key and falls back to the compatibility path.

## Recommended global order

Start with this order for APIs:

1. `aarv.Recovery()` or `plugins/recover`
2. `plugins/requestid`
3. access logging / tracing (`logger`, `verboselog`, `otel`, `prometheus`)
4. security headers (`secure`)
5. CORS (`cors`)
6. body size limits (`bodylimit`)
7. request timeout (`timeout`)
8. request normalization (`sanitize`, decrypt, body decompression if any)
9. authentication (`jwt`, `bearer`, `apikey`, `basicauth`, `hmacauth`)
10. session loading (`session`) when handlers need cookie-tracked state
11. authorization (`rbac`)
12. idempotency (`idempotency`)
13. response modifiers (`etag`, `compress`)
14. static files / health endpoints where intentionally global

That order is a starting point, not a law. Put route-specific middleware
closer to the route when only a subset needs it.

## Security-sensitive ordering

### Auth before RBAC

RBAC is authorization only. It never authenticates and never emits a
`WWW-Authenticate` challenge. Put an auth plugin before RBAC so the role
extractor has a trusted identity to read.

```go
app.Use(jwt.New(jwtCfg))
app.Use(authz.RequireRoles("admin"))
```

### HMAC before idempotency

When signed requests and idempotency are both enabled, authenticate the
request before it can occupy an idempotency lock or cache slot.

```go
app.Use(
    requestid.New(),
    aarv.Recovery(),
    hmacauth.New(hmacCfg),
    idempotency.New(idempotencyCfg),
)
```

### Body limit before body readers

Put `bodylimit` before middleware that reads the request body, such as
binding-heavy custom middleware, encryption/decryption, HMAC signing, or
sanitization. That keeps memory and CPU cost bounded for invalid bodies.

### Compression late

Compression should run after handlers and after response-shaping
middleware has produced the body. Avoid compression on streaming routes
unless the route has been tested with the exact client behavior.

## Route and group middleware

Use global middleware for cross-cutting behavior:

```go
app.Use(requestid.New(), aarv.Recovery())
```

Use groups for API areas:

```go
app.Group("/admin", func(g *aarv.RouteGroup) {
    g.Use(authz.RequireRoles("admin"))
    g.Get("/users", listUsers)
})
```

Use route middleware for one-off policies:

```go
app.Post("/exports", exportData,
    aarv.WithRouteMiddleware(bodylimit.New(10<<20)),
)
```

## Context bridge

By default, Aarv preserves `*aarv.Context` recovery across stdlib
middleware that clones requests with `r.WithContext(...)`. This keeps
mixed middleware stacks predictable.

Use `aarv.WithRequestContextBridge(false)` only for controlled stacks
where no downstream middleware calls `aarv.FromRequest(r)` after a
stdlib middleware clones the request.

```go
app := aarv.New(
    aarv.WithRequestContextBridge(false),
)
```

Plugins that receive an upstream-mutated `*http.Request` and then need
`*aarv.Context` should call `c.BindRequest(r)` before reading from `c`.
The `bearer`, `rbac`, and `session` plugins use this pattern.

## Wrapping a middleware to add observability

A common task is to layer a tracing span (or metrics, or audit log) over
an existing middleware that doesn't emit one — for example, putting a
`auth.verify` span around an authentication middleware so traces show
where time was spent.

The right shape depends on what the inner middleware and downstream
handlers read from the request context. Three options, in increasing
order of correctness for the general case:

### Option 1: stdlib-only wrapper

```go
func traceAuth(inner http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx, span := tracer.Start(r.Context(), "auth.verify")
        defer span.End()
        inner.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

This works **only when neither the inner middleware nor any downstream
handler reads from `*aarv.Context.Context()`**. Aarv keeps
`c.Context()` distinct from `r.Context()`; an upstream `r.WithContext`
is invisible to handlers that read `c.Context()`. In aarv-native apps
this is rare — most plugins and handler conventions go through
`c.Context()`, so the wrapped span will not be the parent of any child
spans those handlers create.

### Option 2: native-only wrapper

```go
func traceAuthNative(next aarv.HandlerFunc) aarv.HandlerFunc {
    return func(c *aarv.Context) error {
        ctx, span := tracer.Start(c.Context(), "auth.verify")
        defer span.End()
        c.SetContext(ctx)
        return next(c)
    }
}

app.Use(aarv.WrapMiddleware(traceAuthNative))
```

`c.SetContext(ctx)` updates the framework context so downstream code
that reads `c.Context()` sees the new parent span. Works correctly on
the native fast path. **Requires every middleware in the chain to be
registered as native** — if any middleware lacks a native pair, aarv
downgrades to the stdlib chain and the wrapper above does nothing on
the stdlib path.

### Option 3: register both forms (the safe default)

```go
stdlib := func(inner http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx, span := tracer.Start(r.Context(), "auth.verify")
        defer span.End()
        // Bridge the new context onto the framework context so
        // downstream c.Context() reads see the new parent span.
        if c, ok := aarv.FromRequest(r); ok {
            c.SetContext(ctx)
        }
        inner.ServeHTTP(w, r.WithContext(ctx))
    })
}

native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
    return func(c *aarv.Context) error {
        ctx, span := tracer.Start(c.Context(), "auth.verify")
        defer span.End()
        c.SetContext(ctx)
        return next(c)
    }
})

app.Use(aarv.RegisterNativeMiddleware(stdlib, native))
```

This is correct regardless of whether the chain runs on the stdlib path
(missing or non-native sibling middleware) or the native fast path. Use
it for any wrapper that mutates the request context and is consumed by
code you don't control.

### Skipping the wrap on observability paths

When the wrapper is broadly applied, exclude the observability endpoints
themselves so probe traffic does not generate spans / metrics:

```go
app.Use(aarv.SkipPaths(
    []string{"/health", "/ready", "/live", "/metrics"},
    aarv.RegisterNativeMiddleware(stdlib, native),
))
```

`aarv.SkipPaths` preserves the native fast path when the wrapped
middleware has a native variant — distinct `SkipPaths` instances each
carry their own native fn, so the chain builder runs the right inner
on the right routes without falling back to stdlib. For plugins that
ship a built-in `SkipPaths` config field (`prometheus.Config.SkipPaths`,
`compress.Config.SkipPaths`, `etag.Config.SkipPaths`,
`logger.Config.SkipPaths`, `verboselog.Config.SkipPaths`), the
per-plugin field skips inside the plugin's own dispatch and is
slightly more efficient than wrapping with `aarv.SkipPaths` — prefer
it when available.

### Adding observability to plugins that already provide a hook

Some plugins expose a `Config.Observer` (or similar callback). When
present, prefer the hook over wrapping — it runs inside the plugin's
own decision logic with strictly typed event data and avoids the
context-bridging dance entirely. For example, `plugins/hmacauth` sets
an `Observer(c, Event)` callback after every verification attempt with
the canonical `Outcome` enum, the client ID, the response status, and
the wall-clock duration; an OpenTelemetry adapter can convert each
event into a span without touching the request pipeline.

## Buffering and streaming

Middleware can change memory behavior:

- Stream-safe: routing, `requestid`, `recover`, `secure`, `cors`,
  `bodylimit`
- Bounded-buffer: `compress`, `verboselog` with body capture
- Full-buffer: `etag`, `encrypt`

Keep full-buffer middleware away from large downloads, uploads, SSE, and
long-lived streams unless payload size is intentionally bounded.

## Health endpoints

Health, readiness, and liveness endpoints usually skip auth, heavy
logging, tracing exporters, response compression, and request body
middleware. Either mount the health plugin early with skip paths on
other middleware, or configure every heavy middleware to skip those
paths.
