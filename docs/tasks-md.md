# tasks.md — Detailed Task Breakdown

> Track progress by checking boxes. Each task is atomic and testable.

---

## Phase 1: Foundation (M1) ✅ COMPLETE

### 1.1 Project Scaffolding ✅
- [x] Initialize Go module: `go mod init github.com/nilshah80/aarv`
- [x] Set `go 1.26.0` in `go.mod`
- [x] Create directory structure (see spec for layout)
- [x] Add `LICENSE` (MIT)
- [ ] Add `.gitignore`, `Makefile`
- [ ] Add `golangci-lint` config (`.golangci.yml`)
- [ ] Set up GitHub Actions CI: lint, test, race detector, coverage

### 1.2 Codec Interface ✅
- [x] Define `Codec` interface: `Encode`, `Decode`, `MarshalBytes`, `UnmarshalBytes`, `ContentType`
- [x] Implement `StdJSONCodec` (wraps `encoding/json`)
- [x] Implement `OptimizedJSONCodec` with `sync.Pool` buffering
- [x] Unit tests: encode/decode round-trip, nil handling, error cases
- [x] Benchmark: `Codec.Encode` vs raw `json.NewEncoder`

### 1.3 App Struct & Options ✅
- [x] Define `App` struct: mux, server, middlewares, hooks, codec, errorHandler, logger
- [x] Implement functional `Option` type
- [x] Implement options: `WithCodec`, `WithLogger`, `WithErrorHandler`
- [x] Implement timeout options: `WithReadTimeout`, `WithWriteTimeout`, `WithIdleTimeout`, `WithReadHeaderTimeout`
- [x] Implement `WithMaxHeaderBytes`, `WithMaxBodySize`, `WithTrustedProxies`
- [x] Implement `WithTLSConfig`, `WithDisableHTTP2`
- [x] Implement `New(opts ...Option) *App` constructor
- [x] Unit tests: each option applies correctly

### 1.4 Context ✅
- [x] Define `Context` struct: req, res, store, statusCode, written, app ref
- [x] Implement `sync.Pool` for Context recycling
- [x] Implement `acquireContext(w, r)` / `releaseContext(c)` with proper reset
- [x] Implement path params: `Param(name)`, `ParamInt(name)`, `ParamInt64(name)`, `ParamUUID(name)`
- [x] Implement query helpers: `Query`, `QueryDefault`, `QueryInt`, `QueryInt64`, `QueryFloat64`, `QueryBool`, `QuerySlice`, `QueryParams`
- [x] Implement header access: `Header`, `SetHeader`, `AddHeader`, `HeaderValues`
- [x] Implement cookie access: `Cookie`, `SetCookie`
- [x] Implement request-scoped store: `Set`, `Get`, `MustGet`
- [x] Implement request metadata: `Method`, `Path`, `RealIP`, `IsTLS`, `Protocol`, `Scheme`, `Host`
- [x] Implement body reader: `Body()` with caching (read once, return cached bytes)
- [x] Implement `BindJSON(dest)` using Codec interface
- [x] Implement response helpers: `JSON`, `JSONPretty`, `Text`, `HTML`, `NoContent`, `Redirect`
- [x] Implement response helpers: `Blob`, `Stream`, `File`, `Attachment`
- [x] Implement `Status(code)` chaining
- [x] Implement `Written()` guard (prevent double-write)
- [x] Implement `RequestID()` getter (reads from store)
- [x] Implement `Logger()` getter (request-scoped slog with request_id)
- [x] Implement `Error(status, msg)` and `ErrorWithDetail(status, msg, detail)`
- [x] Unit tests: every Context method, pool acquire/release, double-write guard
- [x] Benchmark: Context pool vs alloc-per-request

### 1.5 Buffered Response Writer ✅
- [x] Implement `bufferedResponseWriter` wrapping `http.ResponseWriter`
- [x] Buffer response body until explicit flush or handler return
- [x] Allow `OnSend` hooks to inspect/modify buffered body
- [x] Set `Content-Length` header from buffer length
- [x] Implement `Hijack()`, `Flush()`, `Push()` interface passthrough
- [x] Opt-out: `c.Stream()` bypasses buffer for streaming responses
- [ ] Unit tests: buffering, Content-Length, hijack passthrough

