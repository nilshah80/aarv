# Go Web Framework — Complete Specification & Feature Guide

> **Codename**: TBD  
> **Go Version**: 1.26+ (leverages Go 1.22+ enhanced ServeMux)  
> **Philosophy**: Zero-dependency core, pluggable everything, fastest possible hot path  
> **Inspirations**: .NET Minimal API (binding/DX) + Fastify (plugins/hooks) + Mach/Bottle (minimalism)

---

## 1. JSON Library Decision — FINAL

### Approach: Pluggable Codec with `segmentio/encoding` as Recommended Fast Default

| Layer | Library | Why |
|---|---|---|
| **Framework core ships with** | `encoding/json` (stdlib) | Zero-dependency baseline. Works everywhere. |
| **Recommended fast codec** | `segmentio/encoding/json` | 2-4x faster than stdlib, pure Go, all platforms, stable, drop-in API compatible. Used by Segment/Twilio in production. |
| **Maximum perf (amd64/arm64)** | `bytedance/sonic` | Fastest (JIT+SIMD), but platform-limited, `!go1.27` build tag, arm64 bugs. |
| **Future-proof** | `encoding/json/v2` + `encoding/json/jsontext` | Experimental in Go 1.25 (`GOEXPERIMENT=jsonv2`). Working group active. Will become the stdlib default eventually. |
| **Lowest allocs (codegen)** | `mailru/easyjson` | Fastest with fewest allocations but needs `go generate` — breaks zero-ceremony DX. |

### Framework Codec Interface

```
Interface: Codec
├── Encode(w io.Writer, v any) error
├── Decode(r io.Reader, v any) error
├── MarshalBytes(v any) ([]byte, error)      // for pre-serialization
├── UnmarshalBytes(data []byte, v any) error  // for buffered decode
└── ContentType() string
```

### Codec Sub-Packages (separate go modules — no dependency pollution)

```
framework/                    ← zero dependencies
framework/codec/segmentio/    ← imports segmentio/encoding
framework/codec/sonic/        ← imports bytedance/sonic
framework/codec/jsonv2/       ← imports go-json-experiment/json (or stdlib when stable)
framework/codec/easyjson/     ← for users who run go generate
```

---

## 2. Routing

### 2.1 Foundation: Go 1.22+ `http.ServeMux`

| Feature | Support | How |
|---|---|---|
| Method routing | ✅ | `"GET /users/{id}"` pattern syntax |
| Path parameters | ✅ | `{id}`, accessed via `r.PathValue("id")` |
| Wildcard catch-all | ✅ | `{path...}` for remaining segments |
| Exact match | ✅ | `{$}` suffix prevents subtree matching |
| Host-based routing | ✅ | `"example.com/path"` pattern prefix |
| 405 Method Not Allowed | ✅ | Automatic when method doesn't match but path does |
| 404 Not Found | ✅ | Customizable via framework error handler |

### 2.2 Framework Router Enhancements (on top of ServeMux)

| Feature | Description |
|---|---|
| **Fluent registration** | `app.Get()`, `app.Post()`, `app.Put()`, `app.Delete()`, `app.Patch()`, `app.Head()`, `app.Options()`, `app.Any()` |
| **Route groups** | `app.Group("/api/v1", func(g *RouteGroup) { ... })` with nested groups |
| **Group prefix** | Auto-prepends prefix via nested `ServeMux` + `http.StripPrefix` |
| **Scoped middleware** | Middleware applied to group affects only routes within that group |
| **Route metadata** | `WithName("getUser")`, `WithTags("users")`, `WithDescription(...)` for OpenAPI |
| **Route listing** | `app.Routes()` returns all registered routes with metadata |
| **Custom 404 handler** | `app.SetNotFoundHandler(handler)` |
| **Custom 405 handler** | `app.SetMethodNotAllowedHandler(handler)` |
| **Trailing slash redirect** | Configurable: redirect, strip, or strict |
| **Sub-routing** | Mount entire sub-apps: `app.Mount("/admin", adminApp)` |

### 2.3 Route Registration Interfaces

```
Interface: Router
├── Get(pattern string, handler any, opts ...RouteOption) Router
├── Post(pattern string, handler any, opts ...RouteOption) Router
├── Put(pattern string, handler any, opts ...RouteOption) Router
├── Delete(pattern string, handler any, opts ...RouteOption) Router
├── Patch(pattern string, handler any, opts ...RouteOption) Router
├── Head(pattern string, handler any, opts ...RouteOption) Router
├── Options(pattern string, handler any, opts ...RouteOption) Router
├── Any(pattern string, handler any, opts ...RouteOption) Router      // all methods
├── Group(prefix string, fn func(RouteGroup)) Router
├── Mount(prefix string, handler http.Handler) Router
├── Use(middlewares ...Middleware) Router
└── Routes() []RouteInfo

Interface: RouteGroup  (same methods as Router, scoped to prefix)

Struct: RouteInfo
├── Method      string
├── Pattern     string
├── Name        string
├── Tags        []string
├── Middleware   []string   // names of applied middleware
└── Handler     string     // function name via reflection (debug only)

Struct: RouteOption (functional options)
├── WithName(name string)
├── WithTags(tags ...string)
├── WithDescription(desc string)
├── WithDeprecated()
├── WithMiddleware(mw ...Middleware)
└── WithMaxBodySize(bytes int64)
```

