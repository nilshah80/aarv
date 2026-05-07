# Error handling guide

Aarv treats errors as part of the request pipeline. Handlers and middleware
return errors, `OnError` hooks observe them, and one configured
`ErrorHandler` writes the response.

## Default response shape

The default handler writes JSON and includes the request ID when the
`requestid` middleware has run.

```json
{
  "error": "not_found",
  "message": "User not found",
  "request_id": "req_123"
}
```

Unknown errors are masked as `internal_error` with the message
`Internal server error`. The original error is logged through the app logger.

## Return structured errors

Use `*aarv.AppError` for expected HTTP failures. It carries an HTTP status,
a stable machine-readable code, a client-facing message, optional detail, and
an optional internal error that is never serialized to the client.

```go
func getUser(c *aarv.Context) error {
    user, err := repo.Find(c.Context(), c.Param("id"))
    if errors.Is(err, sql.ErrNoRows) {
        return aarv.ErrNotFound("User not found")
    }
    if err != nil {
        return aarv.ErrInternal(err)
    }
    return c.JSON(http.StatusOK, user)
}
```

Useful helpers include:

| Helper | Status | Code |
|---|---:|---|
| `aarv.ErrBadRequest(msg)` | 400 | `bad_request` |
| `aarv.ErrUnauthorized(msg)` | 401 | `unauthorized` |
| `aarv.ErrForbidden(msg)` | 403 | `forbidden` |
| `aarv.ErrNotFound(msg)` | 404 | `not_found` |
| `aarv.ErrConflict(msg)` | 409 | `conflict` |
| `aarv.ErrPayloadTooLarge(msg)` | 413 | `payload_too_large` |
| `aarv.ErrUnprocessable(msg)` | 422 | `validation_failed` |
| `aarv.ErrTooManyRequests(msg)` | 429 | `too_many_requests` |
| `aarv.ErrInternal(err)` | 500 | `internal_error` |
| `aarv.ErrBadGateway(msg)` | 502 | `bad_gateway` |
| `aarv.ErrServiceUnavailable(msg)` | 503 | `service_unavailable` |
| `aarv.ErrGatewayTimeout(msg)` | 504 | `gateway_timeout` |

Use `WithDetail` only for detail that is safe to show to clients.

```go
return aarv.ErrConflict("Email is already registered").
    WithDetail("email must be unique within the tenant")
```

Use `WithInternal` or `ErrInternal` for operational diagnostics.

```go
return aarv.NewError(http.StatusBadGateway, "upstream_failed", "Upstream failed").
    WithInternal(err)
```

## Binding and validation errors

`Bind`, `BindReq`, and binder helpers return framework error types when
request input fails:

- `*aarv.BindError` becomes HTTP 400.
- `*aarv.ValidationErrors` becomes HTTP 422 with per-field details.

Keep handler code focused on business errors. Let the binder produce the
input error response.

```go
type CreateUserReq struct {
    Email string `json:"email" validate:"required,email"`
    Name  string `json:"name" validate:"required,min=2"`
}

app.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (User, error) {
    return service.Create(c.Context(), req)
}))
```

## Install Problem Details

For public APIs, prefer `plugins/problem` so clients receive RFC 7807
`application/problem+json` responses.

```go
import (
    "github.com/nilshah80/aarv"
    "github.com/nilshah80/aarv/plugins/problem"
)

app := aarv.New(
    aarv.WithErrorHandler(problem.Handler(problem.Config{
        Type: "https://api.example.com/problems",
        TypeForCode: map[string]string{
            "validation_failed": "https://api.example.com/problems/validation",
            "unauthorized":      "https://api.example.com/problems/unauthorized",
            "forbidden":         "https://api.example.com/problems/forbidden",
        },
        Instance: func(c *aarv.Context) string {
            return c.Path()
        },
        OnInternal: func(c *aarv.Context, err error) {
            c.Logger().Error("request failed", "error", err)
        },
    })),
)
```

The problem handler maps:

- `*aarv.AppError` to status, title, detail, and problem type.
- `*aarv.ValidationErrors` to 422 with an `errors` extension.
- unknown errors to a masked 500 response.

It also adds `request_id` as an extension when available.

## Custom error handlers

Use `aarv.WithErrorHandler` when you need a different wire shape. A handler
must write exactly one response and should keep unknown errors masked.

```go
app := aarv.New(aarv.WithErrorHandler(func(c *aarv.Context, err error) {
    var appErr *aarv.AppError
    if errors.As(err, &appErr) {
        _ = c.JSON(appErr.StatusCode(), map[string]any{
            "code":       appErr.Code(),
            "message":    appErr.Message(),
            "request_id": c.RequestID(),
        })
        return
    }

    c.Logger().Error("unhandled error", "error", err)
    _ = c.JSON(http.StatusInternalServerError, map[string]any{
        "code":       "internal_error",
        "message":    "Internal server error",
        "request_id": c.RequestID(),
    })
}))
```

Do not write stack traces, SQL errors, secrets, tokens, or upstream response
bodies to clients.

## Observe errors with `OnError`

`OnError` runs before the configured error handler. Use it for metrics,
tracing, and audit logging. The propagated error is available through
`c.HookError()`.

```go
app.AddHook(aarv.OnError, func(c *aarv.Context) error {
    err := c.HookError()
    c.Logger().Warn("request error",
        "error", err,
        "method", c.Method(),
        "path", c.Path(),
    )
    return nil
})
```

An `OnError` hook should normally return nil. Returning another error from
the hook does not replace the original response error.

## Recover panics

Install recovery early in the global middleware chain. The built-in
`aarv.Recovery()` converts panics into `ErrInternal` and routes them through
the app error handler. The `plugins/recover` package offers stack capture and
a custom panic response hook.

```go
app.Use(
    aarv.Recovery(),
    requestid.New(),
    logger.New(),
)
```

Production services should treat panic recovery as a last-resort guard. Return
ordinary errors for expected failures so clients get stable status codes and
your logs keep a useful signal-to-noise ratio.

## Production checklist

- Install `requestid` before middleware that logs or returns errors.
- Put recovery early in the global chain.
- Return `*aarv.AppError` for expected client and upstream failures.
- Wrap private details with `ErrInternal` or `WithInternal`, not `WithDetail`.
- Use `plugins/problem` for external APIs that need a stable error contract.
- Use `OnError` for metrics/logging, not for response formatting.
- Keep validation messages client-safe because they are serialized.
