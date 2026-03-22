# tasks.md — Detailed Task Breakdown

> Track progress by checking boxes. Each task is atomic and testable.

---

## ⭐ PRIORITY: Production Readiness (Do First!)

> **Start here before any new feature work.**

### Current State Assessment

| Area | Current State | Target | Priority |
|------|---------------|--------|----------|
| **Tests** | Non-example packages: 98.7% coverage | 80%+ coverage | **HIGH** |
| **Documentation** | Public API GoDoc complete | GoDoc comments on all exports | **HIGH** |
| **CI/CD** | ✅ Files created | Push & verify workflows run | **HIGH** |
| **Error handling** | Audited and hardened | All edge cases handled gracefully | **MEDIUM** |
| **Security review** | Completed locally | Audit for OWASP top 10 | **MEDIUM** |
| **API stability** | Pre-1.0, latest published tag `v0.3.0` | Semantic versioning, `v0.4.0` release | **HIGH** |

### PR0: CI/CD & Infrastructure ✅ (Files created)
- [x] Create `.github/workflows/test.yml` - runs `go test -race` on Go 1.22/1.23
- [x] Create `.github/workflows/lint.yml` - runs `golangci-lint`
- [x] Create `CHANGELOG.md` - version tracking
- [x] Create `CONTRIBUTING.md` - contributor guide
- [x] **Push to GitHub and verify CI runs** ⬅️ DO THIS FIRST
- [x] Fix any linting errors CI reports

### PR1: Core Test Coverage (HIGH PRIORITY)
- [x] `aarv_test.go` - App creation, configuration, Listen/Shutdown
- [x] `context_test.go` - Context methods, Get/Set, JSON, Text, etc.
- [x] `router_test.go` - Route registration, path params, groups
- [x] `middleware_test.go` - Middleware chain, ordering, error handling
- [x] `bind_test.go` - JSON binding, form binding, query binding
- [x] `validate_test.go` - All validation rules
- [x] `hooks_test.go` - Lifecycle hooks execution order

### PR2: Plugin Test Coverage (HIGH PRIORITY)
- [x] `plugins/logger/logger_test.go` (currently 0%)
- [x] `plugins/verboselog/verboselog_test.go` (currently 90.1%)
- [x] `plugins/encrypt/encrypt_test.go` (currently 86.4%)
- [x] `plugins/cors/cors_test.go` (currently 0%)
- [x] All other plugins need tests

### PR3: GoDoc Comments (HIGH PRIORITY)
- [x] `aarv.go` - App, New(), Listen(), Use(), Get/Post/etc.
- [x] `context.go` - Context, JSON(), Text(), Param(), Query()
- [x] `router.go` - RouteGroup, Group()
- [x] `bind.go` - Bind(), BindReq()
- [x] `validate.go` - Validation rules
- [x] `hooks.go` - Hook types, AddHook()
- [x] All plugins and codecs

### PR4: Error Handling Audit
- [x] Review all error returns in core code
- [x] Ensure panics are recovered in middleware
- [x] Test nil pointer scenarios
- [x] Test malformed JSON, missing fields
- [x] Test concurrent access scenarios

### PR5: Security Review
- [x] Review for OWASP top 10 vulnerabilities
- [x] Ensure no secrets logged, proper redaction
- [x] Review TLS configuration defaults
- [x] Run `govulncheck ./...`

Note: `govulncheck` reports 4 Go standard library vulnerabilities in `go1.26.0`, all fixed in `go1.26.1`. Follow-up remediation is a Go toolchain upgrade rather than a repo code change.

### PR6: Release Prep (v0.3.0)
- [x] All CI checks passing
- [x] Test coverage > 80%
- [x] All GoDoc comments complete
- [x] `git tag -a v0.3.0 -m <Proper message>`
- [x] Create GitHub Release

Note: excluding `examples/...`, combined package coverage is 98.7%. Latest `main` workflow runs for Tests and Lint completed successfully on March 8, 2026, and `v0.3.0` is published on GitHub.

### PR7: Release Prep (v0.4.0)
- [x] Finalize changelog entries for middleware, hooks, examples, and routing improvements
- [x] Re-run full verification suite on the final release candidate tree
- [x] Re-run key example applications locally
- [x] Confirm docs do not reference private/local-only benchmark modules as shipped repo content
- [x] Tag and publish `v0.4.0`

