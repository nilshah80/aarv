# openapi-spec example

Wires `aarv.BindRoute` typed handlers + `validate:""` tags through the
OpenAPI plugin (`plugins/openapi`) and serves both viewers from the
companion plugin (`plugins/openapi-ui`).

## Run

```bash
go run . -addr 127.0.0.1:8080
```

Then:

| URL                                          | What                                       |
|----------------------------------------------|--------------------------------------------|
| `http://127.0.0.1:8080/openapi.json`         | OpenAPI 3.1 JSON spec                      |
| `http://127.0.0.1:8080/openapi.yaml`         | Same spec rendered as YAML                 |
| `http://127.0.0.1:8080/docs`                 | Swagger UI viewer                          |
| `http://127.0.0.1:8080/redoc`                | ReDoc viewer                               |
| `POST http://127.0.0.1:8080/users`           | Echoes the validated body (try it from UI) |
| `GET  http://127.0.0.1:8080/users/{id}`      | Returns a stub user                        |

## What to verify

```bash
curl -s http://127.0.0.1:8080/openapi.json | jq '.paths."/users".post'
```

Should show:
- `summary`, `description`, `operationId`, `tags`, populated from
  `WithSummary` / `WithDescription` / `WithOperationID` / `WithTags`.
- `requestBody.content."application/json".schema.$ref` pointing at
  `#/components/schemas/CreateUserReq`.
- `responses.201`, `404`, `409`, `422` populated from `WithResponse`.
- `responses.201.content."application/json".schema.$ref` pointing at
  `CreateUserRes` (the schema attaches to the lowest 2xx code).

The `CreateUserReq` component should reflect the `validate` tags:
- `name`: `minLength: 2, maxLength: 64`, in `required`.
- `email`: `format: email`, in `required`.
- `role`: `enum: ["admin","editor","viewer"]`.

## Notes

- `securitySchemes.bearerAuth` is emitted into `components` but is NOT
  auto-attached to operations — security middleware integration is a
  12.6b non-goal. Add it with custom post-processing if you need it.
- The `/openapi.json` and `/openapi.yaml` routes self-exclude from the
  generated spec. So do `/docs` and `/redoc` (both are in
  `openapi.DefaultExclude`).