### 1.6 Router ✅
- [x] Create internal `router` struct wrapping `*http.ServeMux`
- [x] Implement `addRoute(method, pattern, handler)` — formats pattern as `"METHOD /pattern"`
- [x] Implement fluent methods: `Get`, `Post`, `Put`, `Delete`, `Patch`, `Head`, `Options`, `Any`
- [x] Implement `Any` — registers handler for all common methods
- [x] Implement `Mount(prefix, http.Handler)` for sub-app mounting
- [x] Implement `Routes() []RouteInfo` — returns all registered routes
- [x] Implement custom 404 handler: `routingMux` wrapper intercepts unmatched routes after middleware runs
- [x] Implement custom 405 handler: detect method mismatch and serve custom response
- [x] Implement trailing slash redirect (configurable)
- [x] Unit tests: route matching, path params, wildcards, method filtering
- [ ] Unit tests: custom 404/405 handler invocation
- [x] Integration test: register 50+ routes, verify no conflicts

### 1.7 Route Groups ✅
- [x] Define `RouteGroup` struct: prefix, parent app/group, scoped middleware
- [x] Implement `Group(prefix, fn func(RouteGroup))` on App
- [x] Implement nested groups: `Group` on `RouteGroup`
- [x] Internal: nested `http.ServeMux` + `http.StripPrefix` composition
- [x] Scoped middleware: group middleware only applies to group routes
- [x] Implement `RouteOption`: `WithName`, `WithTags`, `WithDescription`, `WithRouteMiddleware`, `WithRouteMaxBodySize`
- [ ] Unit tests: group prefix, nested groups, scoped middleware isolation

### 1.8 Handler Adapters ✅
- [x] Define internal `HandlerFunc = func(*Context) error`
- [x] Adapter: `func(*Context) error` → direct use
- [x] Adapter: `func(http.ResponseWriter, *http.Request)` → `Adapt()` wrapper
- [x] Adapter: `http.Handler` → wrap via `ServeHTTP`
- [x] Registration-time signature detection (use type switch, not runtime reflect)
- [x] Unit tests: each adapter fires correctly, error propagation

### 1.9 Error Handling ✅
- [x] Define `AppError` struct: Code, Message, Detail, Internal, ErrorCode
- [x] Implement `Error()` for `error` interface
- [x] Implement constructors: `ErrBadRequest`, `ErrNotFound`, `ErrUnauthorized`, `ErrForbidden`, `ErrConflict`, `ErrUnprocessable`, `ErrTooManyRequests`, `ErrInternal`, `ErrBadGateway`, `ErrServiceUnavailable`, `ErrGatewayTimeout`
- [x] Implement `NewError(status, code, msg)`
- [x] Define `ErrorHandler = func(*Context, error)`
- [x] Implement `DefaultErrorHandler`: checks `*AppError`, logs internal, returns JSON
- [x] Include `request_id` in error responses
- [x] Unit tests: each error constructor, default handler behavior

### 1.10 Server Lifecycle ✅
- [x] Implement `Listen(addr)` — create `http.Server`, start listening
- [x] Implement `ListenTLS(addr, cert, key)` — HTTPS + auto HTTP/2
- [x] Implement `ListenMutualTLS(addr, cert, key, clientCA)` — mTLS
- [x] Implement graceful shutdown: signal capture (SIGINT, SIGTERM), drain, timeout
- [x] Implement `Shutdown(ctx)` for programmatic shutdown
- [x] Set sensible TLS defaults (TLS 1.2 min, TLS 1.3 preferred, strong ciphers)
- [x] Implement `OnStartup` / `OnShutdown` hook invocation
- [x] Print startup banner with address, protocol, Go version
- [x] Unit tests: startup, shutdown, timeout
- [x] Integration test: start server, send request, signal shutdown, verify drain

### 1.11 Framework ServeHTTP Glue ✅
- [x] Implement `App.ServeHTTP(w, r)` — the main entry point
- [x] Acquire Context from pool
- [x] Run middleware chain
- [x] Route to handler via ServeMux
- [x] Catch errors from handler, pass to ErrorHandler
- [x] Flush buffered response
- [x] Release Context to pool
- [x] Integration test: end-to-end request flow

---

## Phase 2: Type-Safe Binding (M2) ✅ COMPLETE

