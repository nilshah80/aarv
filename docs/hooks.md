# Hooks guide

Aarv hooks let applications observe or interrupt request lifecycle phases
without wrapping every route. Hooks are useful for request counters, tracing,
audit logging, request enrichment, policy checks, and graceful startup or
shutdown work.

## Registering hooks

```go
app.AddHook(aarv.OnRequest, func(c *aarv.Context) error {
    c.Set("started_at", time.Now())
    return nil
})
```

Use priorities when order matters. Lower priority runs first.

```go
app.AddHookWithPriority(aarv.OnRequest, -10, installTraceContext)
app.AddHookWithPriority(aarv.OnRequest, 0, countRequest)
```

Nil hooks are ignored.

## Request lifecycle

For a typical typed request, phases run in this order:

```text
OnRequest
PreRouting
route match
global/group/route middleware and body limit
PreParsing
JSON/body parsing
defaults
PreValidation
validation
PreHandler
handler
OnResponse
OnSend
```

`OnError` runs when a hook, middleware, binder, validator, or handler returns
an error and before the configured error handler writes the response.

`OnStartup` and `OnShutdown` are server lifecycle hooks, not per-request
hooks.

## Hook phases

| Phase | Runs |
|---|---|
| `OnRequest` | immediately after Aarv acquires a request context |
| `PreRouting` | before route dispatch |
| `PreParsing` | before body decoding for bound handlers |
| `PreValidation` | after binding/defaults and before validation |
| `PreHandler` | immediately before the user handler |
| `OnResponse` | after the handler completes and before response lifecycle ends |
| `OnSend` | before buffered response bytes flush to the client |
| `OnError` | when Aarv handles an error |
| `OnStartup` | before the server starts accepting traffic |
| `OnShutdown` | when graceful shutdown starts |

Plain handlers do not run `PreParsing` or `PreValidation` unless they call
binding flows that use those phases. `PreHandler` runs for both plain and
typed handlers.

## Short-circuiting

Returning an error from a hook stops the normal flow and routes that error
through `OnError` plus the configured error handler.

```go
app.AddHook(aarv.PreHandler, func(c *aarv.Context) error {
    if c.Header("X-Blocked") == "true" {
        return aarv.ErrForbidden("request blocked")
    }
    return nil
})
```

Use this for cross-cutting policy. For route-specific policy, middleware on a
group or route is usually clearer.

## Observing errors

Use `OnError` for metrics, traces, and logs. The original error is available
as `c.HookError()`.

```go
app.AddHook(aarv.OnError, func(c *aarv.Context) error {
    c.Logger().Warn("request failed",
        "error", c.HookError(),
        "method", c.Method(),
        "path", c.Path(),
        "request_id", c.RequestID(),
    )
    return nil
})
```

`OnError` should normally return nil. Response formatting belongs in
`WithErrorHandler`; see [`docs/error-handling.md`](error-handling.md).

## Startup and shutdown

`OnStartup` runs before `ensureReady` finalizes request dispatch and before the
listener accepts traffic.

```go
app.AddHook(aarv.OnStartup, func(c *aarv.Context) error {
    return warmCache()
})
```

If `OnStartup` returns an error, serving aborts and `OnShutdown` does not run.

`OnShutdown` hooks run when graceful shutdown starts and before
`http.Server.Shutdown` drains connections.

```go
app.AddHook(aarv.OnShutdown, func(c *aarv.Context) error {
    markDraining()
    return nil
})
```

Use the legacy shutdown hook form when cleanup needs the shutdown context.

```go
app.OnShutdown(func(ctx interface{ Done() <-chan struct{} }) error {
    return closeWithDeadline(ctx)
})
```

## Response hooks

`OnResponse` is for work after a handler completes, such as latency recording.

```go
app.AddHook(aarv.OnResponse, func(c *aarv.Context) error {
    started, _ := aarv.GetTyped[time.Time](c, "started_at")
    c.Logger().Info("request completed", "latency", time.Since(started))
    return nil
})
```

`OnSend` runs before buffered response bytes are flushed. It can inspect or
modify buffered responses. Streaming responses bypass normal buffering, so do
not rely on `OnSend` for long-lived streams or file-like payloads.

## Hooks versus middleware

Use hooks for app-wide lifecycle observation or policy that truly applies
everywhere. Use middleware for request wrapping behavior, route/group-specific
policy, response writer wrapping, and compatibility with stdlib middleware.

## Production guidance

- Keep hooks small and deterministic.
- Do not perform slow network calls in per-request hooks.
- Use priorities only where order is part of the contract.
- Put response formatting in the error handler, not `OnError`.
- Use startup hooks for readiness prerequisites and fail fast on error.
- Use shutdown hooks to mark draining before the server starts connection
  shutdown.
