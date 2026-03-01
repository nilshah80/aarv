<p align="center">
  <h1 align="center">Aarv</h1>
  <p align="center"><em>The peaceful sound of minimal Go.</em></p>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/nilshah80/aarv"><img src="https://pkg.go.dev/badge/github.com/nilshah80/aarv.svg" alt="Go Reference"></a>
  <a href="https://github.com/nilshah80/aarv/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
  <a href="https://goreportcard.com/report/github.com/nilshah80/aarv"><img src="https://goreportcard.com/badge/github.com/nilshah80/aarv" alt="Go Report Card"></a>
</p>

---

**Aarv** is a lightweight, zero-dependency Go web framework built on top of `net/http`.

Inspired by **.NET Minimal API** (type-safe binding, fluent registration), **Fastify** (plugins, lifecycle hooks, encapsulation), and **Mach** (minimalism, stdlib-first).

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
