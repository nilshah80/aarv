# SPEC.md — Detailed Technical Specification

> **Framework Codename**: `bolt` (working name)  
> **Go Version**: 1.26+  
> **License**: MIT  
> **Core Dependency Count**: 0 (zero)

---

## 1. Architecture Overview

### 1.1 Layer Diagram

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          User Application                                │
│  app.Post("/users", Bind(createUser))                                    │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │  Router      │  │  Bind[R,S]   │  │  Plugins     │  │  Hooks       │  │
│  │  (ServeMux)  │  │  (Generics)  │  │  (Scoped)    │  │  (Lifecycle) │  │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  │
│         │                 │                 │                 │          │
│  ┌──────▼─────────────────▼─────────────────▼─────────────────▼───────┐  │
│  │                       Context (pooled)                             │  │
│  │  wraps http.Request + http.ResponseWriter + store + params         │  │
│  └──────────────────────────┬────────────────────────────────────────┘  │
│                              │                                          │
│  ┌──────────────────────────▼────────────────────────────────────────┐  │
│  │                   Middleware Chain                                  │  │
│  │  func(http.Handler) http.Handler — fully stdlib compatible         │  │
│  └──────────────────────────┬────────────────────────────────────────┘  │
│                              │                                          │
│  ┌──────────────────────────▼────────────────────────────────────────┐  │
│  │              Codec Interface (pluggable JSON)                      │  │
│  │  Default: encoding/json │ Opt-in: segmentio, sonic, json/v2       │  │
│  └──────────────────────────┬────────────────────────────────────────┘  │
│                              │                                          │
├──────────────────────────────▼──────────────────────────────────────────┤
│                     net/http.Server (stdlib)                             │
│              TLS 1.2/1.3 │ HTTP/2 auto │ Graceful shutdown              │
└──────────────────────────────────────────────────────────────────────────┘
```

### 1.2 Request Lifecycle (Complete Flow)

```
TCP Connection
  │
  ▼
net/http.Server.Serve()
  │
  ▼
TLS Handshake (if HTTPS) ─── crypto/tls, HTTP/2 ALPN negotiation
  │
  ▼
http.Server.Handler.ServeHTTP(w, r)
  │
  ▼
framework.App.ServeHTTP(w, r)
  ├── Acquire Context from sync.Pool
  ├── Initialize: ctx.reset(w, r)
  │
  ▼
[OnRequest hooks] ─── request ID, rate limit check, logging start
  │
  ▼
[Global Middleware chain] ─── recovery, compress, secure headers, CORS
  │
  ▼
http.ServeMux.ServeHTTP(w, r) ─── Go 1.22+ pattern matching
  │                                 "GET /users/{id}" → handler lookup
  │                                 405 auto if method mismatch
  │                                 404 if no match
  ▼
[Group Middleware chain] ─── auth, group-specific middleware
  │
  ▼
[Route Middleware] ─── route-specific (e.g., extra rate limit)
  │
  ▼
[PreParsing hooks] ─── content-type enforcement, decompression
  │
  ▼
[Body Parsing] ─── Codec.Decode(r.Body, &req) — via Bind[Req,Res]
  │                 Multi-source: param → query → header → cookie → body
  │
  ▼
[PreValidation hooks] ─── sanitization, normalization
  │
  ▼
[Validation] ─── pre-computed struct tag rules, unsafe.Pointer field access
  │               On failure → 422 + ValidationError JSON
  │
  ▼
[PreHandler hooks] ─── authorization checks, business rules
  │
  ▼
[Handler] ─── fn(ctx, req) → (res, error)
  │            User's business logic
  │
  ▼
[OnSend hooks] ─── response transformation, compression finalization
  │
  ▼
[Response Write] ─── Codec.Encode(w, res) → buffered → set Content-Length → flush
  │
  ▼
[OnResponse hooks] ─── logging end, metrics recording
  │
  ▼
Return Context to sync.Pool
  │
  ▼