### 2.1 Generic Wrappers ✅
- [x] Implement `Bind[Req, Res](fn func(*Context, Req) (Res, error)) HandlerFunc`
- [x] Implement `BindReq[Req](fn func(*Context, Req) error) HandlerFunc`
- [x] Implement `BindRes[Res](fn func(*Context) (Res, error)) HandlerFunc`
- [x] Body decode via Codec into concrete `*Req` (no interface{})
- [x] Auto-serialize `Res` via Codec on success (200 OK default)
- [x] Error from handler → pass to ErrorHandler
- [x] Unit tests: each wrapper variant, nil body, invalid JSON, handler error

### 2.2 Multi-Source Binder ✅
- [x] Implement `buildBinder[T]()` — registration-time struct inspection
- [x] Parse struct tags: `param`, `query`, `header`, `cookie`, `form`, `default`
- [x] Build field map: `[]fieldBinding{source, name, fieldIndex, kind, defaultValue}`
- [x] Implement `Binder.Bind(c *Context, dest *T) error` — runtime binding
- [x] Path param binding: `r.PathValue()` → field
- [x] Query param binding: `r.URL.Query().Get()` → field with coercion
- [x] Header binding: `r.Header.Get()` → field
- [x] Cookie binding: `r.Cookie()` → field
- [x] Body binding: `Codec.Decode(r.Body, dest)` for `json` tagged fields
- [x] Form binding: `r.FormValue()` → field
- [x] Default value application: apply defaults for zero-value fields
- [x] Type coercion: `string` → `int`, `int64`, `uint`, `uint64`, `float64`, `bool`
- [x] UUID validation for `ParamUUID`
- [x] `CustomBinder` interface: if type implements `BindFromContext(*Context) error`, call it
- [x] `ParamParser` interface: if field type implements `ParseParam(string) error`, call it
- [x] Unit tests: each source, coercion, defaults, custom binders, mixed sources
- [x] Benchmark: multi-source binding vs manual `c.Param()` + `c.Query()` calls

### 2.3 Integration ✅
- [x] Wire `Bind[T]` to call binder before handler
- [x] Wire `Bind[T]` to call validator after binding (if validation enabled)
- [x] Integration test: POST with JSON body + path params + query params → all bound correctly

---

## Phase 3: Validation Engine (M3) ✅ COMPLETE

### 3.1 Tag Parser ✅
- [x] Parse `validate:"required,min=2,max=100,oneof=a b c"` tag format
- [x] Handle nested rules: `validate:"dive,required,min=1"`
- [x] Handle multiple struct fields
- [x] Cache parsed rules per type (sync.Map keyed by reflect.Type)

### 3.2 Built-in Rules ✅
- [x] `required` — non-zero value check
- [x] `min=N` / `max=N` — string length, numeric value, slice length
- [x] `gte=N` / `lte=N` / `gt=N` / `lt=N` — numeric comparisons
- [x] `len=N` — exact length
- [x] `oneof=a b c` — set membership
- [x] `email` — regex-based email validation
- [x] `url` — `net/url.Parse` based
- [x] `uuid` — UUID v4 format regex
- [x] `alpha` / `numeric` / `alphanum` — character class checks
- [x] `ip` / `ipv4` / `ipv6` — `net.ParseIP` based
- [x] `cidr` — `net.ParseCIDR` based
- [x] `json` — `json.Valid()` check
- [x] `datetime=layout` — `time.Parse(layout, value)` based
- [x] `regex=pattern` — `regexp.MatchString` (compiled + cached at registration)
- [x] `contains=str` / `startswith=str` / `endswith=str` / `excludes=str`
- [x] `unique` — slice uniqueness check
- [x] `dive` — validate each element of slice/map
- [ ] Unit tests: every rule, edge cases (empty string, nil pointer, zero value)

### 3.3 Validator Core ✅
- [x] Implement `buildStructValidator()` — registration-time rule compilation
- [x] Pre-compute field indices via `reflect.StructField.Index`
- [x] Build `[]fieldValidator{index, kind, rules}` slice
- [x] Implement `validate(ptr any) []ValidationError` using `reflect.FieldByIndex`
- [x] Custom rule registration: `RegisterRule(name, func(field reflect.Value, param string) bool)`
- [x] `SelfValidator` interface: if type implements `Validate() []ValidationError`, call it
- [x] `StructLevelValidator` interface for struct-level validation
- [x] `RegisterStructValidation(type, fn)` for external struct-level validators
- [ ] Implement `unsafe.Pointer` + offset fast path (currently uses reflect)
- [ ] Unit tests: validator performance, custom rules, self-validator, nested structs
- [ ] Benchmark: reflect validator vs go-playground/validator

