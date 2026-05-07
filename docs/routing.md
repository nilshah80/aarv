# Routing guide

Aarv routes are thin wrappers around Go 1.22+ `net/http` method patterns. The
framework keeps the standard matching model while adding typed handlers, route
groups, route metadata, per-route middleware, and introspection through
`App.Routes()`.

## Basic routes

Register handlers with the method helpers:

```go
app := aarv.New()

app.Get("/health", func(c *aarv.Context) error {
    return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
})

app.Post("/users", aarv.Bind(createUser))
app.Put("/users/{id}", aarv.BindReq(updateUser))
app.Delete("/users/{id}", deleteUser)
```

Available helpers are `Get`, `Post`, `Put`, `Delete`, `Patch`, `Head`,
`Options`, and `Any`. `Any` registers the same handler for `GET`, `POST`,
`PUT`, `DELETE`, `PATCH`, `HEAD`, and `OPTIONS`.

Handlers can be:

- `func(*aarv.Context) error`
- `aarv.HandlerFunc`
- stdlib handlers adapted through `aarv.Adapt`
- typed handlers created with `aarv.Bind`, `aarv.BindReq`, or `aarv.BindRes`

## Path parameters

Use ServeMux path parameters in the pattern and read them from `Context`.

```go
app.Get("/users/{id}", func(c *aarv.Context) error {
    id := c.Param("id")
    return c.JSON(http.StatusOK, map[string]string{"id": id})
})
```

Typed request binding can populate path parameters directly:

```go
type GetUserReq struct {
    ID string `param:"id" validate:"required"`
}

app.Get("/users/{id}", aarv.BindReq(func(c *aarv.Context, req GetUserReq) error {
    return c.JSON(http.StatusOK, findUser(req.ID))
}))
```

Use the conversion helpers when writing manual handlers:

```go
id, err := c.ParamInt("id")
if err != nil {
    return aarv.ErrBadRequest("invalid user id")
}
```

## Route groups

Groups apply a prefix and scoped middleware to routes registered inside the
group. Nested groups inherit parent group middleware.

```go
app.Group("/api", func(api *aarv.RouteGroup) {
    api.Use(apiVersion("v1"))

    api.Get("/health", health)

    api.Group("/admin", func(admin *aarv.RouteGroup) {
        admin.Use(requireAdmin)
        admin.Delete("/users/{id}", deleteUser)
    })
})
```

Execution order for grouped routes is:

```text
global middleware -> parent group middleware -> nested group middleware -> route middleware -> handler
```

Use groups for API versions, authenticated subtrees, tenant boundaries, and
admin surfaces. Do not use groups only for visual organization if they would
make middleware inheritance hard to reason about.

## Per-route middleware and body limits

Attach middleware to one route with `WithRouteMiddleware`.

```go
app.Post(
    "/uploads",
    uploadHandler,
    aarv.WithRouteMiddleware(auditUploads),
    aarv.WithRouteMaxBodySize(10<<20),
)
```

`WithRouteMaxBodySize` overrides the app-level `WithMaxBodySize` for that
route. The limit is applied before the handler reads the body.

## Route metadata

Route options populate `RouteInfo`, which powers introspection and the OpenAPI
plugin.

```go
app.Get(
    "/users/{id}",
    getUser,
    aarv.WithName("getUser"),
    aarv.WithOperationID("getUser"),
    aarv.WithSummary("Get user"),
    aarv.WithDescription("Returns one user by id."),
    aarv.WithTags("users"),
    aarv.WithResponse(200, "User found"),
    aarv.WithResponse(404, "User not found"),
)
```

Use stable `OperationID` values for generated client compatibility. Use tags
to group routes in OpenAPI UI and API reference pages.

## Typed route registration

`BindRoute` registers a typed handler and automatically stores request and
response schema types on the route metadata.

```go
type CreateUserReq struct {
    Name  string `json:"name" validate:"required,min=2"`
    Email string `json:"email" validate:"required,email"`
}

type CreateUserRes struct {
    ID string `json:"id"`
}

aarv.BindRoute(app, "POST", "/users",
    func(c *aarv.Context, req CreateUserReq) (CreateUserRes, error) {
        return CreateUserRes{ID: "usr_1"}, nil
    },
    aarv.WithSummary("Create user"),
    aarv.WithTags("users"),
    aarv.WithResponse(201, "Created"),
)
```

For grouped typed routes, use `BindGroupRoute`.

```go
app.Group("/api", func(api *aarv.RouteGroup) {
    aarv.BindGroupRoute(api, "POST", "/users", createUser)
})
```

When using `app.Get` / `app.Post` with `aarv.Bind` directly, attach schema
metadata explicitly if an OpenAPI consumer needs it.

```go
app.Post("/users", aarv.Bind(createUser), aarv.WithSchema(CreateUserReq{}, CreateUserRes{}))
```

## Mounting stdlib handlers

Use `Mount` for existing `http.Handler` trees.

```go
app.Mount("/debug", http.DefaultServeMux)
```

Mounted handlers keep the Aarv request context marker, so compatible
middleware can still use `aarv.FromRequest(r)` when needed.

## Route introspection

`Routes()` returns a deep copy of registered route metadata.

```go
for _, r := range app.Routes() {
    fmt.Println(r.Method, r.Pattern, r.OperationID)
}
```

The returned slices, maps, and pointer fields can be mutated safely by callers
without changing the live router.

## 404, 405, and trailing slash behavior

Aarv detects method mismatches and returns 405 when a path exists for another
method. Unmatched paths return 404 through the framework error pipeline.

Trailing slash redirect is configurable through the app option used by the
router. Keep it consistent across services so generated links, reverse
proxies, and clients see one canonical path style.

## Production guidance

- Prefer typed `BindRoute` for public APIs that will generate OpenAPI specs.
- Keep route middleware narrow and local to the behavior it protects.
- Put authentication on groups instead of repeating route middleware.
- Use stable `OperationID` values before publishing clients.
- Use route metadata as documentation, not as business logic.