Connection reuse (HTTP/1.1 keep-alive) or stream end (HTTP/2)
```

---

## 2. Core Types — Detailed Design

### 2.1 App Struct

```go
type App struct {
    mux              *http.ServeMux
    server           *http.Server
    config           *Config
    codec            Codec
    validator        *StructValidator
    errorHandler     ErrorHandler
    logger           *slog.Logger
    ctxPool          sync.Pool
    bufPool          sync.Pool

    // Middleware
    globalMiddleware []Middleware
    middlewareChain   http.Handler  // pre-built chain (built once at Listen)

    // Hooks
    hooks            map[HookPhase][]hookEntry

    // Plugins
    plugins          []pluginEntry
    decorators       map[string]any  // shared plugin state

    // Routes (for introspection)
    routes           []RouteInfo

    // Shutdown
    shutdownHooks    []ShutdownHook
    shutdownOnce     sync.Once
}
```

**Design Decision — Why single `*http.ServeMux`?**

Go 1.22+ ServeMux is concurrent-safe for reads but NOT for writes after serving starts. All route registration MUST happen before `Listen()`. This matches .NET's builder pattern where `app.Build()` freezes the configuration. We enforce this by panicking if routes are registered after `Listen()` is called.

**Design Decision — Why `sync.Pool` for Context?**

Financial services APIs handle 10K-100K req/s. Each request allocating a new Context (with maps, buffers, etc.) creates GC pressure. `sync.Pool` recycles Context objects. The `reset()` method clears all fields without deallocating the underlying maps.

```go
// Context pool — pre-allocates with reasonable map sizes
ctxPool: sync.Pool{
    New: func() any {
        return &Context{
            store:  make(map[string]any, 8),   // request-scoped KV
            params: make(map[string]string, 4), // path params cache
        }
    },
}

// Reset reuses maps by clearing, not reallocating
func (c *Context) reset(w http.ResponseWriter, r *http.Request) {
    c.req = r
    c.res = w
    c.statusCode = 0
    c.written = false
    c.bodyCache = nil
    // Clear maps without realloc
    for k := range c.store {
        delete(c.store, k)
    }
    for k := range c.params {
        delete(c.params, k)
    }
}
```

### 2.2 Context Design — Gotchas

**GOTCHA #1: ResponseWriter wrapping**

We need a buffered `ResponseWriter` to:
- Allow `OnSend` hooks to modify the response body
- Set `Content-Length` header accurately
- Enable `ETag` computation
- Prevent partial writes on error

```go
type bufferedWriter struct {
    http.ResponseWriter
    buf        *bytes.Buffer  // pooled
    statusCode int
    committed  bool           // headers sent to client?
}

// Write goes to buffer, not directly to client
func (bw *bufferedWriter) Write(b []byte) (int, error) {
    return bw.buf.Write(b)
}

// Flush sends buffered content to the real ResponseWriter
func (bw *bufferedWriter) Flush() error {
    if bw.committed {
        return ErrAlreadyCommitted
    }
    bw.committed = true
    h := bw.ResponseWriter.Header()
    h.Set("Content-Length", strconv.Itoa(bw.buf.Len()))
    bw.ResponseWriter.WriteHeader(bw.statusCode)
    _, err := bw.buf.WriteTo(bw.ResponseWriter)
    return err
}
```

**GOTCHA #2: Streaming responses bypass buffer**

For SSE, WebSocket upgrades, or large file downloads, the buffer must be bypassed:

```go
// Stream writes directly, bypassing the buffer
func (c *Context) Stream(status int, contentType string, reader io.Reader) error {
    c.res.Header().Set("Content-Type", contentType)
    c.res.(bufferedWriter).committed = true  // mark as direct
    c.res.WriteHeader(status)
    _, err := io.Copy(c.res.(http.ResponseWriter), reader)  // use original
    return err
}
```

**GOTCHA #3: Context must not escape the handler**

Like `fasthttp.RequestCtx`, our Context is pooled and recycled. If the user stores a reference to `*Context` in a goroutine, it will be corrupted when the next request reuses it.

```
Solution: Document clearly. Provide ctx.Clone() for async use cases.
ctx.Clone() deep-copies the request-scoped store and reads body into bytes.
The cloned context has a no-op ResponseWriter (writes are discarded).
```

### 2.3 Handler Adapter — How Multiple Signatures Work

```go
// Internal type — everything converts to this
type HandlerFunc func(*Context) error

