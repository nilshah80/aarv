<p align="center">
  <h1 align="center">Aarv</h1>
  <p align="center"><em>The peaceful sound of minimal Go.</em></p>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/nilshah80/aarv"><img src="https://pkg.go.dev/badge/github.com/nilshah80/aarv.svg" alt="Go Reference"></a>
  <a href="https://github.com/nilshah80/aarv/actions/workflows/test.yml"><img src="https://github.com/nilshah80/aarv/actions/workflows/test.yml/badge.svg" alt="Tests"></a>
  <a href="https://github.com/nilshah80/aarv/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
  <a href="https://goreportcard.com/report/github.com/nilshah80/aarv"><img src="https://goreportcard.com/badge/github.com/nilshah80/aarv" alt="Go Report Card"></a>
</p>

---

**Aarv** is a lightweight, zero-dependency Go web framework built on top of `net/http`.

Inspired by **.NET Minimal API** (type-safe binding, fluent registration) and **Fastify** (plugins, lifecycle hooks, encapsulation).

## Features

- **Zero external dependencies** in core — pure Go stdlib
- **Go 1.22+ ServeMux** routing with method patterns, path params, and wildcards
- **Type-safe request binding** via Go generics (`Bind[Req, Res]`)
- **Multi-source binding** — path params, query, headers, cookies, body, form — all via struct tags
- **Built-in validation engine** — struct tag rules, pre-computed at registration, zero-alloc hot path
- **Fastify-style lifecycle hooks** — OnRequest, PreHandler, OnResponse, OnSend, OnError, and more
- **Scoped plugin system** — encapsulated plugins with decorators, nested registration
- **Middleware compatible** — standard `func(http.Handler) http.Handler` works out of the box
- **Pooled Context** — `sync.Pool` recycled context for minimal GC pressure
- **Pluggable JSON codec** — swap `encoding/json` for segmentio, sonic, or json/v2
- **Graceful shutdown** with signal handling and drain timeout
- **TLS / HTTP/2 / mTLS** — production-ready with sensible defaults

## Quick Start

```go
package main

import "github.com/nilshah80/aarv"

func main() {
    app := aarv.New()

    app.Get("/hello", func(c *aarv.Context) error {
        return c.JSON(200, map[string]string{"message": "hello, world"})
    })

    app.Listen(":8080")
}
```

## Examples

Concrete examples live under [`examples/`](./examples):

- `examples/hello` — minimal routes, `Bind`, route groups
- `examples/rest-crud` — CRUD-style app structure
- `examples/hooks` — full lifecycle hooks including `PreRouting`, `PreParsing`, `PreValidation`, `PreHandler`, `OnError`
- `examples/route-groups` — nested route groups with scoped middleware
- `examples/binding` — multi-source binding across path, query, header, and JSON body
- `examples/error-handling` — custom error handler plus `OnError`
- `examples/custom-middleware` — stdlib-only, native-only, and dual-registered middleware
- `examples/middleware-bridge` — stdlib middleware using `r.WithContext(...)` with Aarv compatibility
- `examples/json-logger` — structured logging
- `examples/encrypt` — AES-GCM request/response encryption
- `examples/custom-plugin` — decorators, dependencies, plugin-scoped routes, dual middleware registration
- `examples/auth` — JWT, API key, and session auth
- `examples/database` — repository-style app with typed handlers
- `examples/fileserver` and `examples/streaming` — file and stream responses

## Type-Safe Handlers

```go
type CreateUserReq struct {
    Name  string `json:"name"  validate:"required,min=2"`
    Email string `json:"email" validate:"required,email"`
}

type CreateUserRes struct {
    ID    string `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email"`
}

app.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (CreateUserRes, error) {
    // req is already parsed, validated, and typed
    return CreateUserRes{ID: "123", Name: req.Name, Email: req.Email}, nil
}))
```

## Multi-Source Binding

```go
type GetOrderReq struct {
    UserID  string `param:"userId"`
    OrderID int    `param:"orderId"`
    Fields  string `query:"fields"  default:"*"`
    Token   string `header:"X-Api-Key"`
}

