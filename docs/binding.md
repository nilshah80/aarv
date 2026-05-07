# Binding guide

Aarv binding turns request data into typed structs. It supports path
parameters, query parameters, headers, cookies, form values, multipart files,
JSON bodies, defaults, custom parsers, and validation.

Binding metadata is precomputed when the route is registered, so request-time
work stays focused on reading the request and setting fields.

## Handler forms

Use `Bind` when the handler returns a response value that should be serialized
as JSON with status 200.

```go
app.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (UserRes, error) {
    return service.Create(c.Context(), req)
}))
```

Use `BindReq` when the handler writes the response itself.

```go
app.Get("/users/{id}", aarv.BindReq(func(c *aarv.Context, req GetUserReq) error {
    user, err := service.Get(c.Context(), req.ID)
    if err != nil {
        return err
    }
    return c.JSON(http.StatusOK, user)
}))
```

Use `BindRes` when there is no request struct but you still want automatic
JSON serialization.

```go
app.Get("/stats", aarv.BindRes(func(c *aarv.Context) (Stats, error) {
    return collectStats(c.Context())
}))
```

## Sources

Bind fields with struct tags:

| Tag | Source |
|---|---|
| `param:"id"` | path parameter |
| `query:"page"` | query string |
| `header:"X-API-Key"` | request header |
| `cookie:"session"` | cookie |
| `form:"title"` | form or multipart text field |
| `file:"avatar"` | multipart file |
| `json:"name"` | JSON request body |
| `default:"1"` | default for zero-value fields |
| `validate:"required"` | validation rules |

Example:

```go
type CreateUserReq struct {
    OrgID   string `param:"orgId" validate:"required"`
    DryRun  bool   `query:"dryRun" default:"false"`
    TraceID string `header:"X-Trace-ID"`
    Name    string `json:"name" validate:"required,min=2"`
    Email   string `json:"email" validate:"required,email"`
    Role    string `json:"role" validate:"oneof=member admin"`
}

app.Post("/orgs/{orgId}/users", aarv.Bind(createUser))
```

## Binding order

For `Bind` and `BindReq`, Aarv resolves request data in this order:

1. Multi-source fields: path, query, header, cookie, form, file.
2. JSON body when the struct has JSON fields and the request body is present.
3. Defaults for zero-value fields.
4. `PreValidation` hooks.
5. Validation.
6. `PreHandler` hooks.
7. User handler.

`PreParsing` runs immediately before JSON body decoding. Binding failures
become `*aarv.BindError`; validation failures become
`*aarv.ValidationErrors`.

## Defaults

Defaults are applied after request sources are read and before validation.
They only fill zero-value fields.

```go
type ListReq struct {
    Page  int    `query:"page" default:"1" validate:"gte=1"`
    Limit int    `query:"limit" default:"50" validate:"gte=1,lte=100"`
    Sort  string `query:"sort" default:"created_at"`
}
```

For booleans, integers, floats, and strings, defaults use the same parsing
rules as request values.

## Supported scalar conversions

Path, query, header, cookie, and form values are strings. Aarv converts them
to:

- `string`
- signed and unsigned integers
- floats
- booleans accepted by `strconv.ParseBool`
- `[]string`, split on commas
- custom types implementing `aarv.ParamParser`

Unsupported kinds fail binding with a 400 response.

## Custom parameter parsing

Implement `ParamParser` for domain-specific values.

```go
type UserID string

func (id *UserID) ParseParam(raw string) error {
    if !strings.HasPrefix(raw, "usr_") {
        return errors.New("invalid user id")
    }
    *id = UserID(raw)
    return nil
}

type GetUserReq struct {
    ID UserID `param:"id"`
}
```

## Custom binding

Implement `CustomBinder` when a request type needs full control over binding.

```go
type SignedReq struct {
    Tenant string
    Body   []byte
}

func (r *SignedReq) BindFromContext(c *aarv.Context) error {
    r.Tenant = c.Header("X-Tenant")
    body, err := c.Body()
    if err != nil {
        return err
    }
    r.Body = body
    return nil
}
```

Custom binders still flow through validation after binding.

## Multipart files

File fields must be `*aarv.UploadedFile` or `[]*aarv.UploadedFile`.

```go
type UploadReq struct {
    Title string               `form:"title" validate:"required"`
    Image *aarv.UploadedFile   `file:"image" validate:"required"`
    Docs  []*aarv.UploadedFile `file:"docs"`
}
```

Set a body limit globally or per route before accepting file uploads:

```go
app.Post("/upload", aarv.BindReq(upload), aarv.WithRouteMaxBodySize(20<<20))
```

## Manual binding helpers

For lower-level handlers, use context helpers:

```go
var req SearchReq
if err := c.BindQuery(&req); err != nil {
    return aarv.ErrBadRequest("invalid query")
}
```

`c.BindJSON`, `c.BindQuery`, and `c.BindForm` are useful when only part of a
request should be bound or when the response flow is unusual.

## Error behavior

- Parse/conversion failures return `*aarv.BindError` and default to 400.
- Validation failures return `*aarv.ValidationErrors` and default to 422.
- Unknown handler errors go through the configured error handler.

See [`docs/error-handling.md`](error-handling.md) for the response contract.

## Production guidance

- Keep request structs small and route-specific.
- Prefer explicit tags over relying on field names.
- Use defaults for stable API behavior, not hidden business policy.
- Use `BindRoute` for externally documented endpoints so schema metadata is
  available automatically.
- Put upload routes behind explicit body limits.
- Keep validation messages safe for clients because they are serialized.