---

## 3. Binding System

### 3.1 Handler Signatures Supported

| Signature | Use Case | Binding |
|---|---|---|
| `func(*Context) error` | Manual control | No auto-binding |
| `func(*Context, Req) error` | Body/params → Req, manual response | `BindReq[Req]` |
| `func(*Context, Req) (Res, error)` | Body → Req, Res → JSON auto-response | `Bind[Req, Res]` |
| `func(*Context) (Res, error)` | No request body, auto-response | `BindRes[Res]` |
| `func(http.ResponseWriter, *http.Request)` | stdlib compat | `Adapt()` |
| `http.Handler` | stdlib interface | Direct mount |

### 3.2 Binding Sources (struct tag driven)

| Source | Struct Tag | Description | Coercion |
|---|---|---|---|
| **JSON body** | `json:"name"` | Parsed via Codec interface | Native JSON types |
| **Path params** | `param:"id"` | From `r.PathValue()` | string → int, int64, uint, float64, bool, uuid |
| **Query params** | `query:"page"` | From `r.URL.Query()` | string → int, int64, float64, bool, []string |
| **Headers** | `header:"X-Api-Key"` | From `r.Header.Get()` | string only |
| **Cookies** | `cookie:"session_id"` | From `r.Cookie()` | string only |
| **Form data** | `form:"username"` | From `r.FormValue()` | string, multipart |
| **Default values** | `default:"20"` | Fallback if source is empty | Per-type coercion |

### 3.3 Binding Interfaces

```
Interface: Binder
├── Bind(c *Context, dest any) error
├── BindBody(c *Context, dest any) error
├── BindParams(c *Context, dest any) error
├── BindQuery(c *Context, dest any) error
├── BindHeaders(c *Context, dest any) error
└── BindAll(c *Context, dest any) error       // all sources merged

Interface: CustomBinder  (user-implemented on their types)
├── BindFromContext(c *Context) error         // like .NET IBindableFromHttpContext<T>

Interface: ParamParser  (user-implemented for custom types)
├── ParseParam(value string) error            // like .NET TryParse
```

### 3.4 Binding Pipeline (per request)

```
Raw Request
  │
  ├─ Path params    → r.PathValue()      → struct fields tagged `param:""`
  ├─ Query params   → r.URL.Query()      → struct fields tagged `query:""`
  ├─ Headers        → r.Header.Get()     → struct fields tagged `header:""`
  ├─ Cookies        → r.Cookie()         → struct fields tagged `cookie:""`
  ├─ Body (JSON)    → Codec.Decode()     → struct fields tagged `json:""`
  └─ Form data      → r.FormValue()      → struct fields tagged `form:""`
  │
  ▼
  Apply defaults for zero-value fields with `default:""` tag
  │
  ▼
  Validate via `validate:""` tag rules
  │
  ▼
  Pass to handler as concrete typed parameter
```

---

## 4. Validation

### 4.1 Built-in Validation Rules (zero-dependency, struct tag based)

| Rule | Applies To | Example |
|---|---|---|
| `required` | all | `validate:"required"` |
| `min=N` | string (length), int/float (value), slice (length) | `validate:"min=2"` |
| `max=N` | string (length), int/float (value), slice (length) | `validate:"max=100"` |
| `gte=N` | numeric | `validate:"gte=0"` |
| `lte=N` | numeric | `validate:"lte=150"` |
| `gt=N` | numeric | `validate:"gt=0"` |
| `lt=N` | numeric | `validate:"lt=100"` |
| `len=N` | string, slice, map | `validate:"len=10"` |
| `oneof=a b c` | string, int | `validate:"oneof=admin user mod"` |
| `email` | string | `validate:"email"` |
| `url` | string | `validate:"url"` |
| `uuid` | string | `validate:"uuid"` |
| `alpha` | string | `validate:"alpha"` |
| `numeric` | string | `validate:"numeric"` |
| `alphanum` | string | `validate:"alphanum"` |
| `datetime=layout` | string | `validate:"datetime=2006-01-02"` |
| `ip` | string | `validate:"ip"` |
| `ipv4` | string | `validate:"ipv4"` |
| `ipv6` | string | `validate:"ipv6"` |
| `cidr` | string | `validate:"cidr"` |
| `json` | string | `validate:"json"` |
| `regex=pattern` | string | `validate:"regex=^[a-z]+$"` |
| `contains=str` | string | `validate:"contains=@"` |
| `startswith=str` | string | `validate:"startswith=IN"` |
| `endswith=str` | string | `validate:"endswith=.com"` |
| `excludes=str` | string | `validate:"excludes=admin"` |
| `unique` | slice | `validate:"unique"` |
| `dive` | slice/map | `validate:"dive,required"` — validate each element |

