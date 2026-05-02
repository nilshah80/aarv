# OpenAPI guide

[`plugins/openapi`](../plugins/openapi/) generates an OpenAPI 3.1 / JSON
Schema 2020-12 specification from your aarv App's registered routes.
[`plugins/openapi-ui`](../plugins/openapi-ui/) mounts Swagger UI and
ReDoc viewers against that spec.

## Quickstart

```go
import (
    "github.com/nilshah80/aarv"
    "github.com/nilshah80/aarv/plugins/openapi"
    openapiui "github.com/nilshah80/aarv/plugins/openapi-ui"
)

type CreateUserReq struct {
    Name string `json:"name" validate:"required,min=2"`
}
type CreateUserRes struct {
    ID string `json:"id"`
}

app := aarv.New()
aarv.BindRoute(app, "POST", "/users",
    func(c *aarv.Context, req CreateUserReq) (CreateUserRes, error) {
        return CreateUserRes{ID: "u_1"}, nil
    },
    aarv.WithSummary("Create user"),
    aarv.WithResponse(201, "Created"),
)

// Generate the spec AFTER all routes are registered.
openapi.New(app, openapi.Config{Title: "Users API", Version: "1.0.0"})
openapiui.Mount(app, openapiui.Config{Title: "Users API"})
```

The spec is served at `/openapi.json` and `/openapi.yaml`; viewers at
`/docs` (Swagger UI) and `/redoc`. All four paths self-exclude from the
generated document.

## Lazy-cache contract

Spec generation is lazy: the first request to `/openapi.json` (or
`/openapi.yaml`) builds the document via `sync.Once` and caches it for
the App lifetime.

**Register all routes BEFORE the first spec request.** Routes added
after that point are not reflected — the freeze point is first request,
not `openapi.New`.

## Metadata sources

| Source                              | RouteInfo field      | Spec location                     |
|-------------------------------------|----------------------|-----------------------------------|
| `aarv.WithSummary("...")`           | `Summary`            | operation.summary                 |
| `aarv.WithDescription("...")`       | `Description`        | operation.description             |
| `aarv.WithOperationID("...")`       | `OperationID`        | operation.operationId             |
| `aarv.WithTags("a", "b")`           | `Tags`               | operation.tags                    |
| `aarv.WithDeprecated()`             | `Deprecated`         | operation.deprecated              |
| `aarv.WithSchema(req, res)`         | `RequestType`/`ResponseType` | requestBody, responses[*].content schemas |
| `aarv.WithSchemaTypes(rt, st)`      | (same, precise form) | (same)                            |
| `aarv.WithResponse(code, "desc")`   | `Responses[code]`    | responses[code].description       |
| `aarv.WithRequestContentType(ct)`   | `RequestContentType` | requestBody.content media key     |
| `aarv.BindRoute[Req, Res]`          | auto-applies WithSchemaTypes | (same as WithSchema)      |

The default request body media type is `App.CodecContentType()` (set
via `aarv.WithCodec`); per-route `WithRequestContentType` overrides
that. Response body media type follows the same rule.

## `validate:""` tag mapping

The plugin reads aarv's `validate` struct tag on each field of the
schema type and translates recognized rules into OpenAPI / JSON Schema
constraints. Unknown tags are ignored with a `slog.Debug` entry.

| Tag                         | OpenAPI / JSON Schema                                        |
|-----------------------------|--------------------------------------------------------------|
| `required`                  | Field appended to schema.required (overrides JSON omitempty) |
| `min=N` (numeric)           | minimum: N                                                   |
| `min=N` (string)            | minLength: N                                                 |
| `min=N` (slice/map/array)   | minItems: N                                                  |
| `max=N`                     | maximum / maxLength / maxItems (mirror of min)               |
| `gte=N`                     | minimum: N (numeric only)                                    |
| `lte=N`                     | maximum: N (numeric only)                                    |
| `gt=N`                      | exclusiveMinimum: N                                          |
| `lt=N`                      | exclusiveMaximum: N                                          |
| `len=N` (string)            | minLength = maxLength = N                                    |
| `len=N` (slice/array)       | minItems = maxItems = N                                      |
| `oneof=red green blue`      | enum: ["red","green","blue"]                                 |
| `email`                     | format: email                                                |
| `url`                       | format: uri                                                  |
| `uuid`                      | format: uuid                                                 |
| `regex=^[a-z]+$`            | pattern: ^[a-z]+$                                            |
| `unique`                    | uniqueItems: true                                            |