### 3.4 Error Formatting ✅
- [x] Implement `ValidationError` struct: Field, Tag, Param, Value, Message
- [x] Auto-generate human-readable messages per rule (hardcoded in `formatMessage`)
- [ ] Configurable message templates
- [x] JSON serialization as 422 response
- [x] Integration with error handler

---

## Phase 4: Middleware & Hooks (M4) ✅ COMPLETE

### 4.1 Middleware Chain ✅
- [x] Implement middleware chain builder: `[]Middleware` → single `http.Handler`
- [x] Pre-build chain at startup (not per request)
- [x] Support `func(http.Handler) http.Handler` (stdlib compatible)
- [x] Support `func(next HandlerFunc) HandlerFunc` (framework specific)
- [x] Adapter: convert between the two middleware types
- [x] Named middleware interface for debug/route listing
- [x] Unit tests: chain order (onion model), early return, error propagation

### 4.2 Hook System ✅
- [x] Define `HookPhase` enum: OnRequest, PreRouting, PreParsing, PreValidation, PreHandler, OnResponse, OnSend, OnError, OnStartup, OnShutdown
- [x] Implement `HookRegistry`: store hooks per phase with priority
- [x] Implement `AddHook(phase, hook)` and `AddHookWithPriority(phase, priority, hook)`
- [x] Implement hook execution: run all hooks for a phase in priority order
- [x] Hook error handling: OnRequest errors trigger error handler; OnResponse/OnSend errors ignored
- [x] Wire hooks into request lifecycle: OnRequest, OnResponse, OnSend, OnError, OnStartup, OnShutdown
- [ ] Hook error handling: full error propagation to OnError phase for all hooks
- [ ] Wire PreRouting, PreParsing, PreValidation, PreHandler phases (defined but not invoked)
- [ ] Unit tests: hook ordering, error short-circuit, all phases fire correctly

### 4.3 Route Group Middleware ✅
- [x] Wire group-level `Use()` to apply middleware only to group routes
- [x] Verify isolation: group middleware doesn't leak to sibling groups
- [x] Route-level middleware via `WithRouteMiddleware` option
- [ ] Integration test: global + group + route middleware ordering

---

## Phase 5: Core Plugins (M5) ✅ COMPLETE

### 5.1 Recovery Plugin ✅
- [x] Catch panics via `defer recover()`
- [x] Log stack trace to `slog.Error`
- [x] Return 500 with generic error response
- [x] Configurable: stack trace depth, disable stack logging
- [ ] Configurable: custom panic handler, include stack in response (debug mode)
- [ ] Unit tests: panic in handler, panic in middleware, nested panic

### 5.2 Request ID Plugin ✅
- [x] Generate ULID (monotonic, sortable) using `crypto/rand` + `time`
- [x] Read existing `X-Request-ID` from request header (propagate)
- [x] Set `X-Request-ID` on response header
- [x] Store in Context via `c.Set("requestId", id)`
- [x] Configurable: header name, generator function
- [ ] Configurable: prefix for generated IDs
- [ ] Unit tests: generation, propagation, custom generator

### 5.3 Logger Plugin ✅
- [x] Log on request start + completion using `log/slog`
- [x] Fields: method, path, status, latency, request_id, client_ip, user_agent, bytes_out
- [x] Skip paths: configurable (e.g., skip /health)
- [x] Log level: configurable (Info default, Debug for verbose)
- [ ] Unit tests: log output format, skip paths, latency measurement

### 5.4 CORS Plugin ✅
- [x] Handle preflight `OPTIONS` requests
- [x] Set `Access-Control-Allow-Origin` (static list or dynamic function)
- [x] Set `Access-Control-Allow-Methods`
- [x] Set `Access-Control-Allow-Headers`
- [x] Set `Access-Control-Expose-Headers`
- [x] Set `Access-Control-Allow-Credentials`
- [x] Set `Access-Control-Max-Age` (preflight cache)
- [x] Configurable: `AllowOriginFunc` for dynamic origin checking
- [x] Security: reject wildcard origin with credentials
- [ ] Unit tests: preflight, simple request, credentials, dynamic origin

