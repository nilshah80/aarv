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

## Performance

Aarv includes built-in request ID generation and context propagation. When comparing frameworks with **identical functionality**, Aarv matches or outperforms alternatives.

### Fair Comparison (500K requests, 100 concurrent connections, real TCP)

All frameworks implementing identical request ID generation + context storage + retrieval:

| Scenario | Aarv | Mach | Gin |
|----------|------|------|-----|
| **Logger Middleware** | | | |
| Throughput | 154K RPS | 154K RPS | 154K RPS |
| P50 Latency | **553µs** | 559µs | 557µs |
| P99 Latency | 1.90ms | 1.88ms | 1.90ms |
| allocs/op | **82** | 88 | 86 |
| **Encryption Middleware** | | | |
| Throughput | 151K RPS | **152K RPS** | 151K RPS |
| P50 Latency | 559µs | **557µs** | 558µs |
| P99 Latency | **1.91ms** | 1.93ms | 1.97ms |
| allocs/op | **78** | 83 | 83 |

### Understanding Vanilla Benchmarks

In "vanilla" benchmarks (no middleware), Aarv shows ~5 extra allocations compared to Mach/Gin. This is because Aarv automatically provides:

- **Request ID generation** (ULID) on every request
- **Context propagation** via `context.WithValue` + `r.WithContext`
- **Request ID retrieval** via `aarv.FromRequest(r).RequestID()`

When other frameworks implement these same features, they incur the same costs — and Aarv often comes out ahead due to its optimized implementation.

**Bottom line**: The "overhead" in vanilla benchmarks is actually useful work. In production scenarios with middleware, Aarv is fastest or tied.

Run benchmarks yourself:
```bash
cd tests/benchmark && go test -bench=BenchmarkFair -benchmem
cd tests/benchmark && go test -v -run TestFairLoadTest -timeout 30m
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