### 4.2 Validation Interfaces

```
Interface: Validator
├── Validate(v any) []ValidationError
├── RegisterRule(name string, fn ValidationFunc)
├── RegisterStructValidation(fn StructValidationFunc, types ...any)
└── RegisterTagNameFunc(fn func(field reflect.StructField) string)

Interface: SelfValidator  (user types implement this)
├── Validate() []ValidationError

Struct: ValidationError
├── Field    string
├── Tag      string
├── Param    string   // e.g., "2" for min=2
├── Value    any
└── Message  string

Type: ValidationFunc = func(value any, param string) bool
Type: StructValidationFunc = func(v any) []ValidationError
```

### 4.3 Validation Error Response Format

```
HTTP 422 Unprocessable Entity
Content-Type: application/json

{
  "error": "validation_failed",
  "message": "Request validation failed",
  "details": [
    {"field": "email", "tag": "required", "message": "email is required"},
    {"field": "age", "tag": "gte", "param": "0", "value": -5, "message": "age must be >= 0"}
  ]
}
```

---

## 5. Context

### 5.1 Context Interface

```
Interface: Context
│
├── REQUEST ACCESS
│   ├── Request() *http.Request
│   ├── Response() http.ResponseWriter
│   ├── Context() context.Context
│   ├── SetContext(ctx context.Context)
│   ├── Method() string
│   ├── Path() string
│   ├── RealIP() string                       // X-Forwarded-For / X-Real-IP aware
│   ├── IsTLS() bool
│   ├── Protocol() string                     // "HTTP/1.1", "HTTP/2.0"
│   ├── Scheme() string                       // "http" or "https"
│   ├── Host() string
│   └── IsTLS() bool
│
├── PATH PARAMETERS
│   ├── Param(name string) string
│   ├── ParamInt(name string) (int, error)
│   ├── ParamInt64(name string) (int64, error)
│   └── ParamUUID(name string) (string, error)  // validates UUID format
│
├── QUERY PARAMETERS
│   ├── Query(name string) string
│   ├── QueryDefault(name string, fallback string) string
│   ├── QueryInt(name string, fallback int) int
│   ├── QueryInt64(name string, fallback int64) int64
│   ├── QueryFloat64(name string, fallback float64) float64
│   ├── QueryBool(name string, fallback bool) bool
│   ├── QuerySlice(name string) []string
│   └── QueryParams() url.Values
│
├── HEADER ACCESS
│   ├── Header(name string) string
│   ├── SetHeader(name, value string)
│   ├── AddHeader(name, value string)
│   └── HeaderValues(name string) []string
│
├── COOKIE ACCESS
│   ├── Cookie(name string) (*http.Cookie, error)
│   └── SetCookie(cookie *http.Cookie)
│
├── BODY PARSING
│   ├── Bind(dest any) error                   // auto-detect content type
│   ├── BindJSON(dest any) error               // force JSON
│   ├── BindForm(dest any) error               // form data
│   ├── BindQuery(dest any) error              // query params to struct
│   ├── BindValidate(dest any) error           // bind + validate
│   ├── Body() ([]byte, error)                 // raw body bytes (cached)
│   └── FormFile(name string) (*multipart.FileHeader, error)
│
├── RESPONSE HELPERS
│   ├── JSON(status int, v any) error
│   ├── JSONPretty(status int, v any) error
│   ├── Text(status int, text string) error
│   ├── HTML(status int, html string) error
│   ├── XML(status int, v any) error
│   ├── Blob(status int, contentType string, data []byte) error
│   ├── Stream(status int, contentType string, reader io.Reader) error
│   ├── File(filepath string) error
│   ├── Attachment(filepath, filename string) error
│   ├── Redirect(status int, url string) error
│   ├── NoContent(status int) error
│   ├── Status(code int) Context               // set status, chain
│   └── Written() bool                         // has response been sent?
│
├── REQUEST-SCOPED STORE  (like Fastify decorateRequest)
│   ├── Set(key string, value any)
│   ├── Get(key string) (any, bool)
│   ├── MustGet(key string) any                // panics if missing
│   └── GetTyped[T any](key string) (T, bool)  // generic typed getter
│
├── ERROR HELPERS
│   ├── Error(status int, message string) error
│   └── ErrorWithDetail(status int, message, detail string) error
│
└── METADATA
    ├── RequestID() string
    └── Logger() *slog.Logger                  // request-scoped logger
```

