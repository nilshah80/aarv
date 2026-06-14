# tasks.md — Detailed Task Breakdown

> Track progress by checking boxes. Each task is atomic and testable.

---

## 📋 Pending Work Summary (Buckets)

> Snapshot of remaining unchecked items as of 2026-06-14: **107 unchecked / 703
> checked**. Items are referenced by section rather than line number (line
> numbers drift). This summary is a dashboard; the authoritative state is the
> checkboxes in each phase below.

**Bottom line:** there is no actionable in-repo backlog. Everything still
unchecked is gated on external (ALP) demand, decided won't-do, or explicitly
optional / over-engineering. The only items that could become real work *if a
need arises* are the optional Redis/SQL session stores.

### Bucket A — Stale: 0 (all reconciled to git/CI reality)

### Bucket B — 3 intentionally remaining (of 12; 9 resolved)

| Section | Item | Disposition |
|---------|------|-------------|
| §12.6.7 | Prometheus optional auth-class labels | Reviewed → gated: no safe default (cardinality); opt-in only, pending concrete ALP demand |
| §12.6.8 | ALP note: swap internal HMAC/ratelimit/idempotency for Aarv imports | External — lives in ALP's `AARV_FEEDBACK.md` |
| §12.6.8 | ALP note: evaluate `BindRoute` + openapi/openapi-ui for drift detection | External — ALP's repo |

Resolved this session: fuzz targets (§Cross-Cutting/Testing), `Bind[T]` escape
audit (§Cross-Cutting/Performance), named-middleware introspection (§4.1),
recovery stack-in-response (§5.1), context-bridge recommendation (§4.0),
advisory gocyclo (§13 Quality Gates), session normalize dedupe (§13 Quality
Gates). The unsafe.Pointer validator fast path (§3.3) was declined.

### Decided won't-do — 5 (unchecked strikethroughs, by design)

| Section | Item | Reason |
|---------|------|--------|
| §3.3 | unsafe.Pointer validator fast path | reflect path benchmark-competitive |
| §5.12 | multipart disk-streaming threshold | stdlib `ParseMultipartForm(32MB)` handles it |
| §5.12 | multipart **ingest** progress | distinct from shipped `SaveFileWith` save-progress; streaming/middleware territory |
| §5.12 | chunked/resumable uploads | different protocol (tus-style); plugin territory |
| §6.1 | configurable JWT claims key | superseded by `jwt.From(c)` |

### Bucket C — Optional / deferred — 99

| Group | Section | Count |
|-------|---------|-------|
| Optional distributed stores: `session-redis`, `session-sql` | §6.7 | 2 |
| Phase 14 — WebSocket / Reverse Proxy / GraphQL (OPTIONAL) | §14.1–14.3 | 28 |
| Appendix A.1–A.9 — "Probably Not Required / Over-Engineering" | Appendix A | 69 |

### Tally

| Category | Count |
|----------|-------|
| Bucket A — stale | 0 |
| Bucket B — gated + external (intentional) | 3 |
| Decided won't-do (strikethrough) | 5 |
| Bucket C — optional / deferred | 99 |
| **Total unchecked** | **107** |
| Checked | 703 |

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

Note: historical `govulncheck` findings were limited to Go standard library vulnerabilities in `go1.26.0`; follow-up remediation is to build and release with the current Go 1.26 patch toolchain rather than changing repo code.

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
- [ ] ~~Implement `unsafe.Pointer` + offset fast path (currently uses reflect)~~ — won't-do; the reflect path is benchmark-competitive and validator internals are explicitly not the next optimization target (see Phase 4 benchmark notes). Revisit only if a benchmark proves a material gap
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
- [x] Decided: do **not** recommend `WithRequestContextBridge(false)` generally. Keep the bridge on by default; `false` is an expert-only opt-in for controlled stacks (~90 ns/op on the bare-min path, negligible for typical handlers, correctness risk otherwise). Documented in `docs/middleware.md` "Context bridge"
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
- [x] Named middleware interface for debug/route listing — `aarv.NamedMiddleware(name, mw)` + `NativeMiddleware.Name`; surfaced via `RouteInfo.Middleware` (route + group, in execution order, **excludes** app-global) and `App.GlobalMiddleware()`. Explicit names are the only stable contract; unnamed middleware get best-effort reflect labels. Native fast path preserved
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
- [x] Configurable: custom panic handler
- [x] Include stack in response (debug mode) — `plugins/recover` `Config{IncludeStackInResponse: true}` adds `panic`/`stack` to the 500 body; default off, no effect when a custom `Handler` is set. Identical on native + stdlib paths; security guard test asserts absence by default
- [x] Unit tests: panic in handler, panic in middleware, nested panic

### 5.2 Request ID Plugin ✅
- [x] Generate ULID (monotonic, sortable) using `crypto/rand` + `time`
- [x] Read existing `X-Request-ID` from request header (propagate)
- [x] Set `X-Request-ID` on response header
- [x] Store in Context via `c.Set("requestId", id)`
- [x] Configurable: header name, generator function
- [x] Configurable: prefix for generated IDs
- [x] Unit tests: generation, propagation, custom generator

### 5.3 Logger Plugin ✅
- [x] Log on request start + completion using `log/slog`
- [x] Fields: method, path, status, latency, request_id, client_ip, user_agent, bytes_out
- [x] Skip paths: configurable (e.g., skip /health)
- [x] Log level: configurable (Info default, Debug for verbose)
- [x] Unit tests: log output format, skip paths, latency measurement

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
- [x] Unit tests: preflight, simple request, credentials, dynamic origin

### 5.5 Secure Headers Plugin ✅
- [x] Set all headers from `SecureConfig` struct
- [x] Defaults: XSS protection, nosniff, DENY, HSTS 1yr, strict referrer, CSP, Permissions-Policy
- [x] Configurable per-header
- [x] Unit tests: every header set correctly, custom overrides

### 5.6 Body Limit Plugin ✅
- [x] Wrap `r.Body` with `http.MaxBytesReader`
- [x] Configurable max bytes
- [x] Return 413 Payload Too Large on exceed (via `NewWithResponse`)
- [x] Unit tests: under limit, at limit, over limit

### 5.7 Timeout Plugin ✅
- [x] Wrap handler with `context.WithTimeout`
- [x] Configurable timeout duration
- [x] Return 504 Gateway Timeout on exceed
- [x] Ensure context cancellation propagates
- [x] Unit tests: fast handler, slow handler, exact timeout

### 5.8 Compress Plugin ✅
- [x] Check `Accept-Encoding` header for gzip/deflate support
- [x] Wrap ResponseWriter with `gzip.Writer` (from `compress/gzip`)
- [x] Set `Content-Encoding: gzip` header
- [x] Set `Vary: Accept-Encoding` header
- [x] Skip compression for small responses (configurable threshold)
- [x] Skip compression for already-compressed content types (images, video)
- [x] Configurable: compression level, min size, excluded content types, prefer gzip
- [x] Pool gzip/deflate writers via `sync.Pool`
- [x] Unit tests: compression, skip small, skip images, Content-Encoding header

### 5.9 ETag Plugin ✅
- [x] Compute ETag from response body hash (CRC32)
- [x] Set `ETag` header
- [x] Check `If-None-Match` request header → return 304 Not Modified
- [x] Weak vs strong ETag support
- [x] Configurable: weak mode
- [x] Configurable: hash function selection
- [x] Unit tests: ETag generation, 304 response, weak ETag

### 5.10 Static Files Plugin ✅
- [x] Wrap `http.FileServer` with configurable root
- [x] Index file support (`index.html`)
- [x] SPA fallback (serve index.html for unmatched paths)
- [x] Cache-Control headers (configurable max-age)
- [x] Directory listing toggle (default: disabled)
- [x] Configurable: root dir, index file, prefix strip, browse
- [x] Unit tests: serve file, index, 404, SPA fallback, cache headers

### 5.11 Health Check Plugin ✅
- [x] Register `/health` → returns `{"status": "ok"}` with 200
- [x] Register `/ready` → calls readiness callback, returns 200 or 503
- [x] Register `/live` → calls liveness callback, returns 200 or 503
- [x] Configurable: paths, callbacks
- [x] Configurable: additional info in response
- [x] Unit tests: healthy, unhealthy, custom callbacks

### 5.12 Multipart File Upload Helper
- [x] Implement `c.FormFile(name string) (*UploadedFile, error)` helper
- [x] Implement `c.FormFiles(name string) ([]*UploadedFile, error)` for multiple files
- [x] `UploadedFile` struct: Filename, Size, ContentType, Header, Open() (returns multipart.File)
- [x] Implement `c.SaveFile(file *UploadedFile, dst string) error` helper
- [x] Implement `c.SaveFileWith(file, dst, onProgress func(written, total int64)) error` — **save/copy** progress callback (fires per chunk while writing to dst). `SaveFile` now delegates with a nil callback; nil keeps io.Copy's WriterTo/ReaderFrom fast paths
- [x] Integration with binder: `file` struct tag for file binding
- [x] Configurable: max file size, allowed content types, max files per field via `FileConfig` + `c.FileWith`/`c.FilesWith`
- [ ] ~~Configurable: memory threshold for disk streaming~~ — deferred; stdlib `ParseMultipartForm(32MB)` handles this; configurable later if needed
- [ ] ~~**Ingest** progress callback (network → server)~~ — deferred; distinct from the shipped `SaveFileWith` **save** progress. Ingest progress requires wrapping `r.Body` before multipart parsing AND a streaming `r.MultipartReader()` path (eager `ParseMultipartForm` consumes the whole body up front), and can't be pushed back on the same request — client-side `XMLHttpRequest.upload.onprogress` is the normal answer. Middleware/streaming-upload territory
- [ ] ~~Chunked upload support~~ — deferred; different protocol (tus-style resumable uploads), plugin territory
- [x] Unit tests: single file, multiple files, size limit, content type validation, binder integration

### 5.13 Cookie Signing & Encryption
- [x] Implement `SecureCookie` helper using `crypto/hmac` for signing
- [x] Implement `c.SetSecureCookie(name, value, secret, opts...)` — signs cookie value with HMAC-SHA256
- [x] Implement `c.SecureCookie(name, secret, serverMaxAge...)` — verifies and returns unsigned value
- [x] Implement optional encryption using `crypto/aes` (AES-256-GCM) with encrypt-then-MAC
- [x] `c.SetEncryptedCookie(name, value, key, opts...)` — encrypt + sign with derived subkeys
- [x] `c.EncryptedCookie(name, key, serverMaxAge...)` — decrypt + verify
- [x] Configurable: expiry, path, domain, secure, httpOnly, sameSite via `CookieOptions`
- [x] Unit tests: sign/verify, encrypt/decrypt, tamper detection, expiry, cross-name replay, empty secret, key derivation