// adaptHandler converts any supported signature to HandlerFunc at REGISTRATION time
// This runs ONCE per route, not per request
func adaptHandler(handler any) HandlerFunc {
    switch h := handler.(type) {
    case HandlerFunc:
        return h
    case func(*Context) error:
        return HandlerFunc(h)
    case func(http.ResponseWriter, *http.Request):
        return func(c *Context) error {
            h(c.Response(), c.Request())
            return nil
        }
    case http.Handler:
        return func(c *Context) error {
            h.ServeHTTP(c.Response(), c.Request())
            return nil
        }
    default:
        panic(fmt.Sprintf("bolt: unsupported handler type %T", handler))
    }
}
```

**Design Decision — Why `panic` at registration, not runtime error?**

If you pass an invalid handler type, that's a programming error (like passing wrong args to `fmt.Sprintf`). It should fail immediately at startup, not silently at runtime when a request hits the route. This matches Go conventions (`regexp.MustCompile`, `template.Must`).

### 2.4 Bind Generics — Complete Implementation Strategy

```go
// Bind creates a typed handler with automatic request parsing and response serialization
// Type parameters Req and Res are resolved at COMPILE TIME by Go generics
func Bind[Req any, Res any](fn func(*Context, Req) (Res, error)) HandlerFunc {

    // ════════════════════════════════════════════════
    // REGISTRATION TIME (runs once when app.Post is called)
    // ════════════════════════════════════════════════

    // 1. Pre-compute multi-source binder for Req
    //    Inspects struct tags: json, param, query, header, cookie, form, default
    //    Uses reflect ONCE → creates []fieldBinding with offsets + parsers
    binder := buildBinder[Req]()

    // 2. Pre-compute validator for Req
    //    Inspects validate:"" struct tags
    //    Uses reflect ONCE → creates []fieldRule with offsets + check functions
    validator := buildValidator[Req]()

    // 3. Determine if Req is a struct (needs body/multi-source) or primitive
    reqNeedsBody := binderNeedsBody[Req]()

    // ════════════════════════════════════════════════
    // RETURNED CLOSURE (runs per request — THIS IS THE HOT PATH)
    // ════════════════════════════════════════════════

    return func(c *Context) error {
        var req Req  // stack-allocated if escape analysis permits

        // Step 1: Multi-source binding
        if binder != nil {
            if err := binder.bind(c, &req); err != nil {
                return &BindError{Err: err, Source: "binding"}
            }
        }

        // Step 2: Body parsing (only if needed and content exists)
        if reqNeedsBody && c.Request().ContentLength > 0 {
            if err := c.codec().Decode(c.Request().Body, &req); err != nil {
                return &BindError{Err: err, Source: "body"}
            }
        }

        // Step 3: Apply defaults for zero-value fields
        if binder != nil {
            binder.applyDefaults(&req)
        }

        // Step 4: Validation
        if validator != nil {
            if errs := validator.validate(&req); len(errs) > 0 {
                return &ValidationErrors{Errors: errs}
            }
        }

        // Step 5: Call user handler
        res, err := fn(c, req)
        if err != nil {
            return err  // error handler will serialize
        }

        // Step 6: Serialize response
        if !c.Written() {
            return c.JSON(http.StatusOK, res)
        }
        return nil
    }
}
```

**GOTCHA — Escape Analysis and Stack Allocation**

`var req Req` inside the closure may or may not be stack-allocated. If `Req` is a large struct or if `&req` escapes to the heap (e.g., passed to `json.Unmarshal` via `interface{}`), it will heap-allocate. This is UNAVOIDABLE with `encoding/json` because `Unmarshal(data, v any)` takes `any`. Sonic and segmentio have the same limitation. The only way to avoid this is `easyjson`-style code generation.

**Mitigation**: For extremely hot paths, provide a `BindPool[Req, Res]` variant that uses `sync.Pool` for the Req struct.

---

## 3. Routing — Deep Dive

### 3.1 ServeMux Pattern Syntax (Go 1.22+)

```
Pattern format: [METHOD ][HOST]/PATH