---

## 6. Middleware System

### 6.1 Middleware Interface

```
// Standard Go middleware — compatible with any net/http middleware
Type: Middleware = func(http.Handler) http.Handler

// Framework-specific middleware (has access to Context)
Type: MiddlewareFunc = func(next HandlerFunc) HandlerFunc

// Interface for named middleware (for route listing / debugging)
Interface: NamedMiddleware
├── Name() string
└── Handle(next http.Handler) http.Handler
```

### 6.2 Middleware Registration Levels

| Level | Scope | Registration |
|---|---|---|
| **Global** | All routes | `app.Use(middleware)` |
| **Group** | Routes in group | `group.Use(middleware)` |
| **Route** | Single route | `app.Get("/path", handler, WithMiddleware(mw))` |

### 6.3 Middleware Execution Order

```
Request → Global[1] → Global[2] → Group[1] → Route[1] → Handler
Response ← Global[1] ← Global[2] ← Group[1] ← Route[1] ← Handler
(onion model — first in, last out)
```

---

## 7. Lifecycle Hooks (Fastify-style)

### 7.1 Hook Phases

| Phase | When | Use Cases |
|---|---|---|
| `OnRequest` | Immediately after request received | Request ID, logging start, rate limiting |
| `PreRouting` | Before route matching | URL rewriting, tenant detection |
| `PreParsing` | Before body parsing | Content-type enforcement, decompression |
| `PreValidation` | After parsing, before validation | Data sanitization, transformation |
| `PreHandler` | After validation, before handler | Authorization, business rule checks |
| `OnResponse` | After handler, before sending | Logging end, metrics, response headers |
| `OnSend` | Just before bytes go to wire | Response compression, encryption, transformation |
| `OnError` | On any error in the chain | Error logging, error transformation, alerting |
| `OnShutdown` | Server shutdown initiated | Cleanup, drain connections |
| `OnStartup` | Server starts listening | Warmup, health registration |

### 7.2 Hook Interface

```
Type: HookFunc = func(c *Context) error

Interface: HookRegistry
├── AddHook(phase HookPhase, hook HookFunc)
├── AddHookWithPriority(phase HookPhase, priority int, hook HookFunc)
└── Hooks(phase HookPhase) []HookFunc
```

---

## 8. Plugin System

### 8.1 Plugin Interface

```
Interface: Plugin
├── Name() string
├── Register(app *PluginContext) error
└── Version() string     // optional, for dependency resolution

// Functional alternative (for simple plugins)
Type: PluginFunc = func(app *PluginContext) error
  → implements Plugin via adapter

Interface: PluginContext  (scoped view of the app)
├── Get/Post/Put/Delete/Patch/...   // scoped to plugin prefix
├── Group(prefix, fn)               // nested groups within plugin
├── Use(middleware)                  // scoped middleware
├── AddHook(phase, hook)            // scoped hooks
├── Decorate(key string, value any) // register shared services
├── Resolve(key string) (any, bool) // retrieve decorated services
├── Register(plugin Plugin)         // nested plugin registration
├── Config() any                    // plugin-specific config
└── Logger() *slog.Logger           // plugin-scoped logger
```

---

## 9. Default Plugins & Middleware

### 9.1 MUST-HAVE — Ship with Framework (as sub-packages)

| Plugin/Middleware | Package | Description |
|---|---|---|
| **Recovery** | `plugins/recover` | Panic recovery → 500 response, stack trace to logger |
| **Request ID** | `plugins/requestid` | Generates/propagates `X-Request-ID` (ULID or UUID) |
| **Logger** | `plugins/logger` | Structured request/response logging via `log/slog` |
| **CORS** | `plugins/cors` | Cross-Origin Resource Sharing headers |
| **Secure Headers** | `plugins/secure` | Security headers (XSS, HSTS, CSP, X-Frame, etc.) |
| **Body Limit** | `plugins/bodylimit` | `http.MaxBytesReader` wrapper, configurable per-route |
| **Timeout** | `plugins/timeout` | Per-request `context.WithTimeout`, configurable per-route |
| **Compress** | `plugins/compress` | Gzip/Deflate response compression via `compress/gzip` stdlib |
| **ETag** | `plugins/etag` | Automatic ETag generation + `If-None-Match` → 304 |
| **Static Files** | `plugins/static` | `http.FileServer` wrapper with index, SPA fallback, caching |
| **Health Check** | `plugins/health` | `/health`, `/ready`, `/live` endpoints for K8s probes |