---

## Phase 1: Foundation (M1) ✅ COMPLETE

### 1.1 Project Scaffolding ✅
- [x] Initialize Go module: `go mod init github.com/nilshah80/aarv`
- [x] Set `go 1.22.0` in `go.mod`
- [x] Create directory structure (see spec for layout)
- [x] Add `LICENSE` (MIT)
- [x] Add `.gitignore`
- [x] Add `golangci-lint` config (`.golangci.yml`)
- [x] Set up GitHub Actions CI: lint, test, race detector, coverage

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
- [x] Unit tests: buffering, Content-Length, hijack passthrough

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
- [x] Unit tests: custom 404/405 handler invocation
- [x] Integration test: register 50+ routes, verify no conflicts

### 1.7 Route Groups ✅
- [x] Define `RouteGroup` struct: prefix, parent app/group, scoped middleware
- [x] Implement `Group(prefix, fn func(RouteGroup))` on App
- [x] Implement nested groups: `Group` on `RouteGroup`
- [x] Internal: nested `http.ServeMux` + `http.StripPrefix` composition
- [x] Scoped middleware: group middleware only applies to group routes
- [x] Implement `RouteOption`: `WithName`, `WithTags`, `WithDescription`, `WithRouteMiddleware`, `WithRouteMaxBodySize`
- [x] Unit tests: group prefix, nested groups, scoped middleware isolation

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
- [x] Implement registration-time struct inspection via `buildStructBinder(...)`
- [x] Parse struct tags: `param`, `query`, `header`, `cookie`, `form`, `default`
- [x] Build field map: `[]fieldBinding{source, name, fieldIndex, kind, defaultValue}`
- [x] Implement runtime binding via `structBinder.bind(c, dest)`
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

## Phase 3: Validation Engine (M3)

### 3.1 Tag Parser ✅
- [x] Parse `validate:"required,min=2,max=100,oneof=a b c"` tag format
- [x] Handle nested rules: `validate:"dive,required,min=1"`
- [x] Handle multiple struct fields
- [x] Cache parsed rules per type (sync.Map keyed by reflect.Type)

### 3.2 Built-in Rules
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
- [x] Unit tests: every rule, edge cases (empty string, nil pointer, zero value)

### 3.3 Validator Core
- [x] Implement `buildStructValidator()` — registration-time rule compilation
- [x] Pre-compute field indices via `reflect.StructField.Index`
- [x] Build `[]fieldValidator{index, kind, rules}` slice
- [x] Implement `validate(ptr any) []ValidationError` using `reflect.FieldByIndex`
- [x] Custom rule registration: `RegisterRule(name, func(field reflect.Value, param string) bool)`
- [x] `SelfValidator` interface: if type implements `Validate() []ValidationError`, call it
- [x] `StructLevelValidator` interface for struct-level validation
- [x] `RegisterStructValidation(type, fn)` for external struct-level validators
- [ ] Implement `unsafe.Pointer` + offset fast path (currently uses reflect)
- [x] Unit tests: custom rules, self-validator, nested structs
- [x] Benchmark: validation path comparison vs go-playground/validator

### 3.4 Error Formatting
- [x] Implement `ValidationError` struct: Field, Tag, Param, Value, Message
- [x] Auto-generate human-readable messages per rule (hardcoded in `formatMessage`)
- [x] Configurable message templates
- [x] JSON serialization as 422 response
- [x] Integration with error handler

---

## Phase 4: Middleware & Hooks (M4)

### 4.0 Priority First
- [x] Reduce default logger overhead in full route-level benchmarks
  Current benchmark signal: route-level logger cost is materially down, isolated logger cost is already competitive, and fair logger is now a near-parity apples-to-apples comparison (`Fair Logger`: aarv `156K RPS`, p99 `1.97ms`; Mach `159K`, p99 `1.87ms`; Gin `157K`, p99 `1.90ms`)
- [x] Reduce default encrypt path overhead
  Current benchmark signal: encrypt middleware is competitive in isolation and near-parity in fair real TCP load tests (`Fair Encryption`: aarv `154K RPS`, p99 `1.99ms`; Mach `155K`, p99 `1.93ms`; Gin `154K`, p99 `1.94ms`)
- [x] Fix benchmark CPU Time / CPU% reporting
  Current benchmark signal: load-test CPU metrics now use real process CPU time via `getrusage` and produce sane values (~`39s`-`41s`, ~`78%`-`80%`)