Examples:
  "GET /users"                    → GET only, exact /users
  "GET /users/{id}"               → GET with path param
  "GET /users/{id}/posts/{pid}"   → multiple path params
  "POST /users"                   → POST only
  "/users"                        → any method
  "GET /files/{path...}"          → catch-all wildcard
  "GET /users/{$}"                → exact /users, NOT /users/anything
  "example.com/api/"              → host-based routing
```

### 3.2 Route Group Implementation

```go
// Group creates a nested ServeMux with prefix stripping
func (a *App) Group(prefix string, fn func(g *RouteGroup)) *App {
    group := &RouteGroup{
        mux:        http.NewServeMux(),
        prefix:     prefix,
        parent:     a,
        middleware:  make([]Middleware, 0),
    }

    fn(group)  // user registers routes on the group

    // Mount the group's mux under the prefix on the parent
    // StripPrefix removes the prefix before the group's mux sees the request
    handler := http.StripPrefix(prefix, group.mux)

    // Wrap with group-level middleware
    for i := len(group.middleware) - 1; i >= 0; i-- {
        handler = group.middleware[i](handler)
    }

    // Register on parent: prefix + "/" to catch all sub-paths
    a.mux.Handle(prefix+"/", handler)

    return a
}
```

**GOTCHA — Trailing Slash with Groups**

If you register `app.Group("/api/v1", ...)` and inside the group register `g.Get("/users", handler)`, the actual pattern is `/api/v1/users`. But ServeMux requires the group prefix to end with `/` for subtree matching. The framework must auto-append `/` to group prefixes when mounting.

**GOTCHA — StripPrefix and Path Parameters**

`http.StripPrefix` works correctly with Go 1.22+ path parameters. After stripping `/api/v1` from `/api/v1/users/123`, the group's mux sees `/users/123` and matches `"/users/{id}"`. PathValue still works because it's set by the mux that matched.

### 3.3 Route Metadata for OpenAPI

```go
type RouteOption func(*routeConfig)

type routeConfig struct {
    name        string
    tags        []string
    description string
    deprecated  bool
    maxBodySize int64
    middleware  []Middleware
    summary     string
    operationID string
}

// Usage:
app.Get("/users/{id}", getUser,
    WithName("GetUser"),
    WithTags("Users"),
    WithDescription("Retrieve a user by ID"),
    WithOperationID("getUserById"),
)