### 5.5 Secure Headers Plugin ✅
- [x] Set all headers from `SecureConfig` struct
- [x] Defaults: XSS protection, nosniff, DENY, HSTS 1yr, strict referrer, CSP, Permissions-Policy
- [x] Configurable per-header
- [ ] Unit tests: every header set correctly, custom overrides

### 5.6 Body Limit Plugin ✅
- [x] Wrap `r.Body` with `http.MaxBytesReader`
- [x] Configurable max bytes
- [x] Return 413 Payload Too Large on exceed (via `NewWithResponse`)
- [ ] Unit tests: under limit, at limit, over limit

### 5.7 Timeout Plugin ✅
- [x] Wrap handler with `context.WithTimeout`
- [x] Configurable timeout duration
- [x] Return 504 Gateway Timeout on exceed
- [x] Ensure context cancellation propagates
- [ ] Unit tests: fast handler, slow handler, exact timeout

### 5.8 Compress Plugin ✅
- [x] Check `Accept-Encoding` header for gzip/deflate support
- [x] Wrap ResponseWriter with `gzip.Writer` (from `compress/gzip`)
- [x] Set `Content-Encoding: gzip` header
- [x] Set `Vary: Accept-Encoding` header
- [x] Skip compression for small responses (configurable threshold)
- [x] Skip compression for already-compressed content types (images, video)
- [x] Configurable: compression level, min size, excluded content types, prefer gzip
- [x] Pool gzip/deflate writers via `sync.Pool`
- [ ] Unit tests: compression, skip small, skip images, Content-Encoding header

### 5.9 ETag Plugin ✅
- [x] Compute ETag from response body hash (CRC32)
- [x] Set `ETag` header
- [x] Check `If-None-Match` request header → return 304 Not Modified
- [x] Weak vs strong ETag support
- [x] Configurable: weak mode
- [ ] Configurable: hash function selection
- [ ] Unit tests: ETag generation, 304 response, weak ETag

### 5.10 Static Files Plugin ✅
- [x] Wrap `http.FileServer` with configurable root
- [x] Index file support (`index.html`)
- [x] SPA fallback (serve index.html for unmatched paths)
- [x] Cache-Control headers (configurable max-age)
- [x] Directory listing toggle (default: disabled)
- [x] Configurable: root dir, index file, prefix strip, browse
- [ ] Unit tests: serve file, index, 404, SPA fallback, cache headers

### 5.11 Health Check Plugin ✅
- [x] Register `/health` → returns `{"status": "ok"}` with 200
- [x] Register `/ready` → calls readiness callback, returns 200 or 503
- [x] Register `/live` → calls liveness callback, returns 200 or 503
- [x] Configurable: paths, callbacks
- [ ] Configurable: additional info in response
- [ ] Unit tests: healthy, unhealthy, custom callbacks

---

## Phase 6: Auth Plugins (M6)

### 6.1 JWT Plugin
- [ ] Implement JWT parser (split header.payload.signature, base64url decode)
- [ ] Implement HMAC signing/verification (HS256, HS384, HS512) via `crypto/hmac`
- [ ] Implement RSA signing/verification (RS256, RS384, RS512) via `crypto/rsa`
- [ ] Implement ECDSA signing/verification (ES256, ES384, ES512) via `crypto/ecdsa`
- [ ] Implement EdDSA signing/verification via `crypto/ed25519`
- [ ] Token lookup: header (`Authorization: Bearer`), query param, cookie
- [ ] Claims validation: `exp`, `nbf`, `iat`, `iss`, `aud`
- [ ] Store claims in Context via configurable key
- [ ] Skip paths configuration
- [ ] `KeyFunc` callback for JWKS / key rotation support
- [ ] Error handler for auth failures (401/403)
- [ ] Token creation helper: `SignToken(claims, key)` → string
- [ ] Unit tests: each algorithm, expired token, invalid signature, missing token, skip paths
- [ ] Security tests: algorithm confusion attack, none algorithm rejection