### 9.2 AUTH Plugins (separate modules — bring your own secrets)

| Plugin | Package | Description |
|---|---|---|
| **JWT Auth** | `plugins/jwt` | JWT validation (`crypto/hmac`, `crypto/ecdsa`, `crypto/rsa` — all stdlib) |
| **API Key** | `plugins/apikey` | Header/query API key validation |
| **Basic Auth** | `plugins/basicauth` | HTTP Basic Authentication |
| **Bearer Token** | `plugins/bearer` | Generic Bearer token extraction + validator callback |
| **RBAC** | `plugins/rbac` | Role-based access control middleware |

### 9.3 OBSERVABILITY Plugins

| Plugin | Package | Dependency | Description |
|---|---|---|---|
| **Prometheus Metrics** | `plugins/prometheus` | `prometheus/client_golang` | Request count, latency histogram, in-flight gauge |
| **OpenTelemetry** | `plugins/otel` | `go.opentelemetry.io/otel` | Distributed tracing spans, propagation |
| **Pprof** | `plugins/pprof` | `net/http/pprof` (stdlib) | Debug profiling endpoints |

### 9.4 RATE LIMITING Plugins

| Plugin | Package | Description |
|---|---|---|
| **Rate Limiter** | `plugins/ratelimit` | Token bucket / sliding window, per-IP or per-key |
| **Throttle** | `plugins/throttle` | Max concurrent requests limiter |

### 9.5 SECURITY Plugins

| Plugin | Package | Description |
|---|---|---|
| **CSRF** | `plugins/csrf` | CSRF token generation + validation |
| **IP Filter** | `plugins/ipfilter` | Whitelist/blacklist IP ranges (CIDR support) |
| **Request Sanitizer** | `plugins/sanitize` | Strip XSS from inputs, normalize Unicode |

---

## 10. Secure Headers Plugin — Full Detail

```
Config: SecureConfig
├── XSSProtection           string   // "1; mode=block"
├── ContentTypeNosniff      string   // "nosniff"
├── XFrameOptions           string   // "SAMEORIGIN" | "DENY"
├── HSTSMaxAge              int      // 31536000 (1 year)
├── HSTSIncludeSubdomains   bool     // true
├── HSTSPreload             bool     // false
├── ContentSecurityPolicy   string   // "default-src 'self'"
├── CSPReportOnly           bool     // false
├── ReferrerPolicy          string   // "strict-origin-when-cross-origin"
├── PermissionsPolicy       string   // "camera=(), microphone=()"
├── CrossOriginEmbedder     string   // "require-corp"
├── CrossOriginOpener       string   // "same-origin"
├── CrossOriginResource     string   // "same-origin"
└── CacheControl            string   // "no-store"  (for sensitive APIs)
```

---

## 11. CORS Plugin — Full Detail

```
Config: CORSConfig
├── AllowOrigins     []string    // ["*"] or ["https://example.com"]
├── AllowMethods     []string    // ["GET", "POST", "PUT", "DELETE", "PATCH"]
├── AllowHeaders     []string    // ["Content-Type", "Authorization", "X-Request-ID"]
├── ExposeHeaders    []string    // ["X-Request-ID", "X-RateLimit-Remaining"]
├── AllowCredentials bool        // false (true conflicts with AllowOrigins=["*"])
├── MaxAge           int         // 86400 (preflight cache in seconds)
└── AllowOriginFunc  func(origin string) bool   // dynamic origin check
```

---

## 12. JWT Auth Plugin — Full Detail

```
Config: JWTConfig
├── SigningMethod     string             // "HS256", "RS256", "ES256"
├── SecretKey         []byte             // for HMAC
├── PublicKey         crypto.PublicKey    // for RSA/ECDSA
├── TokenLookup      string             // "header:Authorization", "query:token", "cookie:jwt"
├── TokenPrefix      string             // "Bearer" (stripped before parsing)
├── Claims           any                // struct type for claims deserialization
├── ContextKey       string             // key to store claims in Context
├── SkipPaths        []string           // paths that bypass auth
├── ErrorHandler     func(*Context, error) error
├── SuccessHandler   func(*Context)
├── KeyFunc          func(token *Token) (any, error)   // for JWKS / key rotation
└── Issuer           string             // validate iss claim

Interface: Token (framework's own — zero-dep, not dgrijalva/jwt-go)
├── Header    map[string]any
├── Claims    map[string]any   // or typed struct
├── Signature []byte
├── Raw       string
├── Valid     bool
└── Method    SigningMethod

Interface: SigningMethod
├── Verify(signingString, signature string, key any) error
├── Sign(signingString string, key any) (string, error)
└── Alg() string

Supported algorithms (all stdlib crypto):
├── HS256, HS384, HS512       → crypto/hmac + crypto/sha256/sha512
├── RS256, RS384, RS512       → crypto/rsa + crypto/sha256/sha512
├── ES256, ES384, ES512       → crypto/ecdsa + crypto/elliptic
└── EdDSA                     → crypto/ed25519
```