- [x] Do another stdlib-only pass on the static/vanilla path
  Current benchmark signal: vanilla is now essentially tied in realistic load tests (`Vanilla`: aarv `158K RPS`, p99 `1.79ms`; Mach `159K`, p99 `1.77ms`; Gin `158K`, p99 `1.78ms`)
- [x] Revisit single-request bind path vs Mach on the std codec
  Current benchmark signal: after making the comparison feature-fair by validating on Mach/Fiber too, aarv now leads on both `Bind` and `BindLight` (`Bind`: aarv `2125 ns/op` vs Mach `2570`; `BindLight`: aarv `977.7 ns/op` vs Mach `1421`)
- [x] Explore reducing residual vanilla / bare-min overhead from baseline context propagation
  Current benchmark signal: added opt-in `WithRequestContextBridge(false)` fast mode for middleware stacks that do not need cloned-request compatibility; it narrows bare-min logger/encrypt cost but intentionally trades away raw `r.WithContext(...)` bridge behavior
- [ ] Decide whether to recommend `WithRequestContextBridge(false)` outside performance-focused deployments
  Current benchmark signal: fast mode trims the bare-min path (`BareMinLogger`: `2454 -> 2364 ns/op`; `BareMinEncrypt`: `2617 -> 2558 ns/op`) but is not a safe default because cloned-request compatibility is intentionally disabled

Notes from latest benchmark pass:
- Validator internals are in good shape and should not be the next optimization target
- Context pooling is already a clear win over alloc-per-request
- Codec encode path is effectively solved: std/optimized/raw are at parity
- JSONLight and ParamLight are ahead of both Mach and Gin
- Bind is now meaningfully fairer, and aarv leads once the other side validates too; opt-in `segmentio` remains the clearest extra decode-speed lever while staying out of core
- Verbose logging is one of aarv's strongest benchmark areas
- Fair logger and fair encrypt are near-parity when the emitted log fields and encryptor behavior are matched exactly
- `requestid` remains opt-in; the default framework cost here is request-context bridging, not built-in request ID generation

### 4.1 Middleware Chain
- [x] Implement middleware chain builder: `[]Middleware` → single `http.Handler`
- [x] Pre-build chain at startup (not per request)
- [x] Support `func(http.Handler) http.Handler` (stdlib compatible)
- [x] Support `func(next HandlerFunc) HandlerFunc` (framework specific)
- [x] Adapter: convert between the two middleware types
- [ ] Named middleware interface for debug/route listing
- [x] Unit tests: chain order (onion model), early return, error propagation

### 4.2 Hook System
- [x] Define `HookPhase` enum: OnRequest, PreRouting, PreParsing, PreValidation, PreHandler, OnResponse, OnSend, OnError, OnStartup, OnShutdown
- [x] Implement `HookRegistry`: store hooks per phase with priority
- [x] Implement `AddHook(phase, hook)` and `AddHookWithPriority(phase, priority, hook)`
- [x] Implement hook execution: run all hooks for a phase in priority order
- [x] Hook error handling: OnRequest errors trigger error handler; OnResponse/OnSend errors ignored
- [x] Wire hooks into request lifecycle: OnRequest, OnResponse, OnSend, OnError, OnStartup, OnShutdown
- [x] Hook error handling: full error propagation to OnError phase for all hooks
- [x] Wire PreRouting, PreParsing, PreValidation, PreHandler phases (defined but not invoked)
- [x] Unit tests: hook ordering, error short-circuit, all phases fire correctly

### 4.3 Route Group Middleware
- [x] Wire group-level `Use()` to apply middleware only to group routes
- [x] Verify isolation: group middleware doesn't leak to sibling groups
- [x] Route-level middleware via `WithRouteMiddleware` option
- [x] Integration test: global + group + route middleware ordering

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

### 5.12 Multipart File Upload Helper
- [ ] Implement `c.FormFile(name string) (*UploadedFile, error)` helper
- [ ] Implement `c.FormFiles(name string) ([]*UploadedFile, error)` for multiple files
- [ ] `UploadedFile` struct: Filename, Size, ContentType, Header, Open() (returns io.Reader)
- [ ] Implement `c.SaveFile(file *UploadedFile, dst string) error` helper
- [ ] Integration with binder: `file` struct tag for file binding
- [ ] Configurable: max file size, allowed content types, max files per field
- [ ] Configurable: memory threshold for disk streaming (default 32MB, then stream to temp file)
- [ ] Progress callback: `OnProgress func(bytesRead, totalBytes int64)` for upload tracking
- [ ] Chunked upload support: resume interrupted uploads via `Content-Range` header
- [ ] Unit tests: single file, multiple files, size limit, content type validation, progress callback

