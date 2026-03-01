# plan.md — Project Plan & Roadmap

> **Project**: Aarv — Lightweight Go Web Framework
> **Go Version**: 1.23+ (leverages Go 1.22+ enhanced ServeMux)
> **License**: MIT
> **Repository**: `github.com/nilshah80/aarv`

---

## Vision

Build the fastest, zero-dependency Go web framework on top of `net/http` stdlib, combining:
- **.NET Minimal API**: Fluent route registration, type-safe request binding via generics, functional options builder
- **Fastify**: Plugin encapsulation, lifecycle hooks, schema-first validation, decorator pattern
- **Mach/Bottle**: Minimalism, zero external dependencies in core, thin abstraction over stdlib

**Non-goals**: Not an MVC framework. No template engine. No ORM. No DI container. No CLI tooling. No code generation. Library-only.

---

## Architecture Overview

```
┌────────────────────────────────────────────────────────────────────┐
│                         User Application                           │
│   app.Get("/users/{id}", Bind(getUser))                            │
├────────────────────────────────────────────────────────────────────┤
│                                                                    │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌───────────────────┐  │
│  │  Router   │  │ Bind[T]  │  │ Plugins  │  │  Middleware Chain │  │
│  │ (ServeMux │  │ Generics │  │ (Scoped) │  │  (Onion Model)   │  │
│  │  Wrapper) │  │ Wrappers │  │          │  │                  │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────────┬─────────┘  │
│       │              │             │                  │            │
│  ┌────▼──────────────▼─────────────▼──────────────────▼─────────┐  │
│  │                        Framework Core                         │  │
│  │  Context (pooled) │ Codec Interface │ Hooks │ Validator       │  │
│  │  ErrorHandler │ Binder │ ResponseWriter (buffered)            │  │
│  └────────────────────────────┬──────────────────────────────────┘  │
│                               │                                    │
├───────────────────────────────▼────────────────────────────────────┤
│                     net/http (Go stdlib)                            │
│         ServeMux │ Server │ TLS │ HTTP/2 │ httptest                 │
└────────────────────────────────────────────────────────────────────┘
```

---

## Milestones

### M1: Foundation ✅ COMPLETE
> Goal: Minimal working framework — can register routes, handle requests, return JSON

- [x] App struct with functional options builder
- [x] Context struct with `sync.Pool` recycling
- [x] Router wrapping Go 1.22+ `http.ServeMux`
- [x] Fluent route registration (`app.Get`, `app.Post`, etc.)
- [x] Handler adapter supporting multiple signatures
- [x] Codec interface + stdlib JSON default
- [x] Basic response helpers (`c.JSON`, `c.Text`, `c.NoContent`)
- [x] Graceful shutdown with signal handling
- [x] `Listen` and `ListenTLS`

**Exit Criteria**: ✅ Can run `app.Get("/hello", func(c *Context) error { return c.JSON(200, "hello") })` and get a response.

---

### M2: Type-Safe Binding ✅ COMPLETE
> Goal: .NET Minimal API-style typed handlers with auto body parsing

- [x] `Bind[Req, Res]()` generic wrapper
- [x] `BindReq[Req]()` — request-only binding
- [x] `BindRes[Res]()` — response-only binding
- [x] `Adapt()` — stdlib `http.HandlerFunc` compatibility
- [x] Multi-source struct binder (path params, query, headers, cookies, body)
- [x] Struct tag parsing: `param:""`, `query:""`, `header:""`, `cookie:""`, `form:""`, `default:""`
- [x] Type coercion (string → int, int64, float64, bool, uuid)
- [x] Registration-time reflection with pre-computed field maps
- [x] Zero-reflection hot path

**Exit Criteria**: ✅ `app.Post("/users", Bind(createUser))` auto-parses JSON body into typed struct and returns typed response.

---

### M3: Validation Engine ✅ COMPLETE
> Goal: Struct tag validation, zero-dependency, pre-computed rules

- [x] Validation tag parser (`validate:"required,min=2,max=100"`)
- [x] All built-in rules (required, min, max, gte, lte, oneof, email, uuid, url, regex, etc.)
- [x] Pre-computed validation rules at registration time
- [x] `unsafe.Pointer` + field offset arithmetic for fast runtime validation
- [x] Custom validator registration
- [x] `SelfValidator` interface for user types
- [x] Validation error response format (422)
- [x] Integration into `Bind[T]` pipeline