// app.Routes() returns all registered routes for OpenAPI generation
```

---

## 4. Codec Interface — Design Decisions

### 4.1 Why Two Decode Methods?

```go
type Codec interface {
    // Stream-based: for request body (io.Reader)
    Decode(r io.Reader, v any) error
    Encode(w io.Writer, v any) error

    // Byte-based: for pre-read bodies, cached data, testing
    UnmarshalBytes(data []byte, v any) error
    MarshalBytes(v any) ([]byte, error)

    ContentType() string
}
```

**Reason**: `json.NewDecoder(r).Decode(v)` has different performance characteristics than `json.Unmarshal(data, v)`. The decoder reads in chunks and can handle streaming, but has overhead for small payloads. `Unmarshal` is faster for small payloads (< 10KB) because it avoids the decoder's buffering machinery. The framework should use the optimal method based on `Content-Length`.

```go
func (c *Context) decodeBody(v any) error {
    // Small body: read all + Unmarshal (faster)
    if c.Request().ContentLength > 0 && c.Request().ContentLength < 10240 {
        body, err := c.Body()  // reads + caches
        if err != nil {
            return err
        }
        return c.app.codec.UnmarshalBytes(body, v)
    }
    // Large/unknown body: stream decode
    return c.app.codec.Decode(c.Request().Body, v)
}
```

### 4.2 Codec Selection Matrix

```
┌──────────────────────────────────────────────────────────────────┐
│ Decision Tree for Users                                          │
│                                                                  │
│ Q: Do you need maximum portability (all OS/arch)?                │
│   YES → segmentio/encoding (2-4x faster, pure Go)               │
│   NO  ↓                                                         │
│                                                                  │
│ Q: Are you running linux/amd64 or linux/arm64 exclusively?       │
│   YES → bytedance/sonic (3-8x faster, JIT+SIMD)                 │
│   NO  → segmentio/encoding                                      │
│                                                                  │
│ Q: Can you run go generate as part of your build?                │
│   YES → mailru/easyjson (lowest allocs, codegen)                 │
│   NO  → segmentio or sonic                                       │
│                                                                  │
│ Q: Are you on Go 1.25+ and willing to use GOEXPERIMENT?          │
│   YES → encoding/json/v2 (stdlib, future-proof)                  │
│   NO  → segmentio or sonic                                       │
│                                                                  │
│ Default (no action): encoding/json (stdlib, zero-dep, slowest)   │
└──────────────────────────────────────────────────────────────────┘
```

---

## 5. Middleware — Architecture Details

### 5.1 Chain Building (Happens Once at Listen)

```go
// buildMiddlewareChain pre-builds the complete handler chain
// This is called ONCE before the server starts accepting requests
func (a *App) buildHandler() http.Handler {
    var handler http.Handler = a.mux

    // Apply global middleware in reverse (so first registered = outermost)
    for i := len(a.globalMiddleware) - 1; i >= 0; i-- {
        handler = a.globalMiddleware[i](handler)
    }

    // Wrap with framework's ServeHTTP (context pool, hooks, error handling)
    return &appHandler{
        app:     a,
        handler: handler,
    }
}
```

**Design Decision — Why `func(http.Handler) http.Handler` not custom type?**

The entire Go middleware ecosystem uses this signature. Chi, Alice, Gorilla, Negroni middleware all work out of the box. If we used a custom signature like `func(*Context) error`, users would need adapters for every existing middleware. We use the stdlib signature for the chain and our `HandlerFunc` for route handlers — two different things.

### 5.2 Stdlib Middleware Compatibility Adapter

```go
// WrapMiddleware converts a framework MiddlewareFunc to stdlib Middleware
func WrapMiddleware(fn func(next HandlerFunc) HandlerFunc) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Extract context from request context (stored by appHandler)
            ctx := r.Context().Value(ctxKey{}).(*Context)
            wrappedNext := func(c *Context) error {
                next.ServeHTTP(w, r)
                return nil
            }
            if err := fn(wrappedNext)(ctx); err != nil {
                ctx.app.errorHandler(ctx, err)
            }
        })
    }
}
```

---

## 6. Plugin System — Isolation Model

### 6.1 Why Scoped?

Fastify's greatest insight: plugins shouldn't leak state. If plugin A adds a middleware, it shouldn't affect plugin B's routes unless explicitly shared.

```
app.Register(AuthPlugin)     → adds JWT middleware to its own routes
app.Register(PublicPlugin)   → no JWT middleware here

Without scoping, AuthPlugin's middleware would apply to PublicPlugin's routes.
```

### 6.2 Plugin Dependency Resolution

```go
// Plugins can declare dependencies
type Plugin interface {
    Name() string
    Version() string
    Register(app *PluginContext) error
    Dependencies() []string  // optional — names of required plugins
}