### 5.13 Cookie Signing & Encryption
- [ ] Implement `SecureCookie` helper using `crypto/hmac` for signing
- [ ] Implement `c.SetSecureCookie(name, value, secret)` — signs cookie value
- [ ] Implement `c.SecureCookie(name, secret)` — verifies and returns unsigned value
- [ ] Implement optional encryption using `crypto/aes` (AES-GCM)
- [ ] `c.SetEncryptedCookie(name, value, key)` — encrypt + sign
- [ ] `c.EncryptedCookie(name, key)` — decrypt + verify
- [ ] Configurable: expiry, path, domain, secure, httpOnly, sameSite
- [ ] Unit tests: sign/verify, encrypt/decrypt, tamper detection, expiry

### 5.14 Server-Sent Events (SSE) Helper
- [ ] Implement `c.SSE()` that returns `*SSEWriter`
- [ ] `SSEWriter.Send(event SSEEvent)` — writes event to response
- [ ] `SSEEvent` struct: Event (name), Data, ID, Retry
- [ ] Auto-set headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`
- [ ] `SSEWriter.Flush()` for immediate send
- [ ] `SSEWriter.Close()` for graceful shutdown
- [ ] Handle client disconnect via `context.Done()`
- [ ] Unit tests: event format, multiple events, client disconnect

---

## Phase 6: Auth Plugins (M6)

### 6.1 JWT Plugin
- [ ] Implement JWT parser (split header.payload.signature, base64url decode)
- [ ] Implement HMAC signing/verification (HS256, HS384, HS512) via `crypto/hmac`
- [ ] Implement RSA signing/verification (RS256, RS384, RS512) via `crypto/rsa`
- [ ] Implement ECDSA signing/verification (ES256, ES384, ES512) via `crypto/ecdsa`
- [ ] Implement EdDSA signing/verification via `crypto/ed25519`
- [ ] Token lookup: header (`Authorization: Bearer`), query param, cookie
- [ ] Standard claims validation: `exp`, `nbf`, `iat`, `iss`, `aud`
- [ ] Custom claims validation: `ClaimsValidator func(claims map[string]any) error` callback
- [ ] Typed claims extraction: `GetClaims[T](c *Context) T` generic helper
- [ ] Store claims in Context via configurable key
- [ ] Skip paths configuration
- [ ] `KeyFunc` callback for JWKS / key rotation support
- [ ] Error handler for auth failures (401/403)
- [ ] Token creation helper: `SignToken(claims, key)` → string
- [ ] Token refresh helper: `RefreshToken(token, key, newExp)` → string
- [ ] Unit tests: each algorithm, expired token, invalid signature, missing token, skip paths
- [ ] Unit tests: custom claims validation, typed claims extraction
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

### 6.6 RFC 7807 Problem Details
- [ ] Implement `ProblemDetails` struct per RFC 7807: type, title, status, detail, instance
- [ ] Add `extensions` map for custom fields
- [ ] Create `ValidationProblem` formatter that wraps validation errors in RFC 7807 format
- [ ] Configurable: enable/disable RFC 7807 format globally or per-route
- [ ] Content-Type: `application/problem+json`
- [ ] Unit tests: RFC 7807 compliance, extension fields

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

### 10.1 Rate Limiter
- [ ] Token bucket algorithm, zero-dep, `sync.Mutex` based
- [ ] Sliding window variant for smoother rate limiting
- [ ] Per-IP keying via `c.RealIP()`
- [ ] Custom key function: `KeyFunc func(*Context) string` (e.g., user ID, API key)
- [ ] `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` headers
- [ ] Configurable response on limit: custom status code, message, handler
- [ ] Skip paths configuration (e.g., health checks)
- [ ] Burst allowance configuration
- [ ] Optional: `plugins/ratelimit-redis/go.mod` for distributed rate limiting via Redis
- [ ] Unit tests: within limit, exceed limit, custom key, headers

### 10.2 Throttle
- [ ] Max concurrent in-flight requests via `chan struct{}`
- [ ] Configurable: max concurrent, queue size, timeout
- [ ] Return 503 Service Unavailable when queue full
- [ ] Unit tests: under limit, at limit, queue timeout

### 10.3 CSRF Protection
- [ ] Double-submit cookie pattern
- [ ] Token generation via `crypto/rand`
- [ ] `X-CSRF-Token` header validation
- [ ] Skip safe methods (GET, HEAD, OPTIONS)
- [ ] Configurable: cookie name, header name, token length
- [ ] Unit tests: valid token, invalid token, missing token, safe methods

### 10.4 IP Filter
- [ ] Whitelist mode — only allow listed CIDRs
- [ ] Blacklist mode — block listed CIDRs
- [ ] `net.ParseCIDR` based matching
- [ ] Configurable: custom block response
- [ ] Unit tests: allow, block, CIDR ranges

### 10.5 Request Sanitizer
- [ ] Strip HTML tags from string fields
- [ ] Normalize Unicode (NFC)
- [ ] Configurable: fields to sanitize, custom sanitizer functions
- [ ] Unit tests: HTML stripping, Unicode normalization

---

## Phase 11: Observability Plugins (M11)

### 11.1 Prometheus Plugin
- [ ] Create `plugins/prometheus/go.mod` — separate module
- [ ] Request counter (method, path, status)
- [ ] Request duration histogram
- [ ] In-flight requests gauge
- [ ] Response size histogram
- [ ] Configurable: buckets, path grouping/normalization (collapse path params)
- [ ] Custom collectors registration
- [ ] Unit tests: metric collection, path grouping

### 11.2 OpenTelemetry Plugin
- [ ] Create `plugins/otel/go.mod` — separate module
- [ ] Span creation per request with configurable span naming
- [ ] W3C Trace Context propagation (traceparent, tracestate headers)
- [ ] Attribute injection: method, path, status, user_agent, request_id
- [ ] Error recording: span.RecordError on handler errors
- [ ] Metrics export: request count, duration, size via OTLP
- [ ] Log correlation: inject trace_id, span_id into slog logger
- [ ] Baggage propagation support
- [ ] Configurable: exporter (OTLP, Jaeger, Zipkin), sampling rate
- [ ] Unit tests: span creation, context propagation, attribute injection

### 11.3 Pprof Plugin
- [ ] Mount `net/http/pprof` at configurable prefix (stdlib, no external deps)
- [ ] Optional: authentication middleware for pprof endpoints
- [ ] Unit tests: endpoint availability

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

## Phase 12.5: OpenAPI / Swagger Generator (M12.5)

### 12.5.1 OpenAPI Core
- [ ] Define `OpenAPIConfig` struct: title, version, description, servers, contact, license
- [ ] Implement route introspection: collect all registered routes with metadata
- [ ] Auto-generate path parameters from `{param}` patterns
- [ ] Auto-generate request body schema from `Bind[Req, Res]` type parameters
- [ ] Auto-generate response schema from handler return types
- [ ] Extract validation rules from `validate:""` tags → OpenAPI constraints (min, max, required, enum)
- [ ] Implement `/openapi.json` endpoint handler
- [ ] Implement `/openapi.yaml` endpoint handler
- [ ] Optional: Swagger UI integration via embedded static files
- [ ] Optional: ReDoc integration
- [ ] Configurable: paths to include/exclude, security schemes
- [ ] Unit tests: schema generation, constraint mapping, endpoint output

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
- [x] Comparison: framework vs Gin vs Mach vs Fiber (tests/benchmark/compare_test.go)
- [x] Load test: 500K requests, 100 VCs, real TCP with latency/memory/CPU metrics

### Examples — PARTIALLY COMPLETE
- [x] examples/hello — minimal hello world
- [x] examples/rest-crud — full CRUD with typed handlers
- [ ] examples/jwt-auth — JWT protected API
- [ ] examples/file-upload — multipart form handling with binder integration
- [ ] examples/middleware-chain — custom middleware
- [ ] examples/plugin-custom — writing a plugin
- [ ] examples/tls-http2 — HTTPS setup
- [ ] examples/microservice — health check + prometheus + structured logging
- [ ] examples/sse — server-sent events real-time updates
- [ ] examples/openapi — auto-generated OpenAPI docs with Swagger UI

---

## Phase 14: Nice-to-Have Features (M14) — OPTIONAL

### 14.1 WebSocket Support
- [ ] Implement WebSocket upgrader using `golang.org/x/net/websocket` or `nhooyr.io/websocket`
- [ ] Alternative: Create `plugins/websocket/go.mod` as separate module to avoid core deps
- [ ] `c.Upgrade()` returns `*WebSocketConn`
- [ ] Message reading/writing: `conn.ReadMessage()`, `conn.WriteMessage()`
- [ ] JSON helpers: `conn.ReadJSON()`, `conn.WriteJSON()`
- [ ] Ping/Pong heartbeat handling
- [ ] Connection close handling with status codes
- [ ] Configurable: read/write buffer sizes, handshake timeout, compression
- [ ] Unit tests: upgrade, message exchange, close handling

### 14.2 GraphQL Adapter
- [ ] Create `plugins/graphql/go.mod` as separate module
- [ ] Adapter for `graphql-go/graphql` library
- [ ] Adapter for `99designs/gqlgen` generated handlers
- [ ] Route helper: `app.GraphQL("/graphql", schema)` or `app.GraphQL("/graphql", gqlgenHandler)`
- [ ] Playground/GraphiQL integration (optional static embed)
- [ ] Context propagation: aarv Context → GraphQL resolver context
- [ ] Unit tests: query execution, mutations, subscriptions routing

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

---

## Appendix: Probably Not Required / Over-Engineering

> **Note**: The features below were considered but are likely over-engineering for a Go web framework.
> They add .NET-style ceremony that conflicts with Go's philosophy of simplicity and explicitness.
> Documented here for reference — implement only if there's strong user demand.

### A.1 Results Helpers (.NET IResult Pattern)
**Why probably not needed**: Go already has `c.JSON()`, `c.Text()`, `c.Redirect()`, etc. This is just syntax sugar that adds an abstraction layer without real benefit. The existing Context methods are already clean and idiomatic Go.

- [ ] Implement `Result` interface with `Execute(*Context) error` method
- [ ] `Results.Ok(data)` — 200 with JSON body
- [ ] `Results.Created(location, data)` — 201 with Location header
- [ ] `Results.Accepted(data)` — 202 with optional body
- [ ] `Results.NoContent()` — 204 empty response
- [ ] `Results.BadRequest(error)` — 400 with error details
- [ ] `Results.Unauthorized()` — 401
- [ ] `Results.Forbidden()` — 403
- [ ] `Results.NotFound()` — 404
- [ ] `Results.Conflict(error)` — 409
- [ ] `Results.UnprocessableEntity(errors)` — 422 with validation errors
- [ ] `Results.TooManyRequests()` — 429
- [ ] `Results.InternalError(error)` — 500
- [ ] `Results.File(path, contentType)` — file download
- [ ] `Results.Stream(reader, contentType)` — streaming response
- [ ] `Results.Redirect(url)` — 302 redirect
- [ ] `Results.Problem(details)` — RFC 7807 Problem Details response
- [ ] Generic `BindResult[Req, Res]` wrapper that accepts `Result` return type

### A.2 Endpoint Filters (.NET IEndpointFilter Pattern)
**Why probably not needed**: Middleware already provides pre/post handler functionality with the onion model. Filters are a .NET pattern that duplicates what middleware does. Adding another abstraction layer increases complexity without clear benefit.

- [ ] Define `EndpointFilter` interface: `Filter(ctx *FilterContext, next FilterDelegate) Result`
- [ ] `FilterContext` struct: Context, Arguments (bound request), Metadata
- [ ] Pre-handler filters that can short-circuit (return early)
- [ ] Post-handler filters for response modification
- [ ] Filter pipeline composition (multiple filters in order)
- [ ] Per-route filter registration via `WithFilters(filter1, filter2)`
- [ ] Global filter registration via `app.AddFilter(filter)`
- [ ] Built-in filters: `AuthorizationFilter`, `LoggingFilter`, `CacheFilter`, `ValidationFilter`

### A.3 Route Groups Enhancement (Fluent Builder)
**Why probably not needed**: Current `Group()` with `Use()` already works well. Fluent builders add ceremony. `RequireAuthorization()` can be done with middleware. Keep it simple.

- [ ] Fluent route group builder: `app.MapGroup("/api/v1").WithTags("v1").WithFilters(...)`
- [ ] Group-level metadata inheritance to child routes
- [ ] Group-level API versioning via prefix or header
- [ ] `group.RequireAuthorization(policy)` — apply auth to all routes in group
- [ ] `group.AllowAnonymous()` — exempt group from parent auth

### A.4 Parameter Binding Enhancements
**Why probably not needed**: The `services` tag over-complicates binding. Use `c.MustGet()` or decorators explicitly — it's clearer. `AsParameters` is a niche use case that adds complexity to the binder for minimal benefit.

- [ ] `services` struct tag: inject from decorator registry (like [FromServices])
- [ ] `AsParameters` support: flatten nested struct into parent binding
- [ ] Custom binder registration: `RegisterBinder[T](fn func(*Context) (T, error))`
- [ ] Automatic service resolution in `Bind` handlers via decorator lookup

### A.5 Verbose Endpoint Metadata
**Why probably not needed**: `.WithName()`, `.WithTags()`, `.WithDescription()` already exist. The additional `.Produces[T]()`, `.ProducesProblem()`, `.RequireAuthorization()` add ceremony to every route definition. OpenAPI can infer most of this from `Bind[Req, Res]` types automatically.

- [ ] `.WithSummary("...")` — OpenAPI summary (short description)
- [ ] `.Produces[TResponse](statusCode)` — document response type for status code
- [ ] `.Produces(statusCode, contentType)` — document response without type
- [ ] `.ProducesProblem(statusCode)` — document RFC 7807 error response
- [ ] `.ProducesValidationProblem()` — document 400 validation error response
- [ ] `.Accepts[TRequest](contentType)` — document accepted request body types
- [ ] `.RequireAuthorization(policy...)` — mark endpoint as requiring auth
- [ ] `.AllowAnonymous()` — exempt endpoint from auth requirements
- [ ] `.WithOpenApi(fn)` — custom OpenAPI operation modifier callback
- [ ] `.ExcludeFromDescription()` — hide endpoint from OpenAPI docs
- [ ] `.WithGroupName("v1")` — API versioning group

### A.6 Dependency Injection Container
**Why probably not needed**: Go philosophy is explicit over implicit. The existing `Decorate`/`Resolve` pattern is sufficient. Full DI containers add magic and make code harder to trace. Just pass dependencies explicitly or use decorators.

- [ ] Service registration: `app.Services.AddSingleton[T](factory)`
- [ ] Service registration: `app.Services.AddScoped[T](factory)` — per-request
- [ ] Service registration: `app.Services.AddTransient[T](factory)` — new each time
- [ ] Service resolution: `GetService[T](c *Context) T`
- [ ] Constructor injection via decorator pattern
- [ ] Lazy initialization for expensive services
- [ ] Service disposal on request end (for scoped services)

### A.7 Response Caching
**Why probably not needed**: Most production apps use Redis, CDN, or reverse proxy (nginx, Cloudflare) for caching. Framework-level caching is rarely used and adds complexity. The existing ETag plugin handles conditional requests.

- [ ] `.CacheOutput(duration)` route option — cache response for duration
- [ ] Vary by query parameters: `VaryByQuery("page", "limit")`
- [ ] Vary by headers: `VaryByHeader("Accept-Language")`
- [ ] Cache tags for targeted invalidation
- [ ] Memory-based cache store (default, zero-dep)
- [ ] Optional: `plugins/cache-redis/go.mod` for distributed caching

### A.8 Typed HTTP Client Factory
**Why probably not needed**: Just use `http.Client` with your own wrapper. Every team has different preferences for HTTP clients (resty, req, etc.). A framework-provided client adds opinions where none are needed.

- [ ] Named client registration: `app.AddHttpClient("github", config)`
- [ ] Base URL configuration
- [ ] Default headers and timeout
- [ ] Retry policies with exponential backoff
- [ ] Circuit breaker pattern
- [ ] Request/response logging
- [ ] OpenTelemetry span propagation

### A.9 Request Decompression
**Why probably not needed**: Rarely needed — most APIs receive uncompressed JSON. If needed, it's a simple middleware to write. Not worth adding to core.

- [ ] Auto-detect `Content-Encoding` header (gzip, deflate, br)
- [ ] Decompress request body before parsing
- [ ] Configurable: max decompressed size, allowed encodings
