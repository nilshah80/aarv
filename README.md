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

The core framework stays zero-dependency and production-oriented. The `requestid` plugin is opt-in, but Aarv does preserve framework context across raw `r.WithContext(...)` middleware clones by default so standard Go middleware keeps working.

### Benchmark Reading Guide

- `vanilla` and `bare-min` benchmarks are baseline framework-overhead comparisons, not full feature-parity comparisons.
- `fair` logger/encrypt benchmarks are the apples-to-apples comparisons: same request ID generation, same context storage/retrieval pattern, and same middleware behavior.
- bind benchmarks are most meaningful when all frameworks validate the request, not just decode JSON.

### Current Benchmark Snapshot

Focused microbenchmarks:

| Scenario | Aarv | Mach | Gin |
|----------|------|------|-----|
| `Bind` | `2125 ns/op`, `7045 B/op`, `30 allocs/op` | `2570 ns/op`, `7898 B/op`, `38 allocs/op` | `2938 ns/op`, `8490 B/op`, `48 allocs/op` |
| `BindLight` | `977.7 ns/op`, `1308 B/op`, `16 allocs/op` | `1421 ns/op`, `2154 B/op`, `24 allocs/op` | `1743 ns/op`, `2749 B/op`, `34 allocs/op` |
| `FairLogger` | `3174 ns/op`, `7713 B/op`, `32 allocs/op` | `3027 ns/op`, `7350 B/op`, `30 allocs/op` | `2981 ns/op`, `7576 B/op`, `28 allocs/op` |
| `FairEncrypt` | `3046 ns/op`, `8585 B/op`, `34 allocs/op` | `2984 ns/op`, `8581 B/op`, `33 allocs/op` | `3034 ns/op`, `8872 B/op`, `33 allocs/op` |

Isolated middleware-only comparisons:

| Scenario | Aarv | Equivalent Baseline |
|----------|------|---------------------|
| `LoggerIsolated` | `948.5 ns/op`, `1042 B/op`, `8 allocs/op` | `1031 ns/op`, `1089 B/op`, `9 allocs/op` |
| `EncryptIsolated` | `564.6 ns/op`, `736 B/op`, `8 allocs/op` | `566.9 ns/op`, `880 B/op`, `10 allocs/op` |

Real TCP load test (`500K` requests / framework, `100` concurrent connections):

| Scenario | Aarv | Mach | Gin |
|----------|------|------|-----|
| `Vanilla` | `158K RPS`, `599.3µs avg`, `1.79ms p99`, `6.0KB/op`, `67 allocs/op`, `79.0% CPU` | `159K RPS`, `599.5µs avg`, `1.77ms p99`, `6.0KB/op`, `67 allocs/op`, `79.2% CPU` | `158K RPS`, `598.4µs avg`, `1.78ms p99`, `6.4KB/op`, `68 allocs/op`, `77.8% CPU` |
| `FairLogger` | `156K RPS`, `603.9µs avg`, `1.97ms p99`, `7.2KB/op`, `82 allocs/op`, `77.5% CPU` | `159K RPS`, `593.4µs avg`, `1.87ms p99`, `6.8KB/op`, `80 allocs/op`, `76.2% CPU` | `157K RPS`, `600.3µs avg`, `1.90ms p99`, `7.0KB/op`, `78 allocs/op`, `78.1% CPU` |
| `FairEncrypt` | `154K RPS`, `606.8µs avg`, `1.99ms p99`, `7.9KB/op`, `84 allocs/op`, `77.1% CPU` | `155K RPS`, `605.0µs avg`, `1.93ms p99`, `7.9KB/op`, `83 allocs/op`, `77.6% CPU` | `154K RPS`, `608.0µs avg`, `1.94ms p99`, `8.2KB/op`, `83 allocs/op`, `77.6% CPU` |
| `BareMinLogger` | `158K RPS`, `601.7µs avg`, `1.87ms p99`, `6.5KB/op`, `73 allocs/op`, `78.1% CPU` | `159K RPS`, `596.5µs avg`, `1.79ms p99`, `6.1KB/op`, `72 allocs/op`, `79.0% CPU` | `157K RPS`, `601.7µs avg`, `1.84ms p99`, `6.4KB/op`, `71 allocs/op`, `78.3% CPU` |
| `BareMinEncrypt` | `154K RPS`, `612.1µs avg`, `1.91ms p99`, `7.7KB/op`, `75 allocs/op`, `77.8% CPU` | `155K RPS`, `610.1µs avg`, `1.91ms p99`, `7.3KB/op`, `74 allocs/op`, `78.0% CPU` | `154K RPS`, `611.6µs avg`, `1.91ms p99`, `7.6KB/op`, `75 allocs/op`, `78.2% CPU` |

Interpretation:

- Aarv's logger and encrypt plugins are already competitive in isolation.
- Fair logger and fair encrypt are near-parity with other stdlib frameworks when the work is actually identical.
- The remaining gap on ultra-minimal middleware paths is mostly framework request-context bridging overhead, not plugin logic.
- On bind, Aarv is ahead once the comparison includes validation work on the other side too.

### Performance Tuning

`WithRequestContextBridge(false)` is an opt-in fast mode for middleware-heavy services that never rely on `aarv.FromRequest(...)` after cloning requests with `r.WithContext(...)`.

Use it when:

- your middleware stack does not need Aarv context recovery from cloned requests
- you want to reduce the residual `bare-min` logger/encrypt overhead

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

This mode is intentionally opt-in because it trades away middleware compatibility for a slightly cheaper hot path.

In the current bare-min benchmarks, disabling the bridge trims roughly:

- logger path: about `90 ns/op`, `371 B/op`, and `2 allocs/op`
- encrypt path: about `59 ns/op`, `369 B/op`, and `2 allocs/op`

Run benchmarks yourself:

```bash
cd tests/benchmark && go test -bench='Benchmark(Fair|BareMin|Bind)' -benchmem
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
