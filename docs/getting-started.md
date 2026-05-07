# Getting started

This guide takes a new Aarv service from an empty module to a small
production-shaped API. It focuses on the default path: stdlib-only root
module, typed handlers, validation, middleware, and clean shutdown.

## Install

```bash
go mod init example.com/users-api
go get github.com/nilshah80/aarv@v0.8.0
```

Submodule plugins such as `prometheus`, `otel`, `openapi`, `openapi-ui`,
`autocert`, `sanitize`, and Redis-backed stores are installed separately
only when you need them.

## Minimal app

```go
package main

import (
    "net/http"

    "github.com/nilshah80/aarv"
)

func main() {
    app := aarv.New()

    app.Get("/health", func(c *aarv.Context) error {
        return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
    })

    app.Listen(":8080")
}
```

Run it:

```bash
go run .
curl http://127.0.0.1:8080/health
```

## Typed handlers

Use `aarv.Bind` when a route accepts a structured request and returns a
structured response. Aarv reads from path params, query strings, headers,
cookies, forms, and JSON bodies based on struct tags.

```go
type CreateUserReq struct {
    Name  string `json:"name" validate:"required,min=2"`
    Email string `json:"email" validate:"required,email"`
}

type UserRes struct {
    ID    string `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email"`
}

app.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (UserRes, error) {
    return UserRes{ID: "u_1", Name: req.Name, Email: req.Email}, nil
}))
```

Validation failures return the framework's validation error response. If
you want RFC 7807 responses, install the Problem Details handler from
[`docs/auth.md`](auth.md) / [`docs/plugins.md`](plugins.md).

## Route groups

Groups share prefixes and middleware. Use them for versioned APIs,
admin surfaces, and plugin-like route bundles.

```go
app.Group("/api/v1", func(g *aarv.RouteGroup) {
    g.Get("/users/{id}", func(c *aarv.Context) error {
        return c.JSON(http.StatusOK, map[string]string{
            "id": c.Param("id"),
        })
    })
})
```

Route-level middleware is available when one endpoint needs extra
protection:

```go
app.Post("/admin/reindex", reindex, aarv.WithRouteMiddleware(adminOnly))
```

## Production-shaped baseline

For most JSON APIs, start with this shape and adjust from there:

```go
import (
    "time"

    "github.com/nilshah80/aarv"
    "github.com/nilshah80/aarv/plugins/bodylimit"
    "github.com/nilshah80/aarv/plugins/logger"
    "github.com/nilshah80/aarv/plugins/problem"
    "github.com/nilshah80/aarv/plugins/requestid"
    "github.com/nilshah80/aarv/plugins/secure"
)

app := aarv.New(
    aarv.WithBanner(false),
    aarv.WithReadTimeout(10*time.Second),
    aarv.WithWriteTimeout(30*time.Second),
    aarv.WithShutdownTimeout(10*time.Second),
    aarv.WithErrorHandler(problem.Handler(problem.Config{
        Instance: func(c *aarv.Context) string { return c.Path() },
    })),
)

app.Use(
    aarv.Recovery(),
    requestid.New(),
    logger.New(logger.Config{SkipPaths: []string{"/health"}}),
    secure.New(),
    bodylimit.New(2<<20),
)
```

Then add authentication, sessions, idempotency, observability, or OpenAPI
only when the service needs them. See:

- [`docs/middleware.md`](middleware.md) for ordering.
- [`docs/routing.md`](routing.md) for groups, route metadata, and `BindRoute`.
- [`docs/binding.md`](binding.md) for all request binding sources.
- [`docs/validation.md`](validation.md) for tags and custom rules.
- [`docs/auth.md`](auth.md) for authentication and RBAC choices.
- [`docs/session.md`](session.md) for cookie-tracked sessions.
- [`docs/security.md`](security.md) for CORS, CSRF, sanitization, and headers.
- [`docs/resilience.md`](resilience.md) for limits, timeouts, rate limiting,
  and idempotency.
- [`docs/observability.md`](observability.md) for request IDs, logging,
  metrics, tracing, health, and pprof.
- [`docs/responses.md`](responses.md) for files, uploads, streaming,
  compression, and ETags.
- [`docs/codecs.md`](codecs.md) for JSON codec selection.
- [`docs/error-handling.md`](error-handling.md) for AppError, Problem Details,
  and recovery.
- [`docs/hooks.md`](hooks.md) for lifecycle hooks.
- [`docs/testing.md`](testing.md) for handler and release checks.
- [`docs/architecture.md`](architecture.md) for request flow and module boundaries.
- [`docs/release-policy.md`](release-policy.md) for compatibility and
  submodule tagging.
- [`docs/openapi.md`](openapi.md) for generated API docs.
- [`docs/tls.md`](tls.md) for TLS, mTLS, autocert, cert reload, and h2c.

## Recommended development loop

Run these before committing changes that affect behavior:

```bash
go test ./...
go test -race ./...
golangci-lint run
govulncheck ./...
```

For submodule plugins, run tests from the submodule directory too:

```bash
cd plugins/prometheus
go test -race ./...
```

## When to add submodules

The root module intentionally stays stdlib-only. Add separate module
plugins when you need their third-party dependencies:

```bash
go get github.com/nilshah80/aarv/plugins/openapi@v0.8.0
go get github.com/nilshah80/aarv/plugins/openapi-ui@v0.8.0
go get github.com/nilshah80/aarv/plugins/prometheus@v0.8.0
go get github.com/nilshah80/aarv/plugins/otel@v0.8.0
```

Keep root-module features on `github.com/nilshah80/aarv@v0.8.0`; they do
not need a separate `go get`.