**Exit Criteria**: ✅ Struct with `validate:""` tags auto-validates before handler, returns structured 422 on failure.

---

### M4: Middleware & Hooks ✅ COMPLETE
> Goal: Full middleware chain + Fastify lifecycle hooks

- [x] Middleware chain builder (onion model)
- [x] Standard `func(http.Handler) http.Handler` compatibility
- [x] Framework `func(next HandlerFunc) HandlerFunc` middleware
- [x] Route groups with prefix + scoped middleware
- [x] Nested route groups
- [x] Lifecycle hooks: OnRequest, PreRouting, PreParsing, PreValidation, PreHandler, OnResponse, OnSend, OnError
- [x] Hook priority ordering
- [x] OnStartup / OnShutdown hooks

**Exit Criteria**: ✅ Can apply middleware at global/group/route level; hooks fire in correct order.

---

### M5: Core Plugins ✅ COMPLETE
> Goal: Essential middleware plugins, all zero-dependency

- [x] Recovery (panic → 500)
- [x] Request ID (ULID generation + propagation)
- [x] Logger (slog structured request logging)
- [x] CORS (full spec compliance)
- [x] Secure Headers (XSS, HSTS, CSP, X-Frame, Referrer-Policy, Permissions-Policy)
- [x] Body Limit (per-route configurable)
- [x] Timeout (per-route `context.WithTimeout`)
- [x] Compress (gzip/deflate via `compress/gzip` stdlib)
- [x] ETag (auto-generation + conditional 304)
- [x] Static Files (file server with SPA fallback)
- [x] Health Check (/health, /ready, /live)

**Exit Criteria**: ✅ All 11 plugins working, configurable, zero external dependencies.

---

### M6: Auth Plugins
> Goal: JWT + API key + Basic auth, all using stdlib crypto

- [ ] JWT plugin: parse, validate, sign using `crypto/hmac`, `crypto/rsa`, `crypto/ecdsa`, `crypto/ed25519`
- [ ] JWT token lookup from header/query/cookie
- [ ] JWT claims extraction to Context store
- [ ] API Key middleware (header/query lookup + validator callback)
- [ ] Basic Auth middleware
- [ ] Bearer Token middleware
- [ ] RBAC middleware (role-based route guarding)

**Exit Criteria**: JWT auth middleware validates tokens, extracts claims, guards routes — zero external deps.

---

### M7: Plugin System ✅ COMPLETE
> Goal: Fastify-style encapsulated plugin registration

- [x] Plugin interface: `Name()`, `Register(*PluginContext)`, `Version()`
- [x] PluginContext: scoped route registration, hooks, middleware, decoration
- [x] Functional plugin adapter (`PluginFunc`)
- [x] Nested plugin registration
- [x] Decorator pattern (shared services across plugins)
- [ ] Plugin dependency ordering (optional)

**Exit Criteria**: ✅ Can write a self-contained plugin that registers routes + hooks + middleware in isolation.

---

### M8: Codec Sub-Packages ✅ COMPLETE
> Goal: Drop-in fast JSON adapters

- [x] `codec/segmentio` — wraps `segmentio/encoding/json`
- [x] `codec/sonic` — wraps `bytedance/sonic` with config profiles
- [x] `codec/jsonv2` — wraps `go-json-experiment/json` (or stdlib `encoding/json/v2` when stable)
- [x] Each as separate Go module (`go.mod`) to avoid dependency pollution
- [ ] Benchmark suite comparing all codecs

**Exit Criteria**: ✅ `app := New(WithCodec(segmentio.New()))` switches JSON engine in one line.

---

### M9: Testing & Utilities ✅ COMPLETE
> Goal: First-class testing support

- [x] `TestClient` — fire requests without network via `httptest`
- [x] `TestResponse` — assert status, parse JSON, check headers
- [x] Fluent test API: `tc.WithHeader("Auth", "Bearer ...").Post("/users", body)`

**Exit Criteria**: ✅ Can write `resp := NewTestClient(app).Post("/users", body); assert(resp.Status == 201)`.

---

### M10: Security Plugins
> Goal: Rate limiting, CSRF, IP filtering

- [ ] Rate Limiter: token bucket + sliding window, per-IP or per-key, zero-dep
- [ ] Throttle: max concurrent requests limiter
- [ ] CSRF: token generation + validation (double-submit cookie pattern)
- [ ] IP Filter: whitelist/blacklist with CIDR range support
- [ ] Request Sanitizer: strip XSS vectors, normalize Unicode