app.Get("/users/{userId}/orders/{orderId}", aarv.BindReq(func(c *aarv.Context, req GetOrderReq) error {
    // req.UserID from path, req.Fields from query, req.Token from header
    return c.JSON(200, getOrder(req))
}))
```

## Route Groups & Middleware

```go
app.Use(aarv.Recovery(), aarv.Logger())

app.Group("/api/v1", func(g *aarv.RouteGroup) {
    g.Use(authMiddleware)

    g.Get("/users", listUsers)
    g.Post("/users", aarv.Bind(createUser))

    g.Group("/admin", func(ag *aarv.RouteGroup) {
        ag.Use(adminOnly)
        ag.Delete("/users/{id}", deleteUser)
    })
})
```

## Middleware Modes

Aarv supports two middleware styles:

- stdlib-compatible: `func(http.Handler) http.Handler`
- Aarv-native: `func(next aarv.HandlerFunc) aarv.HandlerFunc`

Stdlib middleware is the compatibility path. It works with ordinary Go middleware and preserves Aarv context across `r.WithContext(...)` clones by default.

Aarv-native middleware is the faster path. Use `aarv.WrapMiddleware(...)` for custom middleware that needs `*aarv.Context` directly:

```go
app.Use(aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
    return func(c *aarv.Context) error {
        c.SetHeader("X-Trace", "on")
        return next(c)
    }
}))
```

For middleware-heavy services that never rely on Aarv context recovery from cloned requests, `WithRequestContextBridge(false)` remains an opt-in fast mode.

## Lifecycle Hooks

```go
app.AddHook(aarv.OnRequest, func(c *aarv.Context) error {
    c.Set("startTime", time.Now())
    return nil
})

app.AddHook(aarv.OnResponse, func(c *aarv.Context) error {
    start, _ := aarv.GetTyped[time.Time](c, "startTime")
    c.Logger().Info("request completed", "latency", time.Since(start))
    return nil
})
```

## Plugins

```go
app.Register(aarv.PluginFunc(func(p *aarv.PluginContext) error {
    p.Decorate("db", connectDB())
    p.Get("/health", healthCheck)
    return nil
}))
```

## Pluggable JSON Codec

```go
import "github.com/nilshah80/aarv/codec/segmentio"

app := aarv.New(aarv.WithCodec(segmentio.New()))
```

## Performance Notes

The core framework stays zero-dependency and production-oriented. The `requestid` plugin is opt-in, but Aarv preserves framework context across raw `r.WithContext(...)` middleware clones by default so standard Go middleware keeps working.

`WithRequestContextBridge(false)` is an opt-in mode for middleware-heavy services that never rely on `aarv.FromRequest(...)` after cloning requests with `r.WithContext(...)`.

Use it when:

- your middleware stack does not need Aarv context recovery from cloned requests
- you want the leanest compatibility tradeoff for fully controlled middleware stacks

Do not use it when:

- standard middleware calls `r.WithContext(...)` and downstream code expects `aarv.FromRequest(...)` to still work on the cloned request
- you need the default compatibility behavior across mixed stdlib middleware

Example:

```go
app := aarv.New(
    aarv.WithBanner(false),
    aarv.WithRequestContextBridge(false),
)
```

## Architecture

```
Request → Global Middleware → ServeMux Route Match → Group Middleware → Hooks → Bind → Validate → Handler → Response
            ↕ sync.Pool Context    ↕ Pluggable Codec    ↕ Buffered Writer    ↕ Error Handler
```

## Philosophy

- **Minimal** — thin layer over `net/http`, not a replacement
- **Zero-dep core** — everything in the core uses only the Go standard library
- **Fast** — pooled contexts, pre-computed binders/validators, pre-built middleware chains
- **Familiar** — standard middleware signature, standard handler patterns, nothing magical
- **Pluggable** — swap JSON codec, validator, error handler, logger — all via interfaces

## License

MIT