### 5.14 Server-Sent Events (SSE) Helper
- [x] Implement `c.SSE() (*SSEWriter, error)` returning `ErrResponseAlreadyWritten` if response is committed
- [x] `SSEWriter.Send(event SSEEvent)` — writes spec-compliant event and auto-flushes
- [x] `SSEEvent` struct: Event (name), Data, ID, Retry
- [x] Auto-set headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache` (no `Connection: keep-alive` — hop-by-hop, forbidden on HTTP/2)
- [x] `SSEWriter.Flush()` for manual flush without event (e.g. after `Comment()`)
- [x] `SSEWriter.Comment(text)` for keepalive comments (SSE `:` lines)
- [x] `SSEWriter.Close()` marks writer closed; idempotent; `Send`/`Comment`/`Flush` return `ErrSSEClosed` after close
- [x] `SSEWriter.Done() <-chan struct{}` for client disconnect in select loops
- [x] Validate `Event` and `ID` fields reject newlines (`ErrInvalidSSEField`); multi-line `Data` splits to multiple `data:` lines
- [x] Unit tests: event format, multiple events, client disconnect, close contract, field validation, response-already-written guard

---

## Phase 6: Auth Plugins (M6)

### 6.1 JWT Plugin
- [x] Implement JWT parser (split header.payload.signature, base64url decode)
- [x] Implement HMAC signing/verification (HS256, HS384, HS512) via `crypto/hmac`
- [x] Implement RSA signing/verification (RS256, RS384, RS512) via `crypto/rsa`
- [x] Implement ECDSA signing/verification (ES256, ES384, ES512) via `crypto/ecdsa`
- [x] Implement EdDSA signing/verification via `crypto/ed25519`
- [x] Token lookup: header (`Authorization: Bearer`), query param, cookie
- [x] Standard claims validation: `exp`, `nbf`, `iat`, `iss`, `aud`
- [x] Custom claims validation: `ClaimsValidator func(claims map[string]any) error` callback
- [x] Typed claims extraction: `GetClaims[T](c *Context) T` generic helper
- [ ] ~~Store claims in Context via configurable key~~ — replaced by hardcoded `identityStoreKey = "jwtClaims"` and accessed via `jwt.From(c)` / `jwt.FromContext(ctx)`. See scope-change note below.
- [x] Skip paths configuration
- [x] `KeyFunc` callback for JWKS / key rotation support
- [x] Error handler for auth failures (401/403)
- [x] Token creation helper: `SignToken(alg, key, claims)` → string
- [x] Token refresh helper: `RefreshToken(token, cfg, signingKey, ttl)` → string
- [x] Unit tests: each algorithm, expired token, invalid signature, missing token, skip paths
- [x] Unit tests: custom claims validation, typed claims extraction
- [x] Security tests: algorithm confusion attack, none algorithm rejection

**Scope changes accepted during implementation:**
- Storage location: claims are stored under a hardcoded internal key (`jwtClaims`). The public access path is `jwt.From(c)` / `jwt.FromContext(ctx)`. A configurable `ContextKey` was rejected for the same reason as §6.2/§6.3: it creates a way for the helpers to silently miss when misconfigured.
- `Algorithms` allow-list policy: empty `Config.Algorithms` defaults to `[HS256]` only when `Config.HMACSecret` is set. When only `KeyFunc` is set, empty `Algorithms` is a configuration error (no silent HS256 fallback that would surprise asymmetric deployments). `HMACSecret` and `KeyFunc` are mutually exclusive.
- NumericDate strictness: `exp`, `nbf`, and `iat` must be JSON integers in `[0, 253402300799]` (year-9999 upper bound). Fractional, string-shaped, negative, and millisecond-scale values are rejected with `ErrInvalidNumericDate`. This is intentionally stricter than RFC 7519 §2 (which permits non-integer NumericDates) and is documented in the package GoDoc and CHANGELOG.
- Configuration error surface: `New(cfg)` panics on misconfiguration to match `apikey` / `basicauth`. `Parse` and `RefreshToken` validate the same `Config` and return typed sentinels (`ErrMissingKey`, `ErrNoAlgorithms`, `ErrConflictingKey`, `ErrSecretAlgMismatch`, `ErrInvalidLookup`, `ErrUnknownAlg`) so programmatic callers can branch on them via `errors.Is` without `recover`.
- `KeyFunc` callback contract: `KeyFunc` receives the parsed JOSE header only. Issuer-based key selection is not framework-supported because `iss` is unverified at key-resolution time. The `(nil, nil)` rule from §6.2/§6.3 carries forward: returning `(nil, nil)` from `KeyFunc` is treated as auth failure (the plugin refuses to attempt verification with a nil key). `ClaimsValidator` returns only `error` and is unaffected.
- `RefreshToken` signature: `RefreshToken(token, cfg, signingKey, ttl time.Duration)` rather than the original sketch `RefreshToken(token, key, newExp)`. `ttl < time.Second` returns `ErrInvalidTTL` (NumericDate is second-granular per RFC 7519, and a sub-second `ttl` would issue a token whose `exp` equals `iat`); `iat` is set to `now` and `exp` to `now.Add(ttl)`. Other claims (including `jti`) are preserved unchanged — callers wanting to rotate `jti` must do it themselves.

### 6.2 API Key Plugin
- [x] Lookup from header (configurable name) or query param
- [x] Validator callback: `func(key string) (any, error)` — returns identity
- [x] Configurable: header name, query param name, error message
- [x] Unit tests: valid key, invalid key, missing key

**Scope changes accepted during implementation:**
- Validator signature: `func(key string) (any, error)` instead of `func(key string) (bool, error)`. Reason: matches the planned §6.4 Bearer Token validator and avoids forcing a second lookup to retrieve caller identity. Recorded under `[Unreleased]` in `CHANGELOG.md`.
- Storage location: identity is stored under a hardcoded internal key, not a configurable one. The public access path is `apikey.From(c)` / `apikey.FromContext(ctx)`. A configurable `ContextKey` would have created a way for the helpers to silently miss; removed in favor of a single canonical access path.
- Validator contract: returning `(nil, nil)` is treated as authentication failure (401), not authenticated-as-nil. Reason: `context.Context.Value` cannot distinguish a stored nil from a missing value, so allowing nil identity would make `FromContext` lie.
- `StaticKeys` helper hashes stored and presented keys to fixed-length 32-byte SHA-256 digests at snapshot/lookup time so the key-length side channel exposed by byte-by-byte constant-time compares is closed. The map lookup itself remains a small "is this hash known" timing channel; SHA-256 here is for in-memory side-channel resistance, not at-rest key protection.

### 6.3 Basic Auth Plugin
- [x] Parse `Authorization: Basic base64(user:pass)` header
- [x] Validator callback: `func(user, pass string) (any, error)` — returns identity
- [x] Realm configuration
- [x] Unit tests: valid credentials, invalid, missing header

**Scope changes accepted during implementation:**
- Validator signature: `func(user, pass string) (any, error)` instead of `func(user, pass string) (bool, error)`. Reason: matches §6.2 API Key and planned §6.4 Bearer Token; lets the validator return a user record so handlers don't need a second lookup. Recorded under `[Unreleased]` in `CHANGELOG.md`.
- Storage location: identity stored under a hardcoded internal key. Public access is `basicauth.From(c)` / `basicauth.FromContext(ctx)`. Same rationale as §6.2.
- Validator contract: `(nil, nil)` is treated as authentication failure. Same rationale as §6.2.

**Additions on top of spec (correctness, not scope creep):**
- `WWW-Authenticate: Basic` challenge emitted on 401, including configured `realm` and optional `charset` parameters. Required by RFC 7235 for browsers to prompt for credentials. Suppressed for non-401 statuses (e.g. validator-returned `ErrForbidden` → 403 with no challenge).
- `Realm` is validated at `New()` for characters that would produce a malformed header (`"`, `\`, control chars). `Charset`, when non-empty, must be `"UTF-8"` (matched case-insensitively) per RFC 7617 §2.1; any other value panics at `New()`. Misconfiguration panics at startup rather than corrupting responses at runtime.
- Scheme matching is case-insensitive (`Basic`, `basic`, `BASIC`) per RFC 7235 §2.1.
- `StaticCreds` helper provided, mirroring §6.2's `StaticKeys`. Stored passwords are hashed to fixed-length 32-byte SHA-256 digests at snapshot time; per-request comparison hashes the attempted password and uses `crypto/subtle.ConstantTimeCompare` on the equal-length digests so the password-length side channel exposed by `ConstantTimeCompare`'s length-mismatch fast-exit is closed. The map lookup itself remains a small "is this username known" timing channel; SHA-256 here is for in-memory side-channel resistance, not at-rest password protection (use bcrypt/argon2 for that).

### 6.4 Bearer Token Plugin ✅
- [x] Extract `Authorization: Bearer <token>` from header
- [x] Validator callback: `func(token string) (any, error)` — returns claims/user
- [x] Store result in Context
- [x] Unit tests: valid token, invalid, missing