// Registration order matters — framework validates dependencies
func (a *App) Register(plugin Plugin, opts ...PluginOption) *App {
    // Check dependencies are already registered
    for _, dep := range plugin.Dependencies() {
        if !a.hasPlugin(dep) {
            panic(fmt.Sprintf("bolt: plugin %q requires %q to be registered first", 
                plugin.Name(), dep))
        }
    }
    // ...
}
```

### 6.3 Decorator Pattern (Shared Services)

```go
// Plugin A registers a service
func (p *DatabasePlugin) Register(app *PluginContext) error {
    db := connectDB(p.config)
    app.Decorate("db", db)       // available to other plugins
    return nil
}

// Plugin B uses the service
func (p *UserPlugin) Register(app *PluginContext) error {
    db, ok := app.Resolve("db")
    if !ok {
        return fmt.Errorf("db decorator not found")
    }
    repo := NewUserRepo(db.(*sql.DB))
    app.Get("/users", func(c *Context) error { ... })
    return nil
}
```

---

## 7. Validation Engine — Implementation Architecture

### 7.1 Two-Phase Design

**Phase 1 — Registration (uses reflect)**:
- Walk struct fields via `reflect.Type`
- Parse `validate:""` tags into rules
- Record field offsets for direct memory access
- Cache everything in `*StructValidator`

**Phase 2 — Runtime (uses unsafe.Pointer)**:
- Get data pointer from `any` interface
- Add pre-computed offset to reach each field
- Read field value directly via pointer arithmetic
- Execute pre-compiled check functions

```go
type fieldRule struct {
    name     string          // JSON field name (for error messages)
    offset   uintptr         // field offset in struct
    kind     reflect.Kind    // string, int, float, etc.
    size     uintptr         // field size for bounds checking
    checks   []checkFunc     // pre-compiled validation functions
}

type checkFunc struct {
    tag     string           // "required", "min", etc.
    param   string           // "2" for min=2
    fn      func(ptr unsafe.Pointer, kind reflect.Kind) bool
}
```

**GOTCHA — Unsafe Pointer Safety**

`unsafe.Pointer` arithmetic is valid ONLY if:
1. The pointer comes from a live, non-moved object
2. The offset doesn't exceed the struct size
3. The field type matches what we read

Since we control both (reflect gives us correct offsets and types at registration), this is safe. But we MUST NOT cache the `unsafe.Pointer` between requests — only the offsets.

### 7.2 Nested Struct Validation

```go
type Address struct {
    Street string `json:"street" validate:"required"`
    City   string `json:"city" validate:"required"`
    Pin    string `json:"pin" validate:"required,len=6,numeric"`
}

type CreateUserReq struct {
    Name    string  `json:"name" validate:"required,min=2"`
    Email   string  `json:"email" validate:"required,email"`
    Address Address `json:"address" validate:"required"`  // nested validation
}

// The validator recursively builds rules for nested structs
// Each nested struct gets its own fieldRule slice, referenced by the parent
```

### 7.3 Slice/Dive Validation

```go
type BulkCreateReq struct {
    Users []CreateUserReq `json:"users" validate:"required,min=1,max=100,dive"`
}

// "dive" tells the validator to validate each element of the slice
// using the element type's validation rules
```

---

## 8. TLS & HTTP/2 — Architecture

### 8.1 How HTTP/2 Works in Go (Automatic)

```
Client connects to :8443
  │
  ▼
TLS Handshake begins
  ├── Client sends ClientHello with ALPN extension: ["h2", "http/1.1"]
  ├── Server checks tls.Config.NextProtos (auto-set by net/http)
  ├── Server responds with ServerHello, selects "h2" if available
  └── TLS session established with HTTP/2 negotiated
  │
  ▼
HTTP/2 connection: multiplexed streams over single TCP connection
  ├── Stream 1: GET /users/123
  ├── Stream 3: GET /users/456    ← concurrent, no head-of-line blocking
  └── Stream 5: POST /orders      ← concurrent