---

## 13. TLS & HTTP/2 Support

### 13.1 TLS Configuration

| Feature | Support | How |
|---|---|---|
| **HTTPS with TLS** | ✅ | `app.ListenTLS(addr, certFile, keyFile)` |
| **HTTP/2 automatic** | ✅ | Go's `net/http` auto-negotiates HTTP/2 over TLS via ALPN |
| **HTTP/2 cleartext (h2c)** | ✅ | Via `golang.org/x/net/http2/h2c` wrapper (optional plugin) |
| **TLS 1.3 default** | ✅ | `tls.Config{MinVersion: tls.VersionTLS13}` |
| **TLS 1.2 support** | ✅ | Configurable fallback for older clients |
| **Mutual TLS (mTLS)** | ✅ | `tls.Config{ClientAuth: tls.RequireAndVerifyClientCert}` |
| **Custom cipher suites** | ✅ | Exposed via `TLSConfig` option |
| **SNI (multi-cert)** | ✅ | Via `tls.Config.GetCertificate` callback |
| **Auto-reload certs** | ✅ | File watcher plugin for cert rotation |
| **Let's Encrypt / ACME** | 🔌 Plugin | Via `golang.org/x/crypto/acme/autocert` |
| **HTTP/3 (QUIC)** | 🔌 Plugin | Via `quic-go/quic-go` (experimental) |

### 13.2 TLS/Server Interfaces

```
Listen methods on App:
├── Listen(addr string) error                           // HTTP
├── ListenTLS(addr, certFile, keyFile string) error     // HTTPS + HTTP/2
├── ListenMutualTLS(addr, certFile, keyFile, clientCA string) error
├── ListenAutoTLS(addr string, domains ...string) error // ACME/Let's Encrypt
└── Shutdown(ctx context.Context) error

Config: ServerConfig (via functional options)
├── ReadTimeout          time.Duration    // 15s default
├── ReadHeaderTimeout    time.Duration    // 5s default
├── WriteTimeout         time.Duration    // 15s default
├── IdleTimeout          time.Duration    // 60s default
├── MaxHeaderBytes       int              // 1MB default
├── ShutdownTimeout      time.Duration    // 30s default
├── TLSConfig            *tls.Config      // full TLS control
├── Protocols            *http.Protocols  // HTTP/1, HTTP/2, h2c selection
├── DisableHTTP2         bool             // force HTTP/1.1 only
└── TrustedProxies       []string         // for X-Forwarded-For parsing
```

### 13.3 Recommended TLS Defaults (for financial services)

```
TLS Config Defaults:
├── MinVersion: tls.VersionTLS12    (TLS 1.3 preferred, 1.2 fallback)
├── MaxVersion: tls.VersionTLS13
├── CipherSuites (TLS 1.2 only — TLS 1.3 suites are not configurable):
│   ├── TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384
│   ├── TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384
│   ├── TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
│   └── TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
├── CurvePreferences:
│   ├── tls.X25519
│   ├── tls.CurveP256
│   └── tls.CurveP384
├── PreferServerCipherSuites: true
├── Renegotiation: tls.RenegotiateNever
└── SessionTicketsDisabled: false   (enable session resumption)
```

---

## 14. Error Handling

### 14.1 Error Types

```
Interface: AppError
├── Error() string          // implements error
├── StatusCode() int        // HTTP status
├── Message() string        // client-facing message
├── Detail() string         // optional detail
├── Internal() error        // wrapped internal error (not serialized)
└── Code() string           // machine-readable error code

Built-in error constructors:
├── ErrBadRequest(msg string) *AppError           // 400
├── ErrUnauthorized(msg string) *AppError         // 401
├── ErrForbidden(msg string) *AppError            // 403
├── ErrNotFound(msg string) *AppError             // 404
├── ErrMethodNotAllowed(msg string) *AppError     // 405
├── ErrConflict(msg string) *AppError             // 409
├── ErrUnprocessable(msg string) *AppError        // 422
├── ErrTooManyRequests(msg string) *AppError      // 429
├── ErrInternal(err error) *AppError              // 500
├── ErrBadGateway(msg string) *AppError           // 502
├── ErrServiceUnavailable(msg string) *AppError   // 503
├── ErrGatewayTimeout(msg string) *AppError       // 504
└── NewError(status int, code, msg string) *AppError  // custom

Type: ErrorHandler = func(c *Context, err error)
```

### 14.2 Error Response Format