**Scope additions on top of spec (mirroring §6.2 apikey / §6.3 basicauth conventions):**
- Validator signature returns `(any, error)` (not `bool`) so a single lookup yields both authentication and identity, matching apikey/basicauth.
- `(nil, nil)` from the validator is treated as authentication failure: `context.Context.Value` cannot distinguish a stored nil from a missing one, so the plugin refuses to store nil identities.
- Identity stored under hardcoded internal key (`bearerIdentity`); public access via `bearer.From(c)` / `bearer.FromContext(ctx)` only — no configurable context key, same rationale as apikey/basicauth.
- Optional `Config.Query` for query-parameter token transport (RFC 6750 §2.3); disabled by default because URL-borne tokens leak via proxy logs / Referer / browser history. Form-body transport (§2.2) is intentionally not supported (interferes with body parsers, discouraged by spec).
- **Header presence is exclusive** (RFC 6750 §2 single-transport model): when the configured `Header` is non-empty on a request it MUST yield a valid Bearer token, and the `Query` parameter is NOT consulted — even when a valid query token is also present. Closes an audit-bypass where a malformed header (`Authorization: Bearer ` with no token, `Authorization: Basic …`, etc.) is paired with a query token to evade header-only logging.
- Stdlib path uses the new `(*Context).BindRequest` core helper to re-bind `c` to the (possibly upstream-mutated) `r` before stamping identity. Preserves any upstream `r.Clone()` URL/Header/Body mutations rather than silently discarding them in favor of the pre-mutation request bound to `*aarv.Context`.
- **AppError shape parity across native and stdlib middleware paths (default response pipeline only)**: when the validator returns an `*aarv.AppError` and the framework uses its default ErrorHandler, default JSON codec/Content-Type, and no response-mutating OnError, both paths emit byte-identical responses (status, body bytes, Content-Type, WWW-Authenticate) including `Code()` and `Detail()`. With any of `WithErrorHandler`, `WithCodec`, or a response-mutating `OnError` installed, the native path flows through the framework's pipeline while the stdlib path writes JSON directly, so responses diverge — documented in the package godoc.
- `WWW-Authenticate: Bearer realm="..."` challenge emitted on 401 per RFC 6750 §3 / RFC 7235; suppressed for non-401 statuses (e.g. validator-returned `aarv.ErrForbidden`).
- `Realm` validated at `New()` for characters that would yield a malformed header (`"`, `\`, control chars); panics on misconfiguration.
- Scheme matching is case-insensitive (`Bearer`, `bearer`, `BEARER`) per RFC 7235 §2.1; one extra leading space after the scheme is tolerated.
- `StaticTokens` helper mirroring §6.2's `StaticKeys`: stored tokens are hashed to fixed-length 32-byte SHA-256 digests at snapshot time so the token-length side channel is closed. SHA-256 here is for in-memory side-channel resistance, not at-rest token protection (use a real credential store for that).
- Configuration error surface: `New(cfg)` panics on misconfiguration (nil validator, both Header and Query empty, invalid Realm) to match apikey/basicauth.

### 6.5 RBAC Plugin ✅
- [x] Define `RoleExtractor = func(*Context) []string`
- [x] Middleware: `RequireRoles("admin", "editor")` → check extracted roles
- [x] Middleware: `RequireAnyRole("admin", "editor")` → OR check
- [x] Return 403 Forbidden on mismatch
- [x] Unit tests: has role, missing role, multiple roles

**Scope decisions made during implementation:**
- API shape: `New(Config) *Authorizer` returns a factory; the same `*Authorizer` produces many distinct middlewares via `RequireRoles(...)` (AND) and `RequireAnyRole(...)` (OR). One `RoleExtractor` is configured at construction and reused across every policy on top of it. Avoids both global state and per-call extractor passing.
- `Config.RoleExtractor` is required; `New` panics if nil. Silent passthrough on misconfiguration is unsafe for an authz plugin.
- `RequireRoles()` / `RequireAnyRole()` panic on a zero-length input. "No constraint" should be expressed by omitting the middleware, not registering a no-op.
- Role matching is case-sensitive. Identity providers emit canonical role names; case-insensitive matching would silently mask drift between policy strings and IdP output.
- Authorization-only plugin: rbac never emits 401 or `WWW-Authenticate`. Wire your auth plugin (jwt / bearer / apikey / basicauth / custom) BEFORE rbac so the extractor has an authenticated identity to read. A request that reaches rbac with no identity (extractor returns nil/empty) is rejected with 403, matching RFC 7235's "authenticated but lacks privilege" semantics.
- Response body shape matches aarv's framework `errorResponse` (`{"error":"forbidden","message":"...","request_id":"..."}`). The plugin deliberately does NOT include the missing role names in the response — that would leak the policy surface to unauthorized callers.
- Native + stdlib middleware paths registered via `aarv.RegisterNativeMiddleware`. Both paths emit byte-identical denial responses (status, body bytes, Content-Type) **only when the framework uses ALL of: its default ErrorHandler, default JSON codec / Content-Type, and no response-mutating OnError hook**, enforced by an explicit cross-path test. With any of `WithErrorHandler`, `WithCodec`, or a response-mutating `OnError` installed the two diverge — locked in by a second test that documents the limitation in code so any future plugin-level handler/codec hook will surface as a test diff.
- Stdlib path fails closed when no `*aarv.Context` is reachable (i.e. plugin mounted on plain `net/http` without the framework): the extractor signature requires `*aarv.Context`, and silently admitting would defeat authorization. Documented in the package godoc.
- Stdlib path uses the new `(*Context).BindRequest` core helper to re-bind `c` to the (possibly upstream-mutated) `r` before invoking the extractor. `BindRequest` swaps the entire `*http.Request` on `c` (URL, headers, body, context) — preserving any upstream `r.Clone()` mutations — and attaches the framework marker so downstream `aarv.FromRequest(r)` still works. The previous `SetContext`-only sync only carried `r.Context()` across, silently discarding URL/Header/Body changes that an upstream stdlib middleware (header rewriter, prefix stripper, body decompressor, etc.) might have made.
- Role slices are snapshot-copied at middleware construction; mutating the caller's slice afterward cannot change the policy at request time.

### 6.6 RFC 7807 Problem Details ✅
- [x] Implement `ProblemDetails` struct per RFC 7807: type, title, status, detail, instance — landed as `problem.Details` in `plugins/problem`.
- [x] Add `extensions` map for custom fields — `Details.Extensions map[string]any`, flattened into the top-level JSON object on marshal per RFC 7807 §3.2.
- [x] Create `ValidationProblem` formatter that wraps validation errors in RFC 7807 format — `problem.ValidationProblem(*aarv.ValidationErrors) *Details` exposes the per-field failures under the `errors` extension.
- [x] Configurable: enable/disable RFC 7807 format globally or per-route — globally by installing or omitting `problem.Handler(...)` via `aarv.WithErrorHandler`. Per-route toggling would require invasive changes to the framework's error pipeline (the handler runs after middleware returns and does not know which route was hit), so it is intentionally out of scope; documented in the package godoc with a wrap-and-switch workaround.
- [x] Content-Type: `application/problem+json` — exposed as the `problem.ContentType` constant; written via `c.Blob` so the framework's response pipeline (Blob handles status + content-type) stays the source of truth.
- [x] Unit tests: RFC 7807 compliance, extension fields — 100% statement coverage; tests assert the `application/problem+json` Content-Type, default `type` of `"about:blank"`, omission of zero-valued optional members, extension flattening, standard-members-win-on-collision, AppError mapping (status/title/detail), ValidationErrors mapping (422 + `errors` extension), generic-error masking (no internal message leaks to the wire; `OnInternal` callback receives the unmasked error), `TypeForCode` override, fall-through to `Config.Type`, `Instance` generator, `request_id` extension propagation, and the marshal-failure fallback path.

**Scope decisions made during implementation:**
- Single root-module package (`plugins/problem`), stdlib-only — just JSON marshaling, no third-party deps.
- API shape: `Handler(Config) aarv.ErrorHandler` is the primary entry point; users wire via `aarv.WithErrorHandler(problem.Handler(...))`. `FromError(err)` and `ValidationProblem(errs)` are exposed for callers that need the structured form before serialization (e.g. audit logging, composing higher-level responses).
- `Type` resolution: per-error override via `Config.TypeForCode[appErr.Code()]` → `Config.Type` fallback → `DefaultType` ("about:blank"). Lets teams point at a stable error-class taxonomy URL while keeping per-error specificity.
- Generic errors are masked: the original `err.Error()` is never written to the wire (it may carry DB connection strings, stack traces, etc.); the response carries the static `"Internal server error"` text and the `OnInternal` callback receives the unmasked error for logging/instrumentation.
- `request_id` extension is added automatically when the framework's request-id middleware has populated `c.RequestID()`, matching the framework's default error handler's correlation behavior.
- `MarshalJSON` strips reserved keys (`type`/`title`/`status`/`detail`/`instance`) from `Extensions` during the flatten copy — even when the corresponding standard struct field is the zero value (RFC 7807 §3.2 forbids extensions overriding standard members). An older "non-zero overwrites" design would have let an extension impersonate a standard field whenever the struct field was zero; the strip approach closes that. Guarded by both a non-zero collision test and a zero-fields-impersonator regression test.
- `MarshalJSON` is on a value receiver so `json.Marshal(Details{...})` (value) and `json.Marshal(&Details{...})` (pointer) both flow through the custom marshaler. A pointer-receiver design would silently bypass it for the value form and emit Go field names plus a nested `Extensions` object — a footgun for callers who treat `Details` as a value type. Guarded by an explicit value-marshal test.
- `OnInternal` fires for both non-`AppError` 5xx (the obvious case) AND `*aarv.AppError` 5xx that carry a non-nil `Internal()` (the `aarv.ErrInternal(downstream)` shape) so wrapped downstream failures are not silently swallowed when the caller uses problem+json. Plain `*aarv.AppError` 5xx with no `Internal()` (e.g. `ErrServiceUnavailable("draining")`) does NOT fire — the caller chose that status to communicate state, not to signal a fault. Matches the framework's default-handler `slog.Error` parity for users who switch to problem+json.
- Defense-in-depth: `marshalOrFallback` traps a non-marshalable `Extensions` value (channel, func, complex) and emits a hardcoded minimal `application/problem+json` document so the client always sees a conforming response. Both branches are test-covered via direct calls.
- `Status: 0` is omitted from the wire; the handler always populates a non-zero status before writing.

### 6.7 Session Plugin ✅ (sub-modules deferred)
**Why**: Aarv already has the hard half via `securecookie.go` (HMAC-signed + AES-encrypted cookies). A thin session layer on top rounds out the auth story for cookie-based apps and pairs naturally with the CSRF plugin (10.3) and Basic/Bearer auth plugins.
- [x] Create `plugins/session` package — root module, stdlib-only.
- [x] Define `Store` interface: `Get(id) (*Stored, error)`, `Save(id, *Stored, ttl) error`, `Delete(id) error` — `(nil, nil)` on missing/expired matches `idempotency.Store` convention.
- [x] Implement `CookieStore` — JSON-marshals `Stored` and AES-256-GCM-encrypts it into the cookie value itself; no server state. Wired via separate `NewCookie(CookieConfig)` constructor and intentionally does NOT satisfy the `Store` interface (request/response-scoped, fundamentally different load/save shape — keeping it out of `Store` prevents misuse where a server-side store is required).
- [x] Implement `MemoryStore` — in-process `map[string]*memEntry` with lazy TTL eviction (`Get` evicts expired in-line); optional `NewMemoryStoreWithJanitor(d)` adds a periodic sweep goroutine and returns an idempotent `stop func()` for `app.OnShutdown` wiring. Both `Get` and `Save` deep-copy `Stored.Data` and `Stored.Flash` so concurrent requests cannot share live map instances.
- [x] `Session` type: `Get/Set/Delete(key)`, `Flash(key, value)` (write-side) + `ConsumeFlash(key) (any, bool)` (read-side, removes), `Regenerate()` (new ID, preserves data, queues old ID for `Store.Delete` on save), `Destroy()` (subsequent mutations are no-ops; cookie expired and store entry removed on save). `IsNew()` exposes the load-time "no entry was found" signal for handlers that branch on first-touch.
- [x] Middleware: loads session at request start; the `sessionWriter` wrapper commits before the FIRST `WriteHeader` / `Write` / `Flush` (so cookies land alongside `c.JSON` / `c.Blob` / `c.Text` which write headers inline at handler time, not via `OnSend`); a post-handler `commit()` fallback covers handlers that returned without writing. The `c.SetResponse(orig)` restore is `defer`-guarded around `next(c)` so panic / recovery paths cannot leak the wrapper. Persistence predicate is `dirty || len(consumed) > 0 || regenerated || destroyed` — a newly-issued session that the handler never wrote to is NOT saved (clean reads emit zero `Set-Cookie` and incur zero `Store.Save`). Wrapper implements `Unwrap() http.ResponseWriter` (matches `idempotency` / `prometheus` / `otel` convention) so `http.ResponseController` walks through to the underlying Flusher / Hijacker.
- [x] Helper: `session.From(c) (*Session, bool)` and `session.MustFrom(c) *Session` (panics if middleware did not run for this route — surfaces missing wiring during development rather than as a confusing nil-deref later).
- [x] Configurable: `CookieName`, `MaxAge` (zero → `DefaultMaxAge` 24h; negative or `SessionMaxAge` sentinel → browser-session cookie with no Max-Age / Expires attributes; server-side TTL still uses `DefaultMaxAge` to bound retention), `DisableSecure` / `DisableHTTPOnly` (bool fields invert so secure-by-default is the zero value, mirroring the framework's `aarv.CookieOptions.DisableHttpOnly` precedent), `CookieSameSite` (zero → Lax), `CookieDomain`, `CookiePath` (zero → "/"). Two-tier error handling: `ErrorHandler func(*aarv.Context, error) error` for load-time failures (runs BEFORE the handler so it can produce a controlled response) and `SaveErrorHandler func(*aarv.Context, error)` for commit-time failures (side-effect only — by the time it fires, headers may already be flushed). Default save-error path logs via the request-scoped `c.Logger()` (or `Config.Logger` / `slog.Default()` when `c == nil` on stdlib mounts) and swallows.
- [x] CSRF token integration: `Session.CSRFToken()` lazy-issues a 32-byte base64url token, marks the session dirty so it persists, and returns the same token on subsequent calls within the request and across save/load round-trips. `Stored.CSRF` is the only home for the token; `_csrf` is NOT a reserved user key (`Get("_csrf")` returns `(nil, false)`). The CSRF plugin (10.3) will read it via `session.From(c).CSRFToken()` for session-bound tokens.
- [ ] Optional: `plugins/session-redis/go.mod` for distributed sessions — deferred to follow-up task; the `Store` interface and `MemoryStore` are designed so an external backend drops in without middleware changes.
- [ ] Optional: `plugins/session-sql/go.mod` for SQL-backed sessions — deferred to follow-up task; same drop-in shape as session-redis.
- [x] Unit tests: CookieStore round-trip (data + flash + CSRF survive encrypt/decrypt cycle), MemoryStore lazy + janitor eviction, Regenerate (new ID, data preserved, multi-call collapses to single old-ID delete, store actually deletes oldID end-to-end), flash lifecycle (write → consume on next request → gone on the one after), concurrent access (`-race` with N writers + readers against a single key, plus deep-copy guards on both `Save` and `Get` boundaries).
- [x] Security tests: tampered cookie rejected (CookieStore returns fresh session, never raises load error), expired session rejected (forced via store delete; handler observes `IsNew()`), Regenerate clears old ID from store (instrumented store call counts), default-secure cookie attributes (`Secure`, `HttpOnly`, `SameSite=Lax`), `sessionWriter.Unwrap` participates in `http.NewResponseController` chain, SSE-style `Flush` commits cookie before mid-stream chunks reach the client.

**Scope decisions made during implementation:**
- `CookieStore` does NOT implement the public `Store` interface. A stateless cookie backend cannot cleanly satisfy `Save(id, stored, ttl)` without response/writer context — treating ciphertext as an "id" leaks backend-specific semantics into the generic store contract. Internal `sessionBackend` interface unifies both via `(c, r, cfg)` and `(c, w, sess, cfg)` shapes that work whether `c == nil` (stdlib mount) or not. `NewCookie(CookieConfig)` is the dedicated constructor.
- Cookie commit timing is wrapper-driven, not hook-driven. `OnSend` only buffers when hooks are registered (dispatch.go path), so piggybacking on it would impose a runtime cost on every other route. The `sessionWriter` wrapper's `WriteHeader` / `Write` / `Flush` interception is the only path that lands cookies before `c.JSON` / `c.Blob` / `c.Text` flush headers inline.
- Lazy persistence: `new` alone does NOT trigger a save. Only `dirty || len(consumed) > 0 || regenerated || destroyed` does. A health-check route that calls `From(c)` but never mutates emits zero `Set-Cookie` and incurs zero `Store.Save` — verified with instrumented store call counts.
- `MaxAge` representation: `time.Duration` zero → default (24h), negative → browser-session cookie. The split avoids overloading "0" to mean both "unset" and "browser-session" simultaneously. `SessionMaxAge` constant exposes the negative-sentinel value as a self-documenting name.
- `Secure` / `HttpOnly` use `Disable*` fields (bool, default false) so the safe defaults are the zero-value config. Pointer-to-bool would also work but introduces nil checks throughout the hot path; the existing framework `aarv.CookieOptions.DisableHttpOnly` (securecookie.go:71) sets the pattern.
- CookieStore implements its own AES-256-GCM + JSON envelope rather than calling the framework's `(*Context).SetEncryptedCookie` so it works on stdlib mounts where `c == nil` and so the plugin is insulated from future framework cookie-format changes. Wire format: `base64url(nonce || ciphertext || tag)` over `{"t": unixSec, "s": Stored}`. Server-side expiry checks the embedded `t`, independent of the client-spoofable HTTP `Max-Age`. 3.5 KiB encoded-payload guard at Save time stays safely under the ~4 KiB browser cookie limit; oversize surfaces as a `SaveErrorHandler` call carrying `ErrCookiePayloadTooLarge`.
- CSRF storage is single-source on `Stored.CSRF` rather than dual-stored on both `Session.csrf` and `Data["_csrf"]`. Reasoning: `_csrf` should not be a reserved user-visible key; handler `Get("_csrf")` returning `(nil, false)` is the cleanest contract for the user-data namespace. Round-trip parity is verified by an explicit save/load test.

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
- [x] Integration test: plugin registers routes + hooks + middleware in isolation
- [x] Integration test: nested plugins, decorator resolution

---

## Phase 8: Codec Sub-Packages (M8) ✅ COMPLETE

- [x] Create `codec/segmentio/go.mod` — separate module
- [x] Implement `segmentio.New()` returning `Codec`
- [x] Create `codec/sonic/go.mod` — separate module
- [x] Implement `sonic.New()` and `sonic.NewFastest()` returning `Codec`
- [x] Implement `sonic.Pretouch(types ...any)` for JIT warmup
- [x] Create `codec/jsonv2/go.mod` — separate module
- [x] Implement `jsonv2.New()` returning `Codec`
- [x] Benchmark suite: all codecs, marshal/unmarshal, various payload sizes
- [x] README for each sub-package with usage example

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

## Phase 10: Security Plugins (M10) ✅ COMPLETE

### 10.1 Rate Limiter ✅
- [x] Token bucket algorithm, zero-dep, sharded mutex (`plugins/ratelimit/store.go`)
- [x] Sliding window variant for smoother rate limiting (`plugins/ratelimit/algo_sliding_window.go`)
- [x] Per-IP keying via `c.RealIP()` (default `KeyFunc`)
- [x] Custom key function: `KeyFunc func(*aarv.Context) string`
- [x] `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` headers, plus `Retry-After` on 429
- [x] Configurable response on limit: `StatusCode`, `Message`, custom `LimitHandler`
- [x] Skip paths configuration: `SkipPaths []string` + `Skipper func(*aarv.Context) bool` (OR-combined)
- [x] Burst allowance configuration (`Config.Burst`, default = `Limit`)
- [x] **Shipped later in §12.6.4**: `plugins/ratelimit-redis/go.mod` for distributed rate limiting via Redis; tagged through `plugins/ratelimit-redis/v0.9.0`
- [x] Unit tests: within limit, exceed limit, custom key, headers, skip paths, skipper, burst, sliding-window rollover, lazy sweep eviction, `NewWithCleanup` goroutine lifecycle, race
- [x] **Scope additions**: `New(cfg)` starts no goroutines; cleanup is in-line via deterministic `atomic.Uint64` sweep counter. `NewWithCleanup(cfg) (aarv.Middleware, func() error)` starts a periodic janitor and returns a stop function for `app.OnShutdown` wiring.

### 10.2 Throttle ✅
- [x] Max concurrent in-flight requests via `chan struct{}`
- [x] Configurable: `MaxConcurrent`, `QueueSize`, `QueueTimeout`
- [x] Return 503 Service Unavailable when queue full or queue timeout (configurable status/message/handler)
- [x] Unit tests: under limit, at limit, queue timeout, queue full, slot release on handler error and on panic, skip paths, skipper, race
- [x] **Scope addition**: queue token release decoupled from slot release — queue depth bounded at exactly `QueueSize` regardless of handler latency.

### 10.3 CSRF Protection ✅
- [x] Double-submit cookie pattern
- [x] Token generation via `crypto/rand`, base64-RawURL encoded; `crypto/subtle.ConstantTimeCompare` over decoded bytes
- [x] `X-CSRF-Token` header validation; optional `FormField` fallback for non-AJAX form posts
- [x] Skip safe methods (GET, HEAD, OPTIONS, TRACE by default; nil-vs-empty contract)
- [x] Configurable: `CookieName`, `HeaderName`, `TokenLength` (panics on < 16 bytes), cookie path/domain/MaxAge/Secure/HttpOnly/SameSite
- [x] `Token(c)` helper for server-rendered template injection
- [x] Unit tests: valid token, invalid token, missing token, safe methods, custom names, FormField, custom ErrorHandler, HttpOnly+Token(c), `SafeMethods` nil/empty/custom, race
- [x] **Scope addition**: `SafeMethods` nil-vs-empty semantics; `[]string{}` makes every method require a token.

### 10.4 IP Filter ✅
- [x] Allowlist mode — only allow listed CIDRs (`ModeAllowlist`)
- [x] Denylist mode — block listed CIDRs (`ModeDenylist`)
- [x] `net.ParseCIDR` based matching; bare IPs auto-converted to /32 or /128
- [x] Configurable: custom `ErrorHandler`, `Skipper`, `SkipPaths`, `IPFunc` for proxy fronts
- [x] Unit tests: allow exact / CIDR (IPv4 + IPv6), block, custom IPFunc, Skipper, SkipPaths, panic on invalid CIDR, panic on empty allowlist, fail-closed/fail-open on unparseable source, defensive copy of CIDRs
- [x] **Scope addition**: invalid CIDRs panic in `New` (parity with jwt). Empty allowlist panics. Unparseable source IP fails closed in allowlist mode and fails open in denylist mode.

### 10.5 Request Sanitizer ✅
- [x] Strip HTML tags from string fields (stdlib state-machine; decodes `&amp;`, `&lt;`, `&gt;`, `&quot;`, `&#39;`, `&apos;`, `&nbsp;`)
- [x] Normalize Unicode (NFC) via `golang.org/x/text/unicode/norm`
- [x] Configurable: `Fields` allowlist, `SkipFields` blocklist, `Custom []SanitizerFunc`, `MaxBodyBytes`, `ContentTypes`, `Skipper`, `SkipPaths`
- [x] Unit tests: HTML stripping, nested objects/arrays, allowlist, blocklist, custom ordering, NFC normalization, invalid JSON passthrough, MaxBodyBytes 413, content-type filter, skipper, pool reuse independence
- [x] **Scope decision**: separate submodule (`plugins/sanitize/go.mod`) because NFC requires `golang.org/x/text` and the root module is strict zero-dep. Joins the prometheus/otel release dance.

### 10.6 Idempotency Plugin ✅
- [x] Read `Idempotency-Key` header (configurable name); pass through if absent (`RequireKey: true` returns 400 instead)
- [x] Define `Store` interface: `Lock`, `Unlock`, `Get`, `Save`. Plus optional `WaitableStore` extension with `Wait(ctx, key) (*Response, error)`.
- [x] Implement `MemoryStore` (zero-dep, default). Lazy TTL eviction by default; `NewMemoryStoreWithJanitor(sweep)` for explicit goroutine + stop function.
- [x] First request: lock, execute handler, capture status + headers + body via overflow-aware `captureWriter`, persist, return
- [x] Subsequent requests with same key: return cached response verbatim with `Idempotency-Replayed: true` header; hop-by-hop headers stripped on persistence
- [x] Concurrent requests with same key: 409 (`ConflictReject`, default) or replay after wait (`ConflictWait`, requires `WaitableStore`; non-waitable stores fall back to immediate 409 — no polling)
- [x] TTL configuration (default 24h); MaxResponseBytes cap (default 4 MiB) with overflow state machine that streams over-cap responses through unchanged
- [x] `SafeMethods` (default GET, HEAD, OPTIONS via nil); nil-vs-empty contract documented
- [x] `CacheStatuses` (default 2xx + 3xx via nil); nil-vs-empty contract documented
- [x] Hash request body and reject reuse of key with different payload (`HashRequestBody: true` → 422 on mismatch, per IETF draft)
- [x] **Shipped later in §12.6.6**: `plugins/idempotency-redis/go.mod` for distributed setups; tagged through `plugins/idempotency-redis/v0.8.0`
- [x] Unit tests: first/replay, concurrent same-key (50-goroutine race), `ConflictWait` with `MemoryStore`, `ConflictWait` with non-waitable store falls back to 409, payload mismatch → 422, TTL expiry, safe-method bypass (nil/empty/custom), `CacheStatuses` (nil/empty/custom), absent key, `RequireKey`, over-cap response, native/stdlib parity, custom ErrorHandler, ctx cancellation in `Wait`

---

## Phase 11: Observability Plugins (M11) ✅ COMPLETE

### 11.0 Core enablement (PR0) ✅
Prerequisite work in the root module to unblock cardinality control on metrics labels and span names.
- [x] `(*Context).RoutePattern() string` — returns the matched aarv route pattern (path-only, e.g. `/users/{id}`); empty for 404, 405, `App.Mount` handlers, and any path outside the registered aarv route table. Set by the dispatcher at every successful match site (direct and grouped, exact and dynamic, fast path and `routingMux` fallback).
- [x] `(*Context).SetLogger(*slog.Logger)` — swaps the request-scoped logger for the request lifetime; `nil` clears any previous override.
- [x] `patternStr` field added to `directDynamicRoute` / `directDynamicHTTPRoute`; `stripMethodPattern` helper for `mux.Handler(r)` results.
- [x] CI workflow split into root job (Go 1.22/1.23 matrix) and dedicated `test-plugin-submodules` job pinned to Go 1.25.
- [x] Unit tests: exact, dynamic, grouped dynamic, catch-all, route-level middleware pre-`next`, global middleware post-`next`, 404/405/Mount produce empty, pool-reuse independence, `SetLogger` override / nil-clear / no-leak across requests.

### 11.1 Prometheus Plugin ✅
- [x] Create `plugins/prometheus/go.mod` — separate module (`go 1.23.0` — forced by `client_golang` dep tree)
- [x] Request counter (method, path, status)
- [x] Request duration histogram
- [x] In-flight requests gauge
- [x] Response size histogram
- [x] Configurable: buckets, path grouping/normalization via `GroupPath func(c *aarv.Context) string` (default consults `RoutePattern()` — no heuristic ID collapsing)
- [x] Custom collectors registration
- [x] Unit tests: metric collection, path grouping, cardinality bound under concurrent load
- [x] **Scope changes from original spec**:
  - `Handler` is registered as a regular `app.Get("/metrics", …)` route, not via `App.Mount` (which adds a trailing slash and 307-redirects). The original spec mentioned a `Plugin()` auto-mounter — dropped because cardinality / auth / rate-limit gating decisions belong on the user side.
  - `recordingWriter` implements `Unwrap` so `http.ResponseController` can reach the underlying writer for streaming, hijacking, or HTTP/2 push.

### 11.2 OpenTelemetry Plugin ✅
- [x] Create `plugins/otel/go.mod` — separate module (`go 1.25.0` — forced by `go.opentelemetry.io/otel/sdk` dep tree)
- [x] Span creation per request with configurable span naming
- [x] W3C Trace Context **extraction** (traceparent, tracestate headers). Outbound injection is the application's responsibility (e.g. via `otelhttp.NewTransport`) — the plugin does not call `Propagator.Inject`.
- [x] Attribute injection: method, target (route pattern when known, else path), status, user_agent, request_id, net.peer.ip
- [x] Error recording via 5xx → span Status Error mapping (matches OTel HTTP semconv recommendation; aarv's framework swallows handler errors before middleware sees them, so explicit `span.RecordError(handlerErr)` from the public middleware path is not possible)
- [x] Metrics: `http.server.request.count`, `http.server.request.duration_seconds`, `http.server.request.size_bytes`, `http.server.response.size_bytes` via the user-supplied MeterProvider
- [x] Log correlation: replaces `aarv.Context.Logger()` for the request lifetime with one carrying `trace_id` and `span_id`; original logger restored on handler return
- [x] Baggage propagation support (default Propagator is the global one, typically `TraceContext + Baggage` composite)
- [x] **Bring-your-own Provider** instead of bundled exporters/sampling. Original spec called for "exporter (OTLP, Jaeger, Zipkin), sampling rate" knobs — these belong on the user's `TracerProvider` Resource, so we don't pull exporter deps and don't expose redundant Config fields.
- [x] Unit tests: span creation, context propagation, attribute injection, custom SpanNameFunc honored verbatim (not overwritten by route-pattern rename), 5xx → span Error, metrics emitted, log correlation, baggage extracted
- [x] **Scope changes from original spec**:
  - Inverted booleans (`SuppressErrorStatus`, `SuppressMetrics`, `SuppressLogAttrs`) so zero-value `Config{}` produces all default behaviors.
  - `recordingWriter` implements `Unwrap` for `http.ResponseController` compatibility.
  - The default `defaultSpanName` produces `<METHOD> <Path>` at dispatch time; `finalizeSpan` upgrades it to `<METHOD> <RoutePattern>` only when the default namer was used. A caller-supplied `SpanNameFunc` is honored verbatim.

### 11.3 Pprof Plugin ✅
- [x] Mount `net/http/pprof` at configurable prefix (stdlib, no external deps; lives in root module under `plugins/pprof`)
- [x] Optional: `Config.AuthMiddleware` for pprof endpoints
- [x] Unit tests: endpoint availability for all five canonical sub-routes (cmdline, profile, symbol, trace, index), `App.Mount` integration with prefix-restoration, custom prefix support, `AuthMiddleware` blocks unauthenticated, `SkipPaths` exclusion
- [x] **Scope additions**:
  - `Handler(cfg) http.Handler` (canonical, mountable via `App.Mount`) and `New(cfg) aarv.Middleware` (chain-style) both exposed. `New` registers a native middleware pair via `aarv.RegisterNativeMiddleware`; `Handler` returns a plain `http.Handler` (no native pair — debugging endpoints don't need the fast path).
  - `Handler` restores `cfg.Prefix` on `App.Mount`-stripped paths so the inner mux's registered routes match and `pprof.Index`'s hardcoded `/debug/pprof/` prefix check sees the expected URL shape.

### 11.4 Body Dump Sink (verboselog enhancement) ✅
**Why**: `verboselog` already tees request and response bodies but hardcoded the destination to `slog`. Users who need to deliver captured bytes to an audit database, object store, message queue, or fixture recorder no longer have to fork the plugin.
- [x] Add `DumpMeta` struct exposing already-computed metadata: status, latency, request ID, method, path, client IP, user-agent, content type, redacted request/response header maps, query params
- [x] Add `Sink func(c *aarv.Context, reqBody, respBody []byte, meta DumpMeta)` field to `Config`
- [x] Add `SuppressSlog bool` field (default `false` = log to slog) — inverted from the original `LogToSlog bool (default true)` plan so existing zero-value `Config{...}` constructions keep their pre-Sink behavior unchanged. Setting `SuppressSlog: true` with `Sink: nil` panics in `New` (no-op middleware misconfig).
- [x] Sink receives bytes *after* truncation and redaction (consistent with what slog sees) — documented; users wanting raw bytes should set `RedactSensitive: false` and `MaxBodySize` high
- [x] Sink invocation is panic-safe via `defer recover()`; panics are logged through slog at error level
- [x] Sink is invoked synchronously after handler completes; documented that long-running sinks should hand off to a goroutine/queue themselves
- [x] Wired into both the `net/http` middleware path and the native `aarv.HandlerFunc` path
- [x] Updated package doc comment to describe audit/archive use case alongside the existing logging use case
- [x] Unit tests: sink receives correct bytes, sink receives correct meta, sink panic is recovered, `SuppressSlog: true` suppresses slog output, sink + slog both fire when both enabled, sink not invoked for skipped paths, pool-reuse independence (mutate sink-received slice → next request unaffected)
- [x] Example: `examples/verboselog-audit` showing a sink that appends to an in-memory audit log

### Phase 11 release status
- All Phase 11 work landed in the `[Unreleased]` section of `CHANGELOG.md`. To be rolled into a `v0.6.0` minor release.
- At release time, lift the `replace github.com/nilshah80/aarv => ../..` directives in `plugins/prometheus/go.mod` and `plugins/otel/go.mod`, bump their `require` lines to the published `v0.6.0`, run `go mod tidy` inside each, verify tests pass without the replace, then tag `v0.6.0` (root) + `plugins/prometheus/v0.6.0` + `plugins/otel/v0.6.0`.

---

## Phase 12: TLS Helpers (M12) ✅ COMPLETE

- [x] Create `plugins/autocert/go.mod` — separate module
- [x] Implement Let's-Encrypt-style TLS via `autocert.Manager` (`autocert.Listen` + `autocert.ListenWithManager` for shared-manager flows; runs through `app.ListenServer` so the lifecycle stays uniform)
- [x] HTTP→HTTPS redirect handler (`autocert.RedirectHandler` / `RedirectServer` / `ListenRedirect`; `ACMEChallengeHandler` interface for HTTP-01; conservative slowloris-resistant defaults; bare-IPv6 bracketing; control-char rejection; default port stripping)
- [x] Certificate cache directory configuration (`Config.CacheDir` with `os.UserCacheDir` fallback to `os.TempDir`; created with `0700` best-effort)
- [x] Implement cert file watcher for hot-reload (root-module `WithCertReload` + `CertReloader`; fsnotify-free, `os.Stat (ModTime, Size)` polling; one-shot lifecycle; conflict detection with caller `TLSConfig.GetCertificate`; malformed-reload preservation)
- [x] h2c plugin for HTTP/2 cleartext (internal mesh) — `plugins/h2c` with `Wrap` / `Listen`; `MaxFirstRequestBytes` bound on the upstream library's first-request memory exposure; RFC 7540 frame-size validation
- [x] Document recommended TLS config — `docs/tls.md` (deliberately scoped to "hardened defaults" rather than regulatory-compliance claims; covers `WithCertReload` / mTLS / HSTS placement / autocert / h2c threat model / OCSP non-claim / lifecycle order)

### Phase 12 release status
- All Phase 12 work landed in the `[Unreleased]` → `[0.7.5]` block of `CHANGELOG.md`. Same release dance as Phase 11: lift `replace github.com/nilshah80/aarv => ../..` directives in `plugins/autocert/go.mod` and `plugins/h2c/go.mod`, bump their `require` lines to the published `v0.7.5`, run `go mod tidy` inside each, verify tests pass without the replace, then tag `v0.7.5` (root) + `plugins/autocert/v0.7.5` + `plugins/h2c/v0.7.5`.

---

## Phase 12.5: OpenAPI / Swagger Generator (M12.5) ✅ COMPLETE

### 12.5.1 OpenAPI Core
- [x] Define `OpenAPIConfig` struct: title, version, description, servers, contact, license — implemented as `openapi.Config` with `Title`, `Version`, `Description`, `Servers`, `Contact`, `License`, plus `Include` / `Exclude` filtering, `JSONPath` / `YAMLPath`, `DisableJSONEndpoint` / `DisableYAMLEndpoint`, `SecuritySchemes`
- [x] Implement route introspection: collect all registered routes with metadata — extends `RouteInfo` with `Summary`, `OperationID`, `RequestType`, `ResponseType`, `Responses`, `RequestContentType`; `App.Routes()` deep-copies for safety
- [x] Auto-generate path parameters from `{param}` patterns (and catch-all `{name...}` normalized to `{name}`)
- [x] Auto-generate request body schema from `Bind[Req, Res]` type parameters — via new `aarv.BindRoute` / `aarv.BindGroupRoute` generic helpers + `WithSchema` / `WithSchemaTypes`
- [x] Auto-generate response schema from handler return types — same path
- [x] Extract validation rules from `validate:""` tags → OpenAPI constraints — `required`, `min`/`max`/`gte`/`lte`/`gt`/`lt`/`len` (numeric/string/container-aware), `oneof` → enum, `email` / `url` / `uuid` → format, `regex` → pattern, `unique` → uniqueItems; unknown rules ignored with `slog.Debug`. `validate:"required"` overrides JSON `omitempty`.
- [x] Implement `/openapi.json` endpoint handler
- [x] Implement `/openapi.yaml` endpoint handler — via `sigs.k8s.io/yaml.JSONToYAML`
- [x] Swagger UI integration via embedded static files — `plugins/openapi-ui` ships real upstream Swagger UI v5.17.14 (Apache 2.0)
- [x] ReDoc integration — same plugin, ships real upstream ReDoc v2.1.5 (MIT)
- [x] Configurable: paths to include/exclude, security schemes — `Config.Include` / `Exclude` (Include is sole filter when set; default `Exclude` plus auto-added custom `JSONPath` / `YAMLPath` so the spec does not document its own endpoints), `Config.SecuritySchemes` populates `components.securitySchemes`
- [x] Unit tests: schema generation, constraint mapping, endpoint output — 100% coverage on `plugins/openapi` and `plugins/openapi-ui`

### Beyond the original spec
- OpenAPI 3.1 / JSON Schema 2020-12 nullable encoding (`type: ["X","null"]` union, `oneOf` for `$ref`); the deprecated 3.0 `nullable: true` keyword is never emitted
- Component dedup keyed by `reflect.Type` identity; recursive types terminate via component-placeholder pattern
- Component naming with sanitized `pkgpath_TypeName` collision handling and numeric-suffix tiebreak
- `App.CodecContentType()` flows into request and response media types so a non-JSON codec (e.g. YAML) is reflected in the spec without per-route overrides
- `docs/openapi.md` reference (metadata sources, validate-tag mapping, required-field precedence, components, nullable encoding, catch-all, security schemes, non-goals)
- `examples/openapi-spec` runnable end-to-end demo (smoke-tested)

### Phase 12.5 release status
- Bundled with Phase 12 in `[0.7.5]`. Tag `plugins/openapi/v0.7.5` and `plugins/openapi-ui/v0.7.5` after the root tag, following the same `replace`-lift-then-tag dance.

---

## Phase 12.6: ALP-Driven Plugin Work (M12.6)

> **Why now:** ALP has completed Track 1 on `aarv@v0.7.0` and identified the reusable framework pieces needed for Tracks 3, 4, and 6. Current Aarv already has `ratelimit`, `idempotency`, `pprof`, `openapi`, `openapi-ui`, `h2c`, TLS helpers, and the observability plugins; the remaining Aarv work is narrower: signed-request auth, Redis-backed stores, and a few idempotency hardening refinements.

### 12.6.0 Intake and release boundary
- [x] Confirm ALP's current dependency picture before starting: root `github.com/nilshah80/aarv@v0.7.0`, `plugins/otel@v0.7.0`, `plugins/prometheus@v0.7.0`
- [x] Document that ALP can consume `v0.7.5` separately for `BindRoute`, `plugins/openapi`, `plugins/openapi-ui`, TLS helpers, and h2c; those are already built and are not part of this phase
- [x] Keep root module zero-dependency: Redis-backed companions live in separate modules and must not pull `go-redis` into the root module
- [x] Use `v0.7.6` for this ALP-driven work; bump to `v0.8.0` only if an idempotency refinement becomes a breaking config/API change
- [x] Add release notes to `CHANGELOG.md` under `[Unreleased]` as each item lands
- [x] Before tagging, lift local `replace github.com/nilshah80/aarv => ../..` directives in every new plugin submodule, bump `require` lines to the published root tag, run `go mod tidy`, and test without local replace directives

### 12.6.1 `plugins/hmacauth` — signed request authentication
- [x] Create `plugins/hmacauth` package in the root module; stdlib-only
- [x] Package doc defines the canonical request byte sequence exactly:
  ```
  METHOD\n
  PATH\n
  CANONICAL_QUERY\n
  HEX(SHA256(body))\n
  TIMESTAMP\n
  NONCE
  ```
- [x] Configurable header names with defaults: `X-Client-Id`, `X-Timestamp`, `X-Nonce`, `X-Signature`
- [x] Reject missing/malformed auth headers with generic `401` responses; never reveal which header or computed signature failed
- [x] Parse timestamp as Unix seconds in `int64`; reject malformed, negative, zero, and absurdly large values
- [x] Enforce configurable clock skew window; default `SkewSeconds = 300`
- [x] Define `Client` with `ClientID`, `Secret []byte`, `Secrets [][]byte`, and caller-owned `Identity any`
- [x] Define validator shape for client lookup, matching existing auth plugin style: `type Validator func(clientID string) (Client, error)`
- [x] Treat unknown clients and nil/empty secrets as authentication failure
- [x] Implement `StaticClients(map[string]Client) Validator`
- [x] Document that HMAC secrets must remain plaintext in memory because verification needs the secret bytes; never hash or log them
- [x] Read the request body for hashing with a configurable cap; re-inject the body via `c.SetBody(...)` so downstream `aarv.Bind` still works
- [x] Ensure `hmacauth` does not bypass `plugins/bodylimit`; document recommended middleware order: `requestid -> recover/recovery -> bodylimit -> hmacauth -> handler`
- [x] Implement canonical query encoding: sort keys ASCII-ascending, sort repeated values, percent-encode per RFC 3986 unreserved set, and do not use `url.QueryEscape`
- [x] Compute HMAC-SHA256 over the canonical request
- [x] Compare expected and received signatures with `crypto/subtle.ConstantTimeCompare`
- [x] Support secret rotation by checking all configured candidate secrets and accumulating the match result without short-circuiting on the first match
- [x] Define `NonceStore` interface: `SetNX(ctx context.Context, key string, ttl time.Duration) (fresh bool, err error)`
- [x] Add replay protection via `NonceStore.SetNX(ctx, "nonce:"+clientID+":"+nonce, NonceTTL)`
- [x] Default `NonceTTL = 2*SkewSeconds + 60s`; reject `SkewSeconds <= 0` and `NonceTTL <= 0` at construction
- [x] If `NonceStore` is nil, allow requests but emit a one-time warning that replay protection is disabled
- [x] Implement `MemoryNonceStore` for dev/tests with TTL expiry and a bounded max-entry cap
- [x] Provide a stop/cleanup path for `MemoryNonceStore` if it starts any goroutine; document `OnShutdown` wiring
- [x] Store authenticated client data in the request context and expose `hmacauth.From(c) (Client, bool)` and `hmacauth.FromContext(ctx) (Client, bool)`
- [x] Provide custom error handler hook for callers that need non-default response bodies
- [x] Unit tests: valid signature, missing each auth header, malformed timestamp, skew past/future/boundary, unknown client, bad signature, malformed signature hex, empty body, unicode body, large body at cap, query canonicalization edge cases, replay rejection, nil-store warning, per-client nonce isolation
- [x] Unit tests: rotation accepts old+new while both active, rejects old after removal, and does not short-circuit candidate secret checks
- [x] Fuzz test canonical query encoding for determinism and panic-freedom
- [x] Run race tests for `MemoryNonceStore`

### 12.6.2 `plugins/hmacauth` test vectors and client signer
- [x] Add `plugins/hmacauth/testdata/vectors.json`
- [x] Vector schema: `description`, `client_id`, `secret_hex`, `method`, `path`, `query`, `body_b64`, `timestamp`, `nonce`, `expected_signature_hex`
- [x] Cover vectors for empty body, ASCII body, UTF-8 body, binary body, single and repeated query params, path params, long path, long body, SHA-256 block boundary, and GET/POST/PATCH/DELETE methods
- [x] Expose `Vectors() []Vector` so ALP's internal client and other implementations can verify against the same data
- [x] Implement `Sign(req *http.Request, client Client, body []byte, now func() time.Time, nonce string) error`
- [x] Implement `Transport(client Client, opts ...TransportOption) http.RoundTripper`
- [x] Transport options: deterministic clock for tests, nonce source, body clone strategy, and redirect behavior
- [x] Default nonce source uses `crypto/rand` and 16 random bytes hex-encoded
- [x] Signer and verifier round-trip every JSON vector byte-for-byte
- [x] Document redirect behavior explicitly: either re-sign redirected requests with the new URL or fail cleanly

### 12.6.3 `plugins/hmacauth-redis` — Redis nonce store
- [x] Create `plugins/hmacauth-redis/go.mod` as a separate module; package name `hmacauthredis`
- [x] Depend on `github.com/redis/go-redis/v9` without adding Redis to the root module
- [x] Implement `RedisNonceStore` satisfying `hmacauth.NonceStore`
- [x] Use atomic Redis `SET key value NX EX ttl` semantics; return `fresh=false` on duplicate nonce
- [x] Prefix keys in caller-visible config, defaulting to `aarv:hmacauth:nonce:`
- [x] Preserve caller context cancellation and deadlines
- [x] Unit tests with a fake/miniredis-style Redis if practical; otherwise integration tests gated behind env vars
- [x] Tests: first nonce accepted, duplicate rejected, expiry allows reuse, Redis error propagates, context cancellation propagates
- [x] README with ALP-style middleware wiring example

### 12.6.4 `plugins/ratelimit-redis` — distributed rate limiting
- [x] Create `plugins/ratelimit-redis/go.mod` as a separate module; package name `ratelimitredis`
- [x] Reuse the public `plugins/ratelimit` config concepts where possible: `Limit`, `Window`, `Burst`, `KeyFunc`, `SkipPaths`, `Skipper`, headers, custom limit handler
- [x] Decide whether to extract a small backend interface from core `plugins/ratelimit` or keep Redis implementation as a parallel middleware with compatible config; avoid breaking existing `ratelimit.New(cfg)` callers
- [x] Implement Redis-backed token bucket with atomic Lua script for read/refill/decrement/reset calculation
- [x] Set `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` on admitted and denied requests
- [x] Set `Retry-After` on `429`
- [x] Support custom key functions; ALP can pass `hmacauth.From(c).ClientID` with `c.RealIP()` fallback
- [x] Support skip paths for `/health`, `/ready`, `/live`, `/metrics`, and `/debug/*`
- [x] Add configurable key prefix, defaulting to `aarv:ratelimit:`
- [x] Define fail-open/fail-closed behavior on Redis errors; default fail-closed for security-sensitive APIs unless there is a strong reason otherwise
- [x] Tests: within limit, exceed limit, burst, refill timing, custom key func, skip paths, Redis error policy, context cancellation, concurrent callers against same key
- [x] k6 or Go benchmark note showing the Redis variant is suitable for ALP's multi-instance Track 3 usage
- [x] README with ALP-style wiring after HMAC auth

### 12.6.5 `plugins/idempotency` refinements for ALP
- [x] Add `CachedHeaders []string` config; default allowlist: `Content-Type`, `Content-Encoding`, `Cache-Control`, `Location`, `ETag`
- [x] Ensure persisted/replayed responses never include `Set-Cookie`, `Authorization`, `WWW-Authenticate`, `X-Request-Id`, hop-by-hop headers, or other per-request security headers unless explicitly and safely allowed
- [x] Keep `Idempotency-Replayed: true` replay marker; make header name configurable only if there is a concrete consumer need
- [x] Review current `HashRequestBody` behavior against ALP's requirement: same key + different body returns `422 idempotency_key_reused_with_different_payload`
- [x] Add configurable protected methods directly, or document current `SafeMethods` nil-vs-empty settings needed for ALP's default protected set of `POST` and `PATCH`
- [x] Add `CacheStatusFunc func(status int) bool` or equivalent policy hook for non-default cache behavior
- [x] Support ALP policy: cache deterministic 4xx responses; do not cache 5xx responses
- [x] Decide whether per-route TTL belongs in Aarv route metadata; if yes, add `WithRouteIdempotencyTTL(d time.Duration)` without affecting non-idempotency users
- [x] Document canonical middleware order when HMAC and idempotency are both enabled: `requestid -> recovery -> hmacauth -> idempotency -> handler`
- [x] Tests: header allowlist replay, `Set-Cookie` not replayed, `X-Request-Id` not replayed, payload mismatch returns 422, default 5xx not cached, configurable 4xx caching, protected methods match ALP configuration, concurrent same-key reject/wait modes
- [x] Confirm native and stdlib middleware paths have identical behavior after refinements

### 12.6.6 `plugins/idempotency-redis` — distributed idempotency store
- [x] Create `plugins/idempotency-redis/go.mod` as a separate module; package name `idempotencyredis`
- [x] Depend on `github.com/redis/go-redis/v9` without adding Redis to the root module
- [x] Implement `idempotency.Store`; implement `idempotency.WaitableStore` if Redis pub/sub, blocking pop, polling-free wait, or another clean mechanism is selected
- [x] Store lock and cached response separately so a failed handler does not leave a false cached response
- [x] Use atomic lock acquisition with TTL; avoid permanent in-flight locks after process death
- [x] Serialize `idempotency.Response` including status, allowed headers, body, and request body hash
- [x] Preserve TTL exactly enough for ALP retry semantics
- [x] Support configurable key prefix, defaulting to `aarv:idempotency:`
- [x] Tests: first request lock/save/replay, duplicate while in flight, TTL expiry, payload mismatch, Redis outage, context cancellation, malformed cached payload handling
- [x] README with ALP `POST /v1/links` wiring example

### 12.6.7 ALP observability feedback loop — ALP Track 6 audit shipped
- [x] After ALP Track 6 RED audit, review whether `plugins/prometheus` needs custom default buckets for sub-ms redirect paths — shipped as `SubMillisecondBuckets` preset (see §12.6.9)
- [ ] After ALP Track 6 RED audit, review whether `plugins/prometheus` needs safe optional labels such as authenticated client class; avoid high-cardinality defaults — **reviewed**: current labels (`method`, grouped `path`, `status`) are cardinality-bounded; an authenticated-client-class label has no safe default and multiplies series, so it stays opt-in pending a concrete ALP request rather than a speculative default. Left unchecked (feature deliberately gated, not the review)
- [x] After ALP Trace audit, review whether `plugins/otel` needs additional context propagation or span/log correlation hooks — shipped as semconv v1.37.0 migration with `http.route`, `network.protocol.version`, and per-attribute corrections (see §12.6.9)
- [x] Track every ALP-discovered rough edge as an Aarv issue or PR and mirror it in ALP's `AARV_FEEDBACK.md` — AARV_FEEDBACK P1 #1/#2/#3 + P2 #4/#5/#6/#7 + P3 #10 addressed; see §12.6.9 for the per-item mapping
- [x] Do not change `plugins/prometheus` or `plugins/otel` preemptively; build only concrete deltas observed under ALP — process rule, honored throughout the batch

### 12.6.8 Release and ALP consumption
- [x] Run root tests: `go test -race ./...`
- [x] Run every new/changed plugin submodule test with `go test -race ./...`
- [x] Run `golangci-lint run`
- [x] Run `govulncheck` for root and each new Redis submodule
- [x] Tag root release and all touched plugin submodules
- [x] Verify from outside the repo that `go list -m` resolves the new tags for root and submodules
- [ ] Add ALP follow-up note: replace internal HMAC, rate-limit, and idempotency implementations with Aarv imports once tags resolve
- [ ] Add ALP follow-up note: evaluate `BindRoute` + `plugins/openapi` + `plugins/openapi-ui` for management API drift detection after ALP bumps from `v0.7.0`

### 12.6.9 ALP feedback batch — shipped (M12.6 final pre-tag)

> Tracks the AARV_FEEDBACK P1/P2/P3 items shipped after ALP Track 6's observability audit. Cross-reference: [`CHANGELOG.md`](../CHANGELOG.md) `[Unreleased]` section carries the customer-facing version of every entry below, including the otel dashboard-migration note.

#### Public API additions (root module)

- [x] `aarv.StatusRecorder` ([recorder.go](../recorder.go)) — public `http.ResponseWriter` wrapper capturing status code + total body bytes. Constructor `NewStatusRecorder(w)` is unpooled; `Reset(w)` is exposed for callers that pool privately. Implements `Unwrap() http.ResponseWriter` so `http.ResponseController` walks through to `Flusher` / `Hijacker` / `Pusher`. Pooling is intentionally NOT exposed through the constructor (see [memory rule](../../../.claude/projects/-Users-nilayshah-Documents-PoC-aarv/memory/feedback_pool_api_contract.md)): public pooling means public lifecycle rules and misuse risk rarely worth the GC savings on the request hot path. Closes AARV_FEEDBACK P2 #4. Covered by 7 tests in [recorder_test.go](../recorder_test.go) including short-write byte accounting and pool-Reset state isolation.
- [x] `aarv.SkipPaths(paths []string, mw any) NativeMiddleware` ([skippaths.go](../skippaths.go)) — top-level helper for excluding paths from a wrapped middleware. `mw` accepts any of `aarv.Middleware`, `aarv.NativeMiddleware`, or `func(http.Handler) http.Handler`. **Native fast path preserved** — distinct `SkipPaths` instances each carry their own native fn (no global registry lookup), so multiple `SkipPaths`-wrapped middlewares in the same App do not collide. Empty `paths` returns the inner middleware unchanged (wrapped into a `NativeMiddleware`) so callers can wire it in unconditionally; nil `mw` panics immediately rather than silently no-op'ing. Path matching is exact and case-sensitive against `r.URL.Path` (stdlib) and `c.Path()` (native); no trailing-slash normalization, query stripping, or pattern matching. Closes AARV_FEEDBACK P2 #5; the v0.8.0-era stdlib-only / nil-returns-nil shape called out in earlier drafts is superseded by this v0.9.0 redesign.

#### Plugin additions and changes

- [x] `plugins/hmacauth` `Config.Observer` hook with `Outcome` / `Event` types ([observer.go](../plugins/hmacauth/observer.go), [hmacauth.go](../plugins/hmacauth/hmacauth.go)). Fires once per verification attempt (after Skipper bypass) carrying the canonical `Outcome` enum (`OutcomeOK`, `OutcomeUnauthorized`, `OutcomeClockSkew`, `OutcomeSignatureInvalid`, `OutcomeReplayDetected`, `OutcomeBodyTooLarge`), the client ID, the response status, the absolute drift in seconds (clock-skew only), and the wall-clock duration. Zero-overhead nil contract: when `Observer == nil`, the verify path makes no extra `Now` calls and constructs no `Event`. Outcome distinctions matter operationally — nonce-store transport errors report `OutcomeUnauthorized` not `OutcomeReplayDetected` so a Redis outage does not look like a flood of replay attempts; body-read transport errors report `OutcomeUnauthorized` not `OutcomeBodyTooLarge` since their actual status is 401 not 413. Designed so OTel/Prometheus adapters live in companion submodules — root stays zero-dep (see [memory rule](../../../.claude/projects/-Users-nilayshah-Documents-PoC-aarv/memory/feedback_root_plugin_observability.md)). Closes AARV_FEEDBACK P1 #2. Covered by 11 tests in [observer_test.go](../plugins/hmacauth/observer_test.go) including the zero-overhead `Now` call counter and the two outcome-distinction regression guards.
- [x] `plugins/otel` HTTP semconv migration to OTel v1.37.0 ([attributes.go](../plugins/otel/attributes.go), [otel.go](../plugins/otel/otel.go), [otel_test.go](../plugins/otel/otel_test.go)). Modern keys: `http.request.method`, `url.path` (raw path), `http.route` (matched pattern), `http.response.status_code`, `user_agent.original`, `client.address`, `network.protocol.version`. Legacy keys (`http.method`, `http.target`, `http.status_code`, `http.user_agent`, `net.peer.ip`) dual-emitted for one transitional minor — legacy `http.target` keeps its pre-migration shape (pattern when matched, raw path otherwise) and must not be conflated with modern `url.path`. Metrics deliberately omit `url.path` (TSDB cardinality guard) and use `http.route` only — locked in by `TestSemconv_MetricsUseRouteNotURLPath`. Closes AARV_FEEDBACK P1 #1.
- [x] `plugins/prometheus` `SubMillisecondBuckets` preset ([prometheus.go](../plugins/prometheus/prometheus.go)) — `100µs..5s` for services whose p50 falls below `DefaultBuckets`' 1ms first bucket. Closes AARV_FEEDBACK P2 #6. Covered by 2 tests including a config-level integration check that the histogram receives the right bucket boundaries.
- [x] `plugins/openapi` `Config.Tags` declarative filter ([openapi.go](../plugins/openapi/openapi.go)). When non-empty, only routes carrying at least one of the listed tags (set via `aarv.WithTags(...)`) survive; ignored when `Include` is set; applied before `Exclude`. Empty / all-blank `Tags` is treated as unset. Closes AARV_FEEDBACK P3 #10 earlier than expected — ALP Tracks 5/8 will benefit directly when they add their endpoint groups. Covered by 6 tests including tag-union semantics, ignored-when-Include-set, and ordering against Exclude.
- [x] `plugins/logger` internal `responseWriter` ([logger.go](../plugins/logger/logger.go)) migrated to `aarv.StatusRecorder` (still pool-backed privately via `sync.Pool` with `Reset`-based recycling). No behavior change; ~50 LOC of duplication dropped. Updates [logger_test.go](../plugins/logger/logger_test.go) to use the new accessors. Submodule plugins (`plugins/prometheus`, `plugins/otel`) now also migrated — see §12.6.10 below.

#### Documentation

- [x] [`docs/middleware.md`](middleware.md) — new "Wrapping a middleware to add observability" recipe added with three options (stdlib-only wrapper, native-only wrapper, both forms via `RegisterNativeMiddleware`) and a worked example covering `c.SetContext` / `c.BindRequest` / `RegisterNativeMiddleware`. Closes AARV_FEEDBACK P2 #7 AND resolves P1 #3's doc gap in a single page — no separate doc needed.
- [x] [`docs/openapi.md`](openapi.md) — filtering table updated with `Config.Tags` row, plus a worked example showing per-tag spec splitting.
- [x] [`plugins/otel/README.md`](../plugins/otel/README.md) and [`plugins/prometheus/README.md`](../plugins/prometheus/README.md) updated with the new symbols, dual-emit caveat, and bucket-preset selection guidance.
- [x] [`CHANGELOG.md`](../CHANGELOG.md) `[Unreleased]` populated with `Added` / `Changed` / `Migration` / `Internal` sections covering every entry above. The Migration section spells out the otel dashboard-query update path explicitly so dashboard owners can plan before the legacy attributes are dropped in the next-next release.

#### Historical limitation — resolved by the v0.9.0 registry redesign

- [x] *(Historical, v0.8.0 era)* The legacy `nativeMiddlewareRegistry` keyed on `reflect.ValueOf(m).Pointer()` ([middleware.go](../middleware.go)), which returned the *code* pointer for func values, so closures from the same source function literal shared a registry slot. This silently corrupted the native chain when two distinct registrations of the same wrapper ran in one App. **Resolved in v0.9.0** by replacing the global registry with the `NativeMiddleware{Stdlib, Native}` value type — pairs travel with the middleware value itself, eliminating identity-keying entirely. See [CHANGELOG.md](../CHANGELOG.md) "Native middleware registry redesign" and the §12.6.10 entries below for the downstream cleanup.

#### Follow-ups originally gated on the v0.9.0 release — now completed in scope

- [x] `plugins/prometheus` and `plugins/otel` internal `recordingWriter` migration to `aarv.StatusRecorder`. Completed; both submodules now pool `*aarv.StatusRecorder` and use `Reset(w)` per the public pool contract. Net ~43 LOC dropped per submodule with no behavior change. See [prometheus.go](../plugins/prometheus/prometheus.go) lines ~347-368 and [otel.go](../plugins/otel/otel.go) lines ~356-376.
- [x] `plugins/hmacauth-otel` companion submodule. Created with its own `go.mod`, subscribes to `hmacauth.Config.Observer`, and emits the `auth.HMAC.verify` span with `auth.client_id` / `auth.outcome` / `auth.response_status` / `auth.skew_seconds` attributes. Span is backdated via `Event.Duration` so the recorded window covers the actual verification. 100% coverage.

### 12.6.10 Post-tag follow-ups

> Cleanup that becomes possible once §12.6.9's symbols are tagged in a root release. Each item is gated on a specific upstream condition; nothing here runs autonomously. See [memory rule on tagging cadence](../../../.claude/projects/-Users-nilayshah-Documents-PoC-aarv/memory/feedback_no_coauthor.md) for commit conventions.

#### Tag and bump (two-stage release)

The Go multi-module release flow is intentionally staged: submodule `go.mod` files can only be re-pinned to a root tag after that tag exists, otherwise `go mod tidy` cannot resolve it. During development everything runs under `go.work` (gitignored); consumers without `go.work` see the staged state, and a `GOWORK=off` run of a submodule against the v0.8.0 pin will fail to compile against v0.9.0-only APIs — that's the expected pre-Stage-2 state, not a defect.

- [x] **Stage 1** — Cut the aarv root release as **`v0.9.0`** (minor bump; intentional breaking changes documented in `CHANGELOG.md [0.9.0]`). Run the §12.6.8 release checklist end-to-end (root tests, lint, govulncheck, tag, `go list -m` verification from outside the repo). Root tags `v0.9.0` and `v0.9.1` exist.
- [x] **Stage 2** — For each affected submodule (`plugins/prometheus`, `plugins/otel`, `plugins/ratelimit-redis`, `plugins/sanitize`, `plugins/hmacauth-otel`, and any other path-tagged module that consumes root v0.9.0 APIs): `GOWORK=off go get github.com/nilshah80/aarv@v0.9.0`, then `GOWORK=off go mod tidy` inside the submodule directory (never from root), commit the resulting `go.mod`/`go.sum`, then tag the submodule. Submodule tags exist for `plugins/{prometheus,otel,ratelimit-redis,sanitize,openapi}@v0.9.0` and `plugins/hmacauth-otel/v0.1.0`.
- [x] **Stage 2 verification** — After each submodule pin bump, `GOWORK=off go test -race ./...` inside each submodule must be green against the new pin.

#### StatusRecorder adoption in submodule plugins

- [x] Migrate `plugins/prometheus`'s internal `recordingWriter` to wrap `aarv.StatusRecorder` ([prometheus.go](../plugins/prometheus/prometheus.go), lines ~347-368). Pool now holds `*aarv.StatusRecorder`; helpers use `Reset(w)` per the pool API contract. No public API change; tests adjusted to use `Status()` / `BytesWritten()`.
- [x] Migrate `plugins/otel`'s internal `recordingWriter` the same way ([otel.go](../plugins/otel/otel.go), lines ~356-376). Same pattern; same test-side adjustment.
- [x] Tag updated `plugins/prometheus` and `plugins/otel` submodule releases — included in Stage 2 above.

#### `plugins/hmacauth-otel` companion submodule

- [x] Create `plugins/hmacauth-otel/` with its own `go.mod` that depends on the `github.com/nilshah80/aarv` module (root) and imports `github.com/nilshah80/aarv/plugins/hmacauth` plus `go.opentelemetry.io/otel`. The companion's job: subscribe to `hmacauth.Config.Observer` and emit an `auth.HMAC.verify` span per verification attempt.
- [x] Span attribute schema: `auth.client_id` (Event.ClientID), `auth.outcome` (string(Event.Outcome)), `auth.response_status` (Event.Status when non-zero), `auth.skew_seconds` (Event.SkewSeconds when ClockSkew). Span status = error on non-OK outcomes so Tempo / Honeycomb filters like `{ name = "auth.HMAC.verify" && status = error }` work directly. Span is **backdated** via `Event.Duration` so its recorded window covers the actual verification, not the observer callback.
- [x] Provide a convenience `hmacauthotel.NewObserver(opts) hmacauth.Observer` so consumers can wire it via `cfg.Observer = hmacauthotel.NewObserver(...)` without rewriting boilerplate. The TracerProvider defaults to `otel.GetTracerProvider()` when no `WithTracerProvider` option is passed.
- [x] Unit tests with `sdktrace.NewTracerProvider(sdktrace.WithSyncer(recorder))` to capture spans; 10 tests covering span name (default + override), attribute schema per outcome, codes.Error iff non-OK, omission of zero status / skew, default provider fallback, nil-context root parenting, and span backdating.
- [x] README with a wiring example that mirrors `plugins/otel/README.md`'s "Bring your own Provider" section.
- [x] Tag `plugins/hmacauth-otel/v0.1.0` — *queued for the v0.9.0 tag sequence*.
- [x] After this lands, ALP can replace its `traceHMAC` Observer adapter (or local wrapper) with `hmacauthotel.NewObserver(...)` — the cleanest version of the AARV_FEEDBACK P1 #2 closeout.

#### Registry-keying fix (gating item for native-aware wrapper helpers) — RESOLVED in v0.9.0

> The architectural follow-up landed in v0.9.0. `nativeMiddlewareRegistry` and `nativeMiddlewareFunc` are **deleted entirely**; the chain builder consumes a private `[]middlewareSlot` whose entries carry both paths inline. The "Known limitation" subsection in §12.6.9 is therefore resolved.

- [x] **Resolved differently than the original sketch** — instead of keeping `Middleware`'s type signature unchanged and only swapping internal storage, v0.9.0 introduces `aarv.NativeMiddleware{Stdlib, Native}` as a value type that `RegisterNativeMiddleware` and `WrapMiddleware` return directly. This requires per-plugin constructor migration (33 free constructors + 2 `*Authorizer` methods) and a variadic widening on `App.Use` / `RouteGroup.Use` / `PluginContext.Use` / `WithRouteMiddleware` from `...Middleware` to `...any`. The breaking change is accepted at the v0.9.0 minor bump; documented in `CHANGELOG.md [0.9.0]`.
- [x] All ~32 existing `RegisterNativeMiddleware` call sites audited and migrated (in-root + submodules); `plugins/timeout.New` and `pprof.Config.AuthMiddleware` are the only stdlib-only exceptions that stay typed `aarv.Middleware`.
- [x] Unified regression test ([`nativemiddleware_test.go`](../nativemiddleware_test.go)) — `TestRegisterNativeMiddleware_DistinctInstancesDoNotCollide` constructs two distinct wrappers from the same source function and asserts each fires its own native variant.
- [x] `aarv.SkipPaths`'s native variant re-introduced; native fast-path preservation locked in by `TestSkipPaths_PreservesNativeFastPathAcrossInstances`. Old `TestSkipPaths_DoesNotRegisterNativePair` guard removed.
- [x] `CHANGELOG.md [0.9.0]` documents the registry removal and the SkipPaths re-introduction.
- [x] [`skippaths.go`](../skippaths.go) doc comment and [`docs/middleware.md`](middleware.md) "Skipping the wrap on observability paths" subsection updated to drop the stdlib-only caveat.

---

## Phase 13: Documentation & Benchmarks (M13) ✅

### Docs
- [x] README.md: badges, install, quick start, feature list
- [x] docs/getting-started.md
- [x] docs/routing.md
- [x] docs/binding.md
- [x] docs/validation.md
- [x] docs/middleware.md
- [x] docs/hooks.md
- [x] docs/plugins.md (using + writing)
- [x] docs/error-handling.md
- [x] docs/tls-http2.md
- [x] docs/testing.md
- [x] docs/auth.md
- [x] docs/session.md
- [x] docs/architecture.md

### Adoption Docs
- [x] docs/observability.md (request IDs, logging, health, Prometheus, OpenTelemetry, pprof)
- [x] docs/resilience.md (body limits, timeout, throttle, rate limiting, idempotency, Redis stores)
- [x] docs/security.md (security headers, CORS, CSRF, IP filtering, sanitization, encryption)
- [x] docs/responses.md (response helpers, files, uploads, streaming, static, compression, ETags)
- [x] docs/codecs.md (stdlib, segmentio, sonic, jsonv2)
- [x] docs/release-policy.md (compatibility, root/submodule tags, release verification)

### Benchmarks ✅
- [x] Framework overhead: empty handler, measure latency + allocs
- [x] JSON codec comparison: stdlib vs segmentio vs sonic vs jsonv2
- [x] Middleware chain: 0, 1, 5, 10 middlewares overhead
- [x] Binding: manual vs `Bind[T]` overhead
- [x] Validation: framework validator vs go-playground/validator
- [x] Comparison: framework vs Gin vs Mach vs Fiber (tests/benchmark/compare_test.go)
- [x] Load test: 500K requests, 100 VCs, real TCP with latency/memory/CPU metrics

### Examples ✅
- [x] examples/hello — minimal hello world
- [x] examples/rest-crud — full CRUD with typed handlers
- [x] examples/jwt-auth — JWT protected API
- [x] examples/file-upload — multipart form handling with binder integration
- [x] examples/middleware-chain — custom middleware
- [x] examples/plugin-custom — writing a plugin
- [x] examples/tls-http2 — HTTPS setup
- [x] examples/microservice — health check + prometheus + structured logging
- [x] examples/sse — server-sent events real-time updates
- [x] examples/openapi — auto-generated OpenAPI docs with Swagger UI

### Quality Gates / Follow-ups
- [x] Add `gofmt -s -l` check to CI lint workflow
- [x] Advisory `gocyclo` measurement added as a **non-blocking** CI step in `.github/workflows/lint.yml` (`continue-on-error: true`, `gocyclo -over 15 -top 25`, examples/benchmark ignored). Reports the most complex functions for visibility; never release-blocking (no agreed threshold)
- [x] Deduplicated `plugins/session` normalize helpers — `normalizeCookieConfig` now maps onto the shared session fields and delegates to `normalizeConfig`, so the inversion + defaults logic lives in one place (no behavior change; existing tests green)
- [x] Extended plugin curation notes to auth-related plugins (authn schemes: basicauth/apikey/bearer/jwt/hmacauth; authz: rbac; orthogonal: problem) in `docs/plugins.md`, alongside the existing observability note. Standing convention as new plugins land

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

### 14.2 Reverse Proxy Plugin
**Why**: Useful for BFF (Backend-For-Frontend) and lightweight gateway patterns where Aarv fronts one or more upstream services. Built on `net/http/httputil`, zero new dependencies, ~150 lines.
- [ ] Create `plugins/proxy` package
- [ ] Wrap `httputil.ReverseProxy` with aarv-friendly configuration
- [ ] `Balancer` interface: `Next(*Context) *url.URL` — picks an upstream
- [ ] Built-in balancers: `RoundRobin([]*url.URL)`, `Random([]*url.URL)`, `IPHash([]*url.URL)` (sticky by client IP)
- [ ] Per-target health tracking: mark target unhealthy on N consecutive failures, periodic recheck
- [ ] Path rewrite: configurable prefix strip / replacement before forwarding
- [ ] Header manipulation: add/remove request and response headers, preserve `X-Forwarded-For`/`X-Forwarded-Proto`/`X-Forwarded-Host`
- [ ] Configurable: dial timeout, response header timeout, idle conns, max idle conns per host, TLS config
- [ ] Error handler for upstream failures (502/504 with custom body)
- [ ] Optional WebSocket passthrough via `httputil.ReverseProxy`'s built-in upgrade support
- [ ] Helper: `app.Proxy("/api/*", "http://upstream:8080")` route option for trivial cases
- [ ] Unit tests: round-trip, header forwarding, balancer rotation, unhealthy target failover, path rewrite, error response

### 14.3 GraphQL Adapter
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
- [x] 90%+ code coverage target — met for non-example packages per current assessment (98.7%)
- [x] Race detector enabled in CI: `go test -race ./...`
- [x] Fuzz tests for: JSON binding, validation tag parsing, URL parsing — `FuzzBindJSON`, `FuzzParseValidateTag` (NaN-safe determinism check), and `FuzzBindQuery` (raw bytes via `req.URL.RawQuery`) in `fuzz_test.go`. Seed corpus runs under `go test`; active fuzzing is manual/scheduled, not a PR gate. `FuzzCanonicalQuery` additionally covers HMAC query canonicalization
- [x] Integration test suite: full request lifecycle

### Performance ✅
- [x] Zero-allocation hot path audit (use `go test -benchmem`)
- [x] `sync.Pool` for: Context, buffered writer, gzip writer, validation error slices
- [x] Escape analysis audit: `Req` does **not** stay on the stack — `&req` is boxed as `any` for the codec/binder/validator, forcing one heap alloc per bound request (`gcflags=-m=2`: `moved to heap: req` at `bind.go:138`/`174`). Documented as expected (not a regression) in `bind_escape_test.go` + `docs/architecture.md`; stack retention is not achievable without dropping the pluggable codec abstraction
- [x] Pre-build middleware chain at startup (no per-request chain assembly)
- [x] Pre-compute binder + validator at registration time (no per-request reflect)

### Security
- [x] JWT: reject `alg: none`
- [x] JWT: validate `alg` header matches expected algorithm
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
