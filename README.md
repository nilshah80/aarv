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
- `examples/custom-middleware` — stdlib-only, native-only, and dual-registered custom middleware
- `examples/middleware-bridge` — stdlib middleware using `r.WithContext(...)` with Aarv compatibility
- `examples/json-logger` — standard logger plus debug, production-safe, and minimal `verboselog` presets
- `examples/encrypt` — AES-GCM request/response encryption
- `examples/performance-profile` — bridge-off plus fast JSON codec for throughput-oriented services
- `examples/custom-plugin` — decorators, dependencies, plugin-scoped routes, dual middleware registration
- `examples/auth` — JWT, API key, and session auth
- `examples/jwt-auth` — JWT protected API using the JWT plugin
- `examples/database` — repository-style app with typed handlers
- `examples/fileserver` and `examples/streaming` — file and stream responses
- `examples/file-upload` — multipart upload binding with `UploadedFile`
- `examples/middleware-chain` — production-shaped middleware ordering
- `examples/plugin-custom` — custom plugin interface and scoped routes
- `examples/tls-http2` — HTTPS and HTTP/2 setup
- `examples/microservice` — health checks, Prometheus, and structured logging
- `examples/sse` — server-sent events
- `examples/openapi` — generated OpenAPI spec and Swagger UI

## Guides

Start with these docs when wiring a real service:

- [`docs/getting-started.md`](docs/getting-started.md) — first service, typed handlers, production-shaped baseline
- [`docs/routing.md`](docs/routing.md) — routes, groups, metadata, OpenAPI-facing route options
- [`docs/binding.md`](docs/binding.md) — path/query/header/cookie/form/file/JSON binding
- [`docs/validation.md`](docs/validation.md) — validation tags, custom rules, struct-level validation
- [`docs/middleware.md`](docs/middleware.md) — middleware forms, ordering, streaming/buffering notes
- [`docs/hooks.md`](docs/hooks.md) — lifecycle phases, priorities, startup/shutdown hooks
- [`docs/auth.md`](docs/auth.md) — JWT, Bearer, API key, Basic auth, HMAC, RBAC, Problem Details
- [`docs/session.md`](docs/session.md) — MemoryStore vs CookieStore, login/logout, flash, CSRF, secure cookies
- [`docs/security.md`](docs/security.md) — security headers, CORS, CSRF, IP filtering, sanitization, encryption
- [`docs/resilience.md`](docs/resilience.md) — body limits, timeout, throttle, rate limiting, idempotency, Redis stores
- [`docs/observability.md`](docs/observability.md) — request IDs, logging, health, Prometheus, OpenTelemetry, pprof
- [`docs/responses.md`](docs/responses.md) — response helpers, files, uploads, streaming, static, compression, ETags
- [`docs/codecs.md`](docs/codecs.md) — stdlib, segmentio, sonic, jsonv2 codec choices
- [`docs/plugins.md`](docs/plugins.md) — using and writing plugins, root vs submodule release model
- [`docs/error-handling.md`](docs/error-handling.md) — AppError, validation failures, Problem Details, recovery
- [`docs/tls-http2.md`](docs/tls-http2.md) — TLS/HTTP2 production entry point
- [`docs/testing.md`](docs/testing.md) — TestClient, httptest, race, coverage, submodule checks
- [`docs/architecture.md`](docs/architecture.md) — request flow, core boundaries, modules
- [`docs/release-policy.md`](docs/release-policy.md) — compatibility, submodule tags, release verification
- [`docs/tls.md`](docs/tls.md) — TLS, mTLS, cert reload, autocert, h2c
- [`docs/openapi.md`](docs/openapi.md) — generated OpenAPI specs and UI viewers

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

### Plugin catalogue

Plugins fall into three groups depending on their dependency footprint:

| Layer | What lives here | Module path |
|---|---|---|
| **Root features** (built into the root module) | Lifecycle (`ListenServer`, `TLSConfig`, `MutualTLSConfig`, `Shutdown`), hooks, codec, route binding (`Bind`, `BindRoute`, `WithSchema`, …), cert hot-reload (`WithCertReload`) | `github.com/nilshah80/aarv` |
| **Root plugins** (stdlib-only, in the root module) | `apikey`, `basicauth`, `bearer`, `bodylimit`, `compress`, `cors`, `csrf`, `encrypt`, `etag`, `health`, `hmacauth`, `idempotency`, `ipfilter`, `jwt`, `logger`, `pprof`, `problem`, `ratelimit`, `rbac`, `recover`, `requestid`, `secure`, `session`, `static`, `throttle`, `timeout`, `verboselog` | `github.com/nilshah80/aarv/plugins/<name>` |
| **Submodule plugins** (separate `go get`, third-party deps) | `prometheus`, `otel`, `autocert`, `h2c`, `openapi`, `openapi-ui`, `sanitize`, `hmacauth-redis`, `idempotency-redis`, `ratelimit-redis` | `github.com/nilshah80/aarv/plugins/<name>` (own `go.mod`) |

Submodule plugins each carry their own `go.mod` so the root module can
remain stdlib-only. Add them à la carte:

```bash
go get github.com/nilshah80/aarv/plugins/autocert@latest
go get github.com/nilshah80/aarv/plugins/openapi@latest
```

See [`docs/tls.md`](docs/tls.md) for the autocert / cert-reload / h2c
operational notes and [`docs/openapi.md`](docs/openapi.md) for the
OpenAPI plugin reference.

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

## Middleware Buffering Notes

Not all middleware have the same memory and streaming behavior. For production workloads, the important split is:

- Stream-safe: plain routing, native middleware, most header-only middleware, `bodylimit`, `recover`, `requestid`, `secure`, `cors`
- Bounded-buffer: `compress` buffers until the compression threshold decision is made; `verboselog` with body logging buffers captured request bytes and response bytes up to `MaxBodySize`
- Full-buffer: `etag` buffers the full response body; `encrypt` fully reads encrypted request bodies and buffers full response bodies for encryption

Use the full-buffer plugins only on routes where payload size is bounded and intentional. For large file, streaming, or long-lived responses, keep those routes on stream-safe middleware only.

## Recommended Performance Profile

For services where raw throughput matters more than maximum stdlib middleware compatibility, the measured fast-path profile is:

```go
import (
    "github.com/nilshah80/aarv"
    "github.com/nilshah80/aarv/codec/segmentio"
)

app := aarv.New(
    aarv.WithBanner(false),
    aarv.WithRequestContextBridge(false),
    aarv.WithCodec(segmentio.New()),
)
```

Why this profile:

- `WithRequestContextBridge(false)` removes the cloned-request compatibility overhead for stdlib middleware stacks that never need `aarv.FromRequest(...)` after `r.WithContext(...)`.
- `segmentio.New()` is currently the best measured JSON codec in the local benchmark harness for both `c.JSON(...)` and bind/decode workloads.

Current local benchmark signal from `tests/benchmark`:

- stdlib middleware chain with bridge on: about `219 ns/op`, `856 B/op`, `5 allocs/op`
- stdlib middleware chain with bridge off: about `177-181 ns/op`, `512 B/op`, `3 allocs/op`
- default stdlib JSON response path: about `399-408 ns/op`, `945 B/op`, `9 allocs/op`
- segmentio JSON response path: about `356-364 ns/op`, `884 B/op`, `7 allocs/op`
- default stdlib bind path: about `1010-1021 ns/op`, `1308 B/op`, `16 allocs/op`
- segmentio bind path: about `642-658 ns/op`, `1085-1087 B/op`, `12 allocs/op`

These numbers are machine- and workload-dependent, but the ordering has been stable in the benchmark harness.

See [`examples/performance-profile`](./examples/performance-profile) for a runnable setup using this profile.

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