```json
{
  "error": "not_found",
  "message": "User not found",
  "detail": "No user exists with ID abc-123",
  "request_id": "01HXYZ..."
}
```

---

## 15. App Configuration

### 15.1 Functional Options

```
App creation: New(opts ...Option) *App

Options:
├── WithCodec(codec Codec)                        // JSON library
├── WithLogger(logger *slog.Logger)                // structured logger
├── WithErrorHandler(fn ErrorHandler)              // global error handler
├── WithValidator(v Validator)                     // custom validator
├── WithReadTimeout(d time.Duration)
├── WithWriteTimeout(d time.Duration)
├── WithIdleTimeout(d time.Duration)
├── WithReadHeaderTimeout(d time.Duration)
├── WithShutdownTimeout(d time.Duration)
├── WithMaxHeaderBytes(n int)
├── WithMaxBodySize(n int64)                       // global body limit
├── WithTLSConfig(cfg *tls.Config)
├── WithTrustedProxies(cidrs ...string)
├── WithRedirectTrailingSlash(enabled bool)
├── WithDisableHTTP2(disabled bool)
├── WithDebug(enabled bool)                        // verbose logging
└── WithBanner(enabled bool)                       // startup banner
```

---

## 16. Testing Utilities

### 16.1 Testing Interface

```
Interface: TestClient
├── Get(path string) *TestResponse
├── Post(path string, body any) *TestResponse
├── Put(path string, body any) *TestResponse
├── Delete(path string) *TestResponse
├── Patch(path string, body any) *TestResponse
├── WithHeader(key, value string) TestClient
├── WithCookie(cookie *http.Cookie) TestClient
├── WithQuery(key, value string) TestClient
├── WithBearer(token string) TestClient
└── Do(req *http.Request) *TestResponse

Struct: TestResponse
├── Status      int
├── Headers     http.Header
├── Body        []byte
├── JSON(dest any) error
├── Text() string
└── AssertStatus(t *testing.T, expected int)

Constructor:
├── NewTestClient(app *App) TestClient

Uses: httptest.NewRecorder + httptest.NewRequest (zero network, pure stdlib)
```

---

## 17. Graceful Shutdown

```
Shutdown flow:
1. Signal received (SIGINT / SIGTERM)
2. OnShutdown hooks fire
3. Server stops accepting new connections
4. In-flight requests drain (up to ShutdownTimeout)
5. Background goroutines complete (via errgroup or WaitGroup)
6. Close listeners
7. Return

Interface: ShutdownHook = func(ctx context.Context) error
Registration: app.OnShutdown(hook ShutdownHook)
```

---

## 18. Project Structure (Sub-Packages)

```
framework/
├── go.mod                       ← ZERO external dependencies
├── aarv.go                      // App struct, builder, server lifecycle
├── context.go                   // Context struct, pooling
├── router.go                    // Router, RouteGroup, ServeMux wrapping
├── handler.go                   // Handler adapter (signature conversion)
├── bind.go                      // Bind[Req,Res], BindReq, BindRes generics
├── binder.go                    // Multi-source binder (param/query/header/body)
├── validator.go                 // Struct tag validation engine
├── codec.go                     // Codec interface + stdlib JSON default
├── errors.go                    // AppError, error constructors
├── middleware.go                // Middleware chain, composition
├── hooks.go                     // Lifecycle hook registry
├── plugin.go                    // Plugin interface, PluginContext
├── options.go                   // Functional options
├── pool.go                      // sync.Pool for Context, buffers
├── response_writer.go           // Buffered response writer (for hooks)
├── test.go                      // TestClient, TestResponse
│
├── codec/                       ← separate go modules
│   ├── segmentio/go.mod         // imports segmentio/encoding
│   ├── sonic/go.mod             // imports bytedance/sonic
│   └── jsonv2/go.mod            // imports go-json-experiment/json
│
├── plugins/                     ← each is in-tree, zero external deps
│   ├── recover/                 // panic recovery
│   ├── requestid/               // X-Request-ID generation
│   ├── logger/                  // slog request logger
│   ├── cors/                    // CORS headers
│   ├── secure/                  // security headers
│   ├── bodylimit/               // request body size limit
│   ├── timeout/                 // request timeout
│   ├── compress/                // gzip/deflate
│   ├── etag/                    // ETag + conditional requests
│   ├── static/                  // file serving
│   ├── health/                  // health/ready/live probes
│   ├── jwt/                     // JWT auth (stdlib crypto only)
│   ├── apikey/                  // API key auth
│   ├── basicauth/               // HTTP Basic auth
│   ├── bearer/                  // Bearer token
│   ├── rbac/                    // role-based access control
│   ├── ratelimit/               // rate limiting
│   ├── throttle/                // concurrency limiter
│   ├── csrf/                    // CSRF protection
│   ├── ipfilter/                // IP whitelist/blacklist
│   ├── sanitize/                // input sanitization
│   └── pprof/                   // debug profiling
│
├── plugins/                     ← separate go modules (external deps)
│   ├── prometheus/go.mod        // imports prometheus client
│   ├── otel/go.mod              // imports opentelemetry
│   └── autocert/go.mod          // imports x/crypto/acme/autocert
│
└── examples/
    ├── hello/
    ├── rest-crud/
    ├── jwt-auth/
    ├── file-upload/
    ├── middleware-chain/
    ├── plugin-custom/
    └── tls-http2/
```