### 6.2 API Key Plugin
- [ ] Lookup from header (configurable name) or query param
- [ ] Validator callback: `func(key string) (bool, error)`
- [ ] Configurable: header name, query param name, error message
- [ ] Unit tests: valid key, invalid key, missing key

### 6.3 Basic Auth Plugin
- [ ] Parse `Authorization: Basic base64(user:pass)` header
- [ ] Validator callback: `func(user, pass string) (bool, error)`
- [ ] Realm configuration
- [ ] Unit tests: valid credentials, invalid, missing header

### 6.4 Bearer Token Plugin
- [ ] Extract `Authorization: Bearer <token>` from header
- [ ] Validator callback: `func(token string) (any, error)` — returns claims/user
- [ ] Store result in Context
- [ ] Unit tests: valid token, invalid, missing

### 6.5 RBAC Plugin
- [ ] Define `RoleExtractor = func(*Context) []string`
- [ ] Middleware: `RequireRoles("admin", "editor")` → check extracted roles
- [ ] Middleware: `RequireAnyRole("admin", "editor")` → OR check
- [ ] Return 403 Forbidden on mismatch
- [ ] Unit tests: has role, missing role, multiple roles

---

## Phase 7: Plugin System (M7) ✅ COMPLETE

- [x] Define `Plugin` interface: `Name()`, `Register(*PluginContext)`, `Version()`
- [x] Define `PluginFunc` adapter for functional plugins
- [x] Implement `PluginContext`: scoped routes, hooks, middleware, decoration
- [x] Route registration on PluginContext scoped to prefix
- [x] `Decorate(key, value)` / `Resolve(key)` for shared services
- [x] Nested `Register(plugin)` within PluginContext
- [x] Plugin-scoped logger (includes plugin name)
- [x] Plugin config passing via `PluginOption`
- [ ] Integration test: plugin registers routes + hooks + middleware in isolation
- [ ] Integration test: nested plugins, decorator resolution

---

## Phase 8: Codec Sub-Packages (M8) ✅ COMPLETE

- [x] Create `codec/segmentio/go.mod` — separate module
- [x] Implement `segmentio.New()` returning `Codec`
- [x] Create `codec/sonic/go.mod` — separate module
- [x] Implement `sonic.New()` and `sonic.NewFastest()` returning `Codec`
- [x] Implement `sonic.Pretouch(types ...any)` for JIT warmup
- [x] Create `codec/jsonv2/go.mod` — separate module
- [x] Implement `jsonv2.New()` returning `Codec`
- [ ] Benchmark suite: all codecs, marshal/unmarshal, various payload sizes
- [ ] README for each sub-package with usage example

---

## Phase 9: Testing Utilities (M9) ✅ COMPLETE

- [x] Implement `TestClient` struct
- [x] Methods: `Get`, `Post`, `Put`, `Delete`, `Patch`
- [x] Fluent: `WithHeader`, `WithCookie`, `WithQuery`, `WithBearer`
- [x] `Do(req)` for custom requests
- [x] Uses `httptest.NewRecorder` + `httptest.NewRequest` internally
- [x] Implement `TestResponse` struct: Status, Headers, Body
- [x] `JSON(dest)` — unmarshal body
- [x] `Text()` — body as string
- [x] `AssertStatus(t, expected)` — test helper
- [x] Unit tests for TestClient itself

---

## Phase 10: Security Plugins (M10)

