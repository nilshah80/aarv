# Architecture guide

Aarv is a small layer over `net/http`. The core framework owns request
context pooling, routing integration, middleware execution, hooks, binding,
validation, response helpers, error handling, server lifecycle, and route
metadata. Plugins add optional behavior around that core.

## Design goals

- Keep the root module zero-dependency.
- Preserve stdlib `http.Handler` and middleware compatibility.
- Make typed handlers ergonomic without hiding `net/http`.
- Precompute route, binder, and validator metadata at registration time.
- Keep optional integrations in plugins or submodules.

## Core request flow

```text
http.Server
  -> App.ServeHTTP
  -> acquire pooled *aarv.Context
  -> OnRequest
  -> PreRouting
  -> global middleware
  -> route dispatch
  -> group middleware
  -> route middleware
  -> route body limit
  -> binding hooks / binder / validator
  -> handler
  -> OnResponse
  -> OnSend
  -> response flush
  -> release Context
```

Errors short-circuit to:

```text
error -> OnError -> configured ErrorHandler -> response
```

Panics are converted to errors only when recovery middleware is installed.

## App

`App` is the central runtime object. It owns:

- the underlying `http.ServeMux`
- route metadata
- global middleware
- hook registry
- plugin registrations
- codec and error handler
- logger
- server configuration
- context fast-path indexes for dispatch

The app is configured before serving. Do not mutate routes, middleware,
plugins, or hooks concurrently with live traffic.

## Context

`Context` wraps the current `http.ResponseWriter`, `*http.Request`, route
state, request-scoped store, codec access, logger access, and response helper
state.

Contexts are pooled with `sync.Pool`, then reset between requests. Never store
`*aarv.Context` beyond the lifetime of the request. Copy values out when a
goroutine needs them later.

Use `c.Context()` for cancellation and deadlines. Use `c.SetContext` or
`c.SetContextValue` when middleware needs to replace or enrich the stdlib
request context while preserving Aarv's context marker.

## Routing

Aarv uses Go's ServeMux method patterns. Route registration stores both the
mux handler and `RouteInfo` metadata. `Routes()` returns a deep copy so
introspection consumers cannot mutate live router state.

Route groups are prefix plus scoped middleware. Nested groups inherit parent
middleware by copying the middleware slice at group creation time.

Route options configure:

- metadata (`WithName`, `WithTags`, `WithSummary`, `WithDescription`,
  `WithOperationID`, `WithDeprecated`)
- schemas (`WithSchema`, `WithSchemaTypes`)
- documented responses (`WithResponse`)
- request content type (`WithRequestContentType`)
- per-route middleware (`WithRouteMiddleware`)
- per-route body limit (`WithRouteMaxBodySize`)
- idempotency TTL override (`WithRouteIdempotencyTTL`)

## Middleware

Aarv supports two middleware forms:

- stdlib: `func(http.Handler) http.Handler`
- native: `func(aarv.HandlerFunc) aarv.HandlerFunc`

Stdlib middleware is the compatibility path. Native middleware is the direct
`*aarv.Context` path. `aarv.RegisterNativeMiddleware` lets one middleware
provide both forms so Aarv can use the native path where possible.

Global middleware wraps all routes. Group middleware wraps routes registered in
that group. Route middleware wraps one route.

## Binding and validation

`Bind`, `BindReq`, and `BindRes` adapt typed functions to `HandlerFunc`.

For request structs, Aarv builds binder and validator plans when the route is
registered. Request-time binding reads tagged fields from path params, query,
headers, cookies, forms, files, and JSON body. Defaults are applied before
validation.

Validation failures return `*ValidationErrors`. Binding failures return
`*BindError`.

The bound request value (`Req`) is heap-allocated once per request: the binder
plan passes `&req` as `any` to the codec, binder, and validator, so it cannot
stay on the stack without abandoning those pluggable abstractions. This is a
single, expected allocation per bound request — see the escape-analysis audit in
`bind_escape_test.go`.

## Hooks

Hooks are stored by phase and sorted by priority. Fast boolean flags on `App`
avoid hook dispatch overhead when a phase has no hooks.

Request hooks run inside the request lifecycle. `OnStartup` and `OnShutdown`
run during server lifecycle. `OnError` observes the original propagated error
through `c.HookError()`.

## Responses

Response helpers live on `Context`: `JSON`, `Text`, `HTML`, `Blob`,
`NoContent`, `Redirect`, `Stream`, `File`, and `Attachment`.

The response pipeline can buffer responses so `OnSend` hooks can inspect or
modify response bytes before flush. Streaming bypasses normal buffering.
Middleware that wraps the response writer must preserve optional interfaces
when it needs streaming, flushing, hijacking, or controller support.

## Errors

Expected HTTP failures should use `*AppError`. The default error handler maps:

- `*ValidationErrors` to 422
- `*BindError` to 400
- `*AppError` to its status and code
- unknown errors to masked 500 responses

`WithErrorHandler` replaces the wire format globally. `plugins/problem`
provides RFC 7807 `application/problem+json` responses.

## Server lifecycle

`Listen`, `ListenTLS`, `ListenMutualTLS`, and `ListenServer` share one
signal-aware lifecycle:

```text
set server
OnStartup
ensure ready
serve
signal or serve return
OnShutdown hooks
legacy shutdown hooks
http.Server.Shutdown
transport cleanup
```

TLS helpers apply hardened defaults, optional HTTP/2 disablement, mTLS support,
and optional cert hot reload.

## Plugins and modules

Root plugins stay stdlib-only and live under `plugins/<name>` in the root
module. Plugins that need third-party dependencies use their own module under
`plugins/<name>/go.mod`. Codecs with optional third-party dependencies live
under `codec/<name>/go.mod`.

This split keeps the root import lightweight while allowing production
integrations such as OpenTelemetry, Prometheus, autocert, h2c, Redis-backed
middleware, and alternate JSON codecs.

## Compatibility boundaries

- Public core APIs are Go APIs; behavior should remain stable within a release
  line.
- Route metadata is the contract for OpenAPI and other introspection
  consumers.
- Plugin submodules are released with path-prefixed tags.
- The root module must not gain third-party runtime dependencies.

## Operational guidance

- Configure the app fully before serving.
- Keep plugins explicit and ordered.
- Prefer middleware for route-local behavior and hooks for lifecycle
  observation.
- Do not retain pooled contexts after a request.
- Keep root-module changes dependency-free.
- Test root and submodules separately before releases.