### Required-field precedence

`validate:"required"` always wins over JSON `omitempty`. A field tagged
`json:"name,omitempty" validate:"required"` IS required in the spec.
A field tagged `json:"name,omitempty"` (no validate) is optional.

## Components and recursion

Named struct types resolve to a single `#/components/schemas/{Name}`
ref and are emitted once under `components.schemas`. Recursive types
terminate via the component-placeholder pattern: the component entry
is staked out before field walking, so a recursive reference
encountered during walk resolves to a `$ref` pointing at the
in-progress component.

Anonymous struct types are inlined at the use site (no component).

### Component naming rule

1. First occurrence of a `TypeName` gets the bare name.
2. Collision with a different type: the second one is qualified as
   `pkgpath_TypeName` with non-`[A-Za-z0-9_]` characters replaced by
   `_`, runs collapsed, leading/trailing underscores trimmed.
3. Tie-breaker collision (same simple name in same package): numeric
   suffix `_2`, `_3`, …

### Nullable encoding (OpenAPI 3.1)

Nullable (pointer) fields render as the JSON Schema 2020-12 union form,
not the deprecated 3.0 `nullable: true` keyword:

- Primitive: `type: ["string", "null"]`
- `$ref`: `oneOf: [{$ref}, {type: null}]` (since 3.1 forbids
  `nullable` siblings of `$ref`)
- Typeless (interface{}): `type: "null"`

## Catch-all routes

Aarv supports `/files/{path...}` catch-all syntax. The OpenAPI plugin
normalizes the path to `/files/{path}` and emits a single string-typed
path parameter named `path`. OpenAPI 3.1 has no native catch-all
concept; consumers should treat the parameter as an opaque string that
may contain slashes.

## Security schemes

`Config.SecuritySchemes` populates `components.securitySchemes`. The
plugin does NOT auto-attach security requirements to operations from
middleware — set per-operation `security` via custom post-processing
if you need it.

```go
openapi.New(app, openapi.Config{
    SecuritySchemes: map[string]openapi.SecurityScheme{
        "bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
    },
})
```

## Filtering routes

| Field                  | Behavior                                                |
|------------------------|---------------------------------------------------------|
| `Config.Include`       | When non-nil, the SOLE filter; `Exclude` is ignored.    |
| `Config.Exclude`       | Path-prefix list. Routes whose Pattern starts with any entry are dropped. |
| `DefaultExclude`       | `["/openapi.json", "/openapi.yaml", "/docs", "/redoc"]` so the spec does not document its own viewer routes. |

Custom `JSONPath` / `YAMLPath` are auto-added to `Exclude` so the
generator does not self-document even at non-default endpoints.

## YAML output

`/openapi.yaml` is generated by round-tripping the JSON spec through
`sigs.k8s.io/yaml.JSONToYAML`. This preserves `encoding/json`'s
deterministic key ordering. Set `Config.YAMLPath = ""` to use the
default; set `Config.DisableYAMLEndpoint = true` to suppress
registration entirely.

## Non-goals

- Polymorphism / discriminator-based schemas.
- Full custom `MarshalJSON` introspection (the plugin walks struct
  fields via reflection, not via simulated marshal).
- Non-string-keyed maps (degraded to bare `object` schemas).
- Automatic `operation.security` from middleware.

## See also

- [`examples/openapi-spec/`](../examples/openapi-spec/) — runnable end-to-end demo
- [`plugins/openapi-ui/ASSETS.md`](../plugins/openapi-ui/ASSETS.md) — vendored UI bundle versions + update procedure