- [ ] Rate Limiter: token bucket algorithm, zero-dep, `sync.Mutex` based
- [ ] Rate Limiter: sliding window variant
- [ ] Rate Limiter: per-IP keying via `c.RealIP()`
- [ ] Rate Limiter: custom key function
- [ ] Rate Limiter: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` headers
- [ ] Throttle: max concurrent in-flight requests via `chan struct{}`
- [ ] CSRF: double-submit cookie pattern
- [ ] CSRF: token generation via `crypto/rand`
- [ ] CSRF: `X-CSRF-Token` header validation
- [ ] CSRF: skip safe methods (GET, HEAD, OPTIONS)
- [ ] IP Filter: whitelist mode — only allow listed CIDRs
- [ ] IP Filter: blacklist mode — block listed CIDRs
- [ ] IP Filter: `net.ParseCIDR` based matching
- [ ] Sanitizer: strip HTML tags from string fields
- [ ] Sanitizer: normalize Unicode (NFC)
- [ ] Unit tests for every plugin

---

## Phase 11: Observability Plugins (M11)

- [ ] Create `plugins/prometheus/go.mod` — separate module
- [ ] Prometheus: request counter (method, path, status)
- [ ] Prometheus: request duration histogram
- [ ] Prometheus: in-flight requests gauge
- [ ] Prometheus: response size histogram
- [ ] Prometheus: configurable buckets, path grouping
- [ ] Create `plugins/otel/go.mod` — separate module
- [ ] OpenTelemetry: span creation per request
- [ ] OpenTelemetry: W3C Trace Context propagation
- [ ] OpenTelemetry: attribute injection (method, path, status)
- [ ] OpenTelemetry: configurable span naming
- [ ] Pprof: mount `net/http/pprof` at configurable prefix (stdlib, no external deps)

---

## Phase 12: TLS Helpers (M12)

- [ ] Create `plugins/autocert/go.mod` — separate module
- [ ] Implement `ListenAutoTLS` via `autocert.Manager`
- [ ] HTTP→HTTPS redirect handler
- [ ] Certificate cache directory configuration
- [ ] Implement cert file watcher for hot-reload (fsnotify-free, `os.Stat` polling)
- [ ] h2c plugin for HTTP/2 cleartext (internal mesh)
- [ ] Document recommended TLS config for financial services

---

## Phase 13: Documentation & Benchmarks (M13) — PARTIALLY COMPLETE

### Docs
- [x] README.md: badges, install, quick start, feature list
- [ ] docs/getting-started.md
- [ ] docs/routing.md
- [ ] docs/binding.md
- [ ] docs/validation.md
- [ ] docs/middleware.md
- [ ] docs/hooks.md
- [ ] docs/plugins.md (using + writing)
- [ ] docs/error-handling.md
- [ ] docs/tls-http2.md
- [ ] docs/testing.md
- [ ] docs/auth.md
- [ ] docs/migration-from-gin.md
- [ ] docs/migration-from-echo.md
- [ ] docs/architecture.md

### Benchmarks ✅
- [x] Framework overhead: empty handler, measure latency + allocs
- [ ] JSON codec comparison: stdlib vs segmentio vs sonic vs jsonv2
- [x] Middleware chain: 0, 1, 5, 10 middlewares overhead
- [x] Binding: manual vs `Bind[T]` overhead
- [x] Validation: framework validator vs go-playground/validator
- [x] Comparison: framework vs Gin vs Mach vs Fiber (bench/compare_test.go)
- [x] Load test: 500K requests, 100 VCs, real TCP with latency/memory/CPU metrics

### Examples — PARTIALLY COMPLETE
- [x] examples/hello — minimal hello world
- [x] examples/rest-crud — full CRUD with typed handlers
- [ ] examples/jwt-auth — JWT protected API
- [ ] examples/file-upload — multipart form handling
- [ ] examples/middleware-chain — custom middleware
- [ ] examples/plugin-custom — writing a plugin
- [ ] examples/tls-http2 — HTTPS setup
- [ ] examples/microservice — health check + prometheus + structured logging

---

## Cross-Cutting Tasks

### Testing
- [ ] 90%+ code coverage target
- [ ] Race detector enabled in CI: `go test -race ./...`
- [ ] Fuzz tests for: JSON binding, validation tag parsing, URL parsing
- [x] Integration test suite: full request lifecycle

### Performance ✅
- [x] Zero-allocation hot path audit (use `go test -benchmem`)
- [x] `sync.Pool` for: Context, buffered writer, gzip writer, validation error slices
- [ ] Escape analysis audit: ensure Req struct stays on stack in `Bind[T]`
- [x] Pre-build middleware chain at startup (no per-request chain assembly)
- [x] Pre-compute binder + validator at registration time (no per-request reflect)

### Security
- [ ] JWT: reject `alg: none`
- [ ] JWT: validate `alg` header matches expected algorithm
- [x] Body limit enforced before parsing
- [x] Timeout enforced via context cancellation
- [x] No user input in log format strings (slog structured logging prevents this)
- [x] Path traversal protection in static file server
- [x] CORS: reject `AllowCredentials: true` with `AllowOrigins: ["*"]`