```

**GOTCHA — HTTP/2 is ONLY auto-enabled with `ListenAndServeTLS`**

If you use `ListenAndServe` (plain HTTP), you get HTTP/1.1 only. For h2c (HTTP/2 cleartext), you need `golang.org/x/net/http2/h2c` — useful for internal service-to-service in K8s where TLS termination happens at the ingress.

### 8.2 mTLS for Service Mesh / Partner APIs

```go
func (a *App) ListenMutualTLS(addr, certFile, keyFile, clientCAFile string) error {
    // Load client CA pool
    clientCACert, _ := os.ReadFile(clientCAFile)
    clientCAPool := x509.NewCertPool()
    clientCAPool.AppendCertsFromPEM(clientCACert)

    tlsConfig := &tls.Config{
        MinVersion: tls.VersionTLS12,
        ClientAuth: tls.RequireAndVerifyClientCert,  // enforce client cert
        ClientCAs:  clientCAPool,
    }

    a.server.TLSConfig = tlsConfig
    return a.server.ListenAndServeTLS(certFile, keyFile)
}
```

**Relevance**: Your MF platform likely connects to BSE/NSE, payment gateways, and partner AMCs — many require mTLS for API access.

---

## 9. Error Handling — Design Philosophy

### 9.1 Errors Are Values (Go Idiom)

Handlers return errors. The framework decides how to serialize them. This separates business logic from HTTP response formatting.

```go
// Handler just returns an error — doesn't write HTTP response
app.Get("/users/{id}", func(c *Context) error {
    user, err := repo.FindUser(c.Param("id"))
    if errors.Is(err, sql.ErrNoRows) {
        return ErrNotFound("user not found")
    }
    if err != nil {
        return ErrInternal(err)  // wraps internal error, returns 500
    }
    return c.JSON(200, user)
})
```

### 9.2 Error Handler Chain

```
Handler returns error
  │
  ▼
Is it *ValidationErrors? → 422 + validation detail JSON
  │ no
  ▼
Is it *BindError? → 400 + binding error JSON
  │ no
  ▼
Is it *AppError? → use StatusCode() + structured JSON
  │ no
  ▼
Is it a stdlib error? → 500 + generic error JSON
  │
  ▼
[OnError hooks] → error logging, alerting, metrics
  │
  ▼
Write error response to client
```

### 9.3 Custom Error Handler Override

```go
app := bolt.New(bolt.WithErrorHandler(func(c *bolt.Context, err error) {
    // Custom error format for your MF platform
    var appErr *bolt.AppError
    if errors.As(err, &appErr) {
        c.JSON(appErr.StatusCode(), map[string]any{
            "success": false,
            "error":   appErr.Code(),
            "message": appErr.Message(),
            "traceId": c.RequestID(),
        })
    } else {
        c.JSON(500, map[string]any{
            "success": false,
            "error":   "INTERNAL_ERROR",
            "traceId": c.RequestID(),
        })
    }
}))
```

---

## 10. Performance Considerations

### 10.1 Allocation Budget per Request

Target for framework overhead (excluding business logic + JSON):

```
GOAL: ≤ 3 heap allocations per request for framework code

What MUST be zero-alloc:
├── Context acquisition (pooled)
├── Path parameter access (ServeMux internal, already allocated)
├── Query parameter access (parsed by net/http, already allocated)
├── Middleware chain traversal (pre-built, no per-request alloc)
├── Hook execution (slice iteration, no alloc)
├── Validation (pre-computed rules, unsafe.Pointer, no alloc)
└── Response buffer (pooled)

