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