**Exit Criteria**: Rate limiter returns 429, CSRF protects POST routes, IP filter blocks ranges.

---

### M11: Observability Plugins
> Goal: Prometheus, OpenTelemetry, Pprof

- [ ] Prometheus: request count, latency histogram, in-flight gauge, custom collectors
- [ ] OpenTelemetry: span creation, context propagation, attribute injection
- [ ] Pprof: mount `net/http/pprof` handlers at configurable prefix
- [ ] Each as separate Go module (has external deps)

**Exit Criteria**: Prometheus `/metrics` endpoint exports request latency histograms.

---

### M12: TLS Helpers
> Goal: Production TLS features

- [ ] `ListenAutoTLS` via `golang.org/x/crypto/acme/autocert`
- [ ] `ListenMutualTLS` with client CA verification
- [ ] Certificate file watcher for hot-reload
- [ ] Recommended TLS defaults for financial services
- [ ] h2c (HTTP/2 cleartext) plugin for internal mesh

**Exit Criteria**: `app.ListenAutoTLS(":443", "example.com")` auto-provisions Let's Encrypt cert.

---

### M13: Documentation & Benchmarks — IN PROGRESS
> Goal: Production-ready documentation and performance proof

- [x] README with quick start
- [ ] API reference (generated from godoc)
- [ ] Architecture guide
- [ ] Plugin development guide
- [ ] Migration guide (from Gin, Echo, Chi)
- [x] Benchmark suite: framework overhead, comparison vs Gin/Fiber/Mach
- [x] Load test: 500K requests, 100 VCs, real TCP (latency, memory, CPU, RPS)
- [x] Example applications: hello, rest-crud
- [ ] Example applications: JWT auth, file upload, middleware chain, plugin, TLS, microservice

**Exit Criteria**: Sub-microsecond framework overhead per request. Competitive with Gin/Echo in benchmarks. ✅ (Confirmed via benchmarks)

---

## Progress Summary

```
✅ M1  Foundation              — COMPLETE
✅ M2  Type-Safe Binding       — COMPLETE
✅ M3  Validation Engine       — COMPLETE
✅ M4  Middleware & Hooks       — COMPLETE
✅ M5  Core Plugins (11/11)    — COMPLETE
⬜ M6  Auth Plugins            — NOT STARTED
✅ M7  Plugin System           — COMPLETE
✅ M8  Codec Sub-Packages      — COMPLETE
✅ M9  Testing Utilities       — COMPLETE
⬜ M10 Security Plugins        — NOT STARTED
⬜ M11 Observability Plugins   — NOT STARTED
⬜ M12 TLS Helpers             — NOT STARTED
🔶 M13 Docs & Benchmarks      — IN PROGRESS (benchmarks done, docs pending)
```

**Overall**: 9 of 13 milestones complete. Core framework fully functional with benchmarks proving competitive performance vs Gin, Fiber, and Mach.

---

## Release Strategy

| Version | Content | Quality Gate | Status |
|---|---|---|---|
| `v0.1.0-alpha` | M1-M4 (core framework) | All tests pass, basic benchmark | ✅ Ready |
| `v0.2.0-alpha` | +M5-M6 (plugins, auth) | Integration tests, security review | 🔶 M5 done, M6 pending |
| `v0.3.0-beta` | +M7-M10 (plugin system, testing, security) | Load testing, fuzzing | 🔶 M7-M9 done, M10 pending |
| `v0.4.0-beta` | +M11-M12 (observability, TLS) | Production pilot on internal service | ⬜ Not started |
| `v1.0.0` | All milestones + docs + benchmarks | API freeze, semver commitment | ⬜ Not started |

---

## Risk Register

| Risk | Impact | Mitigation |
|---|---|---|
| Go 1.22 ServeMux limitations (no regex, no param constraints) | Medium | Validate in PreValidation hook; document clearly |
| `encoding/json/v2` API changes before stable | Low | Codec interface isolates framework from JSON impl |
| `unsafe.Pointer` in validator breaks on Go version upgrade | Medium | Comprehensive test suite; fallback to reflect-based validator |
| Sonic build constraint `!go1.27` | Low | Sonic is optional codec sub-package, not core |
| Performance regression from buffered ResponseWriter | Medium | Benchmark; provide opt-out for streaming endpoints |
| Plugin isolation is hard without goroutine-local storage | Low | Convention-based isolation via PluginContext scoping |