What MAY allocate:
├── JSON decode (codec-dependent, 2-10 allocs)
├── JSON encode (codec-dependent, 1-5 allocs)
├── Req struct (may escape to heap if > stack limit or passed to Unmarshal)
└── Error objects (only on error paths)
```

### 10.2 Benchmarking Strategy

```
Benchmarks to include:
├── BenchmarkRouterStatic         → static route, no params
├── BenchmarkRouterParam          → /users/{id} with PathValue
├── BenchmarkRouterGroupNested    → nested group routing
├── BenchmarkContextJSON          → c.JSON(200, smallStruct)
├── BenchmarkBindSmallStruct      → Bind[Small, Small] with 5 fields
├── BenchmarkBindLargeStruct      → Bind[Large, Large] with 30 fields
├── BenchmarkValidation           → 10-field struct with mixed rules
├── BenchmarkMiddlewareChain5     → 5 middleware in chain
├── BenchmarkMiddlewareChain10    → 10 middleware in chain
├── BenchmarkFullStack            → complete request: decode → validate → handler → encode
├── BenchmarkCodecStdlib          → compare JSON codecs
├── BenchmarkCodecSegmentio       → compare JSON codecs
├── BenchmarkCodecSonic           → compare JSON codecs
└── BenchmarkParallel             → b.RunParallel for concurrency

Compare against: net/http raw, chi, echo, gin, fiber
```

---

## 11. Graceful Shutdown — Complete Flow

```go
func (a *App) Listen(addr string) error {
    a.server = &http.Server{
        Addr:              addr,
        Handler:           a.buildHandler(),
        ReadTimeout:       a.config.ReadTimeout,
        ReadHeaderTimeout: a.config.ReadHeaderTimeout,
        WriteTimeout:      a.config.WriteTimeout,
        IdleTimeout:       a.config.IdleTimeout,
        MaxHeaderBytes:    a.config.MaxHeaderBytes,
        ErrorLog:          slog.NewLogLogger(a.logger.Handler(), slog.LevelError),
    }

    // Fire OnStartup hooks
    for _, hook := range a.hooks[OnStartup] {
        if err := hook.fn(nil); err != nil {
            return fmt.Errorf("startup hook failed: %w", err)
        }
    }

    // Print banner
    if a.config.Banner {
        a.printBanner(addr)
    }

    // Listen in goroutine
    errCh := make(chan error, 1)
    go func() {
        a.logger.Info("server started", "addr", addr)
        if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            errCh <- err
        }
        close(errCh)
    }()

    // Wait for signal
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

    select {
    case sig := <-quit:
        a.logger.Info("shutdown signal received", "signal", sig.String())
    case err := <-errCh:
        return fmt.Errorf("server error: %w", err)
    }

    // Graceful shutdown
    ctx, cancel := context.WithTimeout(context.Background(), a.config.ShutdownTimeout)
    defer cancel()

    // Fire OnShutdown hooks
    for _, hook := range a.shutdownHooks {
        if err := hook(ctx); err != nil {
            a.logger.Error("shutdown hook error", "error", err)
        }
    }

    return a.server.Shutdown(ctx)
}
```

---

## 12. Security Considerations

### 12.1 Request Size Limits

```
Default limits (configurable):
├── Max header size:   1 MB  (http.Server.MaxHeaderBytes)
├── Max body size:     4 MB  (BodyLimit middleware)
├── Max form memory:   32 MB (multipart parsing)
├── Max URL length:    8 KB  (before net/http rejects)
└── Max query params:  1000  (DoS prevention)
```

### 12.2 Timeout Layers

```
Connection timeouts (net/http.Server):
├── ReadTimeout:        15s  — total time to read entire request
├── ReadHeaderTimeout:  5s   — time to read headers only
├── WriteTimeout:       15s  — total time to write response
├── IdleTimeout:        60s  — keep-alive idle before close

Request-level timeout (context middleware):
├── Timeout middleware: 30s  — context.WithTimeout per request
├── Cancellation:       client disconnect → ctx.Done() fires
```

### 12.3 IP Extraction for Rate Limiting

```
Trust hierarchy (configurable):
1. X-Real-IP (if from trusted proxy)
2. X-Forwarded-For (first untrusted IP from right)
3. r.RemoteAddr (direct connection)

GOTCHA: Never trust X-Forwarded-For from untrusted sources.
Configure TrustedProxies to specify which IPs can set forwarding headers.
```
