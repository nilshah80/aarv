# Testing guide

Aarv apps are ordinary `http.Handler` values, so you can test them with the
standard `net/http/httptest` package. The framework also provides a small
`TestClient` helper for concise handler tests.

## App factory

Build apps in a function that tests can call independently.

```go
func setupApp() *aarv.App {
    app := aarv.New(aarv.WithBanner(false))
    app.Use(aarv.Recovery(), requestid.New())

    app.Get("/ping", func(c *aarv.Context) error {
        return c.Text(http.StatusOK, "pong")
    })

    return app
}
```

Avoid sharing one mutable app across tests unless the test is explicitly
checking shared behavior.

## TestClient

`NewTestClient` sends requests through the app without starting a real server.

```go
func TestPing(t *testing.T) {
    tc := aarv.NewTestClient(setupApp())

    res := tc.Get("/ping")
    res.AssertStatus(t, http.StatusOK)

    if res.Text() != "pong" {
        t.Fatalf("body = %q", res.Text())
    }
}
```

Use fluent helpers for headers, cookies, query parameters, and bearer tokens.

```go
res := tc.
    WithHeader("X-API-Key", "test-key").
    WithQuery("page", "2").
    WithBearer("token").
    Get("/users")
```

`TestResponse.JSON(&dest)` decodes response bodies.

```go
var body map[string]any
if err := res.JSON(&body); err != nil {
    t.Fatal(err)
}
```

## Testing typed handlers

Post JSON with `tc.Post`, `tc.Put`, or `tc.Patch`.

```go
func TestCreateUser(t *testing.T) {
    tc := aarv.NewTestClient(setupApp())

    res := tc.Post("/users", map[string]any{
        "name":  "Alice",
        "email": "alice@example.com",
    })

    res.AssertStatus(t, http.StatusOK)
}
```

Validation failures should assert status and machine-readable error code.

```go
res := tc.Post("/users", map[string]any{"name": "A"})
res.AssertStatus(t, http.StatusUnprocessableEntity)
```

## Raw httptest

Use raw `httptest` when you need custom methods, bodies, multipart forms,
streaming, trailers, or low-level response inspection.

```go
req := httptest.NewRequest(http.MethodPost, "/upload", body)
req.Header.Set("Content-Type", writer.FormDataContentType())

rec := httptest.NewRecorder()
app.ServeHTTP(rec, req)

if rec.Code != http.StatusOK {
    t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
}
```

## Middleware and hooks

Test middleware and hooks through real requests whenever possible.

```go
var called atomic.Bool
app.AddHook(aarv.OnError, func(c *aarv.Context) error {
    called.Store(true)
    return nil
})

app.Get("/boom", func(c *aarv.Context) error {
    return aarv.ErrBadRequest("boom")
})

aarv.NewTestClient(app).Get("/boom").AssertStatus(t, http.StatusBadRequest)
if !called.Load() {
    t.Fatal("OnError did not run")
}
```

## Recommended local checks

Run the root test suite:

```bash
go test ./...
```

Run with the race detector before releases and after middleware, session,
cache, or concurrency changes:

```bash
go test -race ./...
```

Run coverage for non-example packages when checking production readiness:

```bash
go test -coverprofile=coverage.txt -covermode=atomic ./...
go tool cover -func=coverage.txt
```

Run benchmarks after performance-sensitive changes:

```bash
go test -bench=. -benchmem ./...
```

Run vulnerability checks in the root module and any submodules being released:

```bash
govulncheck ./...
```

## Submodule plugins and codecs

Some plugins and codecs have their own `go.mod`. Test them from their module
directory as part of release prep.

```bash
cd plugins/openapi
go test ./...

cd ../../codec/segmentio
go test ./...
```

For all-module release verification, iterate the root module plus every
`plugins/*/go.mod` and `codec/*/go.mod`.

## Test data and temporary files

Use `t.TempDir()` for files created by tests. Avoid writing to shared
locations such as `/tmp` unless the test is explicitly about filesystem
integration and cleans up after itself.

## Production readiness checklist

- Unit test handlers with `NewTestClient`.
- Use raw `httptest` for multipart, streaming, and transport-level cases.
- Assert status codes and stable error codes, not only body text.
- Run `go test -race ./...` before release candidates.
- Test submodules from their own module directories.
- Keep benchmark comparisons separate from correctness tests.

## See also

- [`examples/testing/`](../examples/testing/) — runnable test suite covering
  `TestClient` flows, raw `httptest` for multipart and streaming, hooks and
  middleware assertions, and error-shape checks.