---

## 19. Stdlib-Only Technologies Used

| Need | Stdlib Package | Notes |
|---|---|---|
| HTTP server | `net/http` | ServeMux (1.22+), Server, TLS |
| JSON (default) | `encoding/json` | Zero-dep baseline codec |
| TLS / HTTPS | `crypto/tls` | TLS 1.2/1.3 |
| HTTP/2 | `net/http` (auto) | Negotiated via ALPN over TLS |
| JWT HMAC | `crypto/hmac`, `crypto/sha256` | HS256/384/512 |
| JWT RSA | `crypto/rsa`, `crypto/sha256` | RS256/384/512 |
| JWT ECDSA | `crypto/ecdsa`, `crypto/elliptic` | ES256/384/512 |
| JWT EdDSA | `crypto/ed25519` | Ed25519 |
| Base64 (JWT) | `encoding/base64` | URL-safe base64 |
| Gzip compression | `compress/gzip`, `compress/flate` | Response compression |
| UUID generation | `crypto/rand`, `encoding/hex` | For request IDs |
| Regex (validation) | `regexp` | For `regex=` validation tag |
| Time parsing | `time` | For `datetime=` validation |
| IP parsing | `net` | For `ip`, `cidr` validation |
| URL parsing | `net/url` | Query params, URL validation |
| Structured logging | `log/slog` | Go 1.21+ |
| Testing | `net/http/httptest` | TestClient internals |
| Profiling | `net/http/pprof` | Debug endpoints |
| Context | `context` | Request cancellation, timeouts |
| Sync | `sync` | Pools, mutexes |
| Unsafe | `unsafe` | Fast field access in validator |
| Reflect | `reflect` | Registration-time only (never hot path) |
| OS signals | `os/signal`, `syscall` | Graceful shutdown |

---

## 20. External Dependencies Map

| What | When | Package |
|---|---|---|
| No external deps | Framework core + all built-in plugins | — |
| Fast JSON | User opts in | `github.com/segmentio/encoding` |
| Fastest JSON | User opts in (amd64/arm64) | `github.com/bytedance/sonic` |
| Future JSON | When stable | `encoding/json/v2` (stdlib) |
| Prometheus metrics | User opts in | `github.com/prometheus/client_golang` |
| OpenTelemetry | User opts in | `go.opentelemetry.io/otel` |
| Let's Encrypt | User opts in | `golang.org/x/crypto/acme/autocert` |
| HTTP/3 / QUIC | User opts in (experimental) | `github.com/quic-go/quic-go` |

---

## 21. Implementation Priority

| Phase | What | Effort |
|---|---|---|
| **Phase 1** | App, Context (pooled), Router (ServeMux wrapper), Handler adapters, Codec interface, `Listen` + graceful shutdown, stdlib JSON default | 1-2 weeks |
| **Phase 2** | `Bind[Req,Res]` generics, multi-source binder (param/query/header/body), struct tag parser | 1 week |
| **Phase 3** | Validation engine (struct tags, pre-computed rules, `unsafe.Pointer` field access) | 1 week |
| **Phase 4** | Middleware chain, Route groups, Lifecycle hooks | 1 week |
| **Phase 5** | Core plugins: Recovery, RequestID, Logger, CORS, Secure Headers, BodyLimit, Timeout, Compress | 1-2 weeks |
| **Phase 6** | JWT plugin (stdlib crypto), API Key, Basic Auth, Bearer, RBAC | 1 week |
| **Phase 7** | Plugin system (scoped registration, decoration, nested plugins) | 1 week |
| **Phase 8** | Codec sub-packages: segmentio, sonic, jsonv2 | 2-3 days |
| **Phase 9** | Testing utilities, ETag, Static files, Health checks | 1 week |
| **Phase 10** | Rate limiting, CSRF, IP filter, Throttle, Sanitize | 1 week |
| **Phase 11** | Observability: Prometheus, OpenTelemetry, Pprof | 1 week |
| **Phase 12** | TLS helpers: AutoTLS/ACME, mTLS, cert reload | 3-5 days |
| **Phase 13** | Documentation, examples, benchmarks | ongoing |
