# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.6.0] - 2026-05-02

### Added
- Core: `(*Context).RoutePattern() string` returns the registered aarv route pattern that matched a request, in path-only form (e.g. `/users/{id}`), or empty for 404, 405, `App.Mount` handlers, and any path outside the registered aarv route table. Set by the dispatcher before the matched handler runs, so route-level middleware sees it pre-`next` and global middleware sees it post-`next`. Used by the prometheus and otel plugins for cardinality-controlled label values and span names.
- Core: `(*Context).SetLogger(*slog.Logger)` swaps the request-scoped logger for the remainder of a request. Pass `nil` to clear any previous override; the next `Logger()` call rebuilds from `app.logger`. The override is also cleared on pool return. Pairs with `Logger()` for the OTel log-correlation pattern.
- pprof plugin (`plugins/pprof`, root module, stdlib-only): mounts Go's standard `net/http/pprof` endpoints under a configurable prefix. Exposes both `Handler(cfg) http.Handler` for `App.Mount`-style usage and `New(cfg) aarv.Middleware` for chain-style usage. `Handler` internally restores the configured prefix on App.Mount-stripped paths so the inner mux's registered routes match and `pprof.Index` (which hardcodes `/debug/pprof/`) sees its expected URL shape. Optional `AuthMiddleware` gating closes pprof to unauthenticated callers — strongly recommended in any environment where pprof is reachable from outside the operator's machine.
- verboselog plugin: new `Sink Sink` callback and `SuppressSlog bool` toggle on `Config`. The sink receives captured request/response bytes (post-truncation, post-redaction — consistent with what slog sees) and a `DumpMeta` struct with status, latency, request_id, method, path, headers, and query. Useful for delivering audit captures to a database, object store, message queue, or fixture recorder. Sink invocation is panic-safe (a panicking sink is recovered and logged via slog without crashing the request). `SuppressSlog: true` with `Sink: nil` panics in `New` — that combination is a no-op middleware. New example at `examples/verboselog-audit/`.
- Prometheus plugin (`plugins/prometheus`, separate module): records the four standard HTTP server metrics (`http_requests_total`, `http_request_duration_seconds`, `http_requests_in_flight`, `http_response_size_bytes`) labeled by method, path, and status. Default `GroupPath` consults `Context.RoutePattern()` to keep label cardinality bounded — collapses `/users/1`, `/users/2`, etc. to a single `/users/{id}` label. Exposes `New(cfg) aarv.Middleware` for instrumentation and `Handler(cfg) http.Handler` for the `/metrics` scrape endpoint (recommend registering as a regular `app.Get` route, not via `App.Mount` which redirects). The recording response writer implements `Unwrap` so `http.ResponseController` can reach the underlying writer for streaming, hijacking, or HTTP/2 push.
- OpenTelemetry plugin (`plugins/otel`, separate module): one server span per request (extracts W3C trace context from incoming headers via the configured Propagator — outbound injection is left to the application's outbound HTTP client, e.g. via `otelhttp.NewTransport`), HTTP semconv attributes, 5xx → span status `Error`, four standard HTTP server metrics via the configured MeterProvider, and trace-correlated `slog` (replaces `Context.Logger()` with one carrying `trace_id` and `span_id` for the request lifetime, restored on handler return). The recording response writer implements `Unwrap` for `http.ResponseController` compatibility. Config uses inverted `Suppress*` booleans (`SuppressErrorStatus`, `SuppressMetrics`, `SuppressLogAttrs`) so zero-value `Config{}` produces all default behaviors. Bring-your-own-Provider design: the plugin does not pull or configure exporters / samplers / batchers — those live on the user-supplied `TracerProvider` / `MeterProvider`.

### Changed
- CI workflow: split into two jobs. The root-test job retains the `1.22`/`1.23` matrix and exercises the zero-dep root module. A new `test-plugin-submodules` job pins to Go `1.25` explicitly and auto-discovers any `plugins/*/go.mod`, running `go test -race ./...` inside each — this satisfies both `plugins/prometheus`'s `go 1.23.0` floor and `plugins/otel`'s `go 1.25.0` floor without relying on toolchain auto-download. The codec submodules are intentionally not included in this loop — they require Go 1.25+ and continue to be tested manually before each tag.

### Notes — Go floor reality vs plan
- The original phase 11 plan anchored on a `go 1.22.0` floor for new plugin modules. After `go mod tidy` the actual floors landed higher because the OTel SDK's transitive deps require newer `go` directives:
  - `plugins/prometheus/go.mod`: `go 1.23.0` (forced by `client_golang` dep tree even with `client_golang` pinned to `v1.19.0`)
  - `plugins/otel/go.mod`: `go 1.25.0` (forced by `go.opentelemetry.io/otel/sdk` dep tree)
- This means observability plugins do NOT support root's `go 1.22.0` floor. The dedicated `test-plugin-submodules` CI job runs on Go 1.25 explicitly. Users of the otel plugin must run their applications on Go 1.25+; users of the prometheus plugin need Go 1.23+. Documented per-plugin so consumers can pick.

### Notes
- `plugins/prometheus` `GroupPath` default returns `c.RoutePattern()` when set, falling back to `c.Path()`. There is deliberately no heuristic ID collapsing (e.g. `/users/123` → `/users/{id}` via regex). Operators handling routes outside the aarv router (mounted handlers, plain `http.Handler` traffic) should supply a custom `GroupPath`.
- `plugins/otel` does not call `span.RecordError` for handler errors. The aarv framework converts handler-returned errors into HTTP responses (via `App.handleError`) before the middleware sees the result, so the otel plugin detects "handler errored" via the 5xx response status — matching the OTel HTTP semantic-convention recommendation.
- `plugins/prometheus` and `plugins/otel` carry `replace github.com/nilshah80/aarv => ../..` in their `go.mod` for local development. This must be lifted at release time so the tagged module bytes can resolve via the Go proxy with a published aarv version.
- The verboselog `LogToSlog` field name was decided against in favor of `SuppressSlog` (zero-value `false`) so that existing `Config{...}` constructions without DefaultConfig inheritance keep their pre-Sink behavior unchanged. The same inversion was applied to the otel plugin's three feature toggles (`SuppressErrorStatus`, `SuppressMetrics`, `SuppressLogAttrs`) so `Config{}` produces all default behaviors without needing `DefaultConfig()` boilerplate.

## [0.5.1] - 2026-04-29

### Fixed
- Data race between `hookRegistry.finalize` (called lazily via `sync.Once` on the first request through `ServeHTTP`) and `hookRegistry.run` for `OnShutdown` (called from `listenAndShutdown` on a separate goroutine). The race was latent in production whenever `Shutdown` was triggered after at least one request had been served, and surfaced under `-race` on Go 1.23 via `TestGracefulShutdownViaExternalCall`. Finalization is now performed eagerly at the top of `listenAndShutdown` via the new internal `App.ensureReady` helper; `ServeHTTP` continues to call the same helper for direct callers (e.g. `httptest`) that bypass the listen loop.

### Codec Submodules
- `codec/jsonv2`, `codec/segmentio`, and `codec/sonic` are re-tagged at `v0.5.1` for version alignment with the core release. No codec source changes in this release; the `v0.5.1` codec module bytes are byte-identical to `v0.5.0`.

## [0.5.0] - 2026-04-29

### Added
- API key authentication plugin (`plugins/apikey`): header- and query-based key lookup (query opt-in), pluggable `Validator`, `StaticKeys` helper that hashes stored and presented keys to fixed-length SHA-256 digests for lookup so the key-length side channel is closed (note: SHA-256 is used here for in-memory side-channel resistance, not at-rest key protection — store key digests externally and use a custom validator for that), identity retrieval via `From(c)` / `FromContext(ctx)`. Registers both stdlib and native middleware paths. Validator must return a non-nil identity on success — `(nil, nil)` is rejected as auth failure because `context.Context` cannot distinguish a stored nil from a missing value.
- Basic authentication plugin (`plugins/basicauth`): RFC 7617 parser (case-insensitive scheme, first-colon split so passwords can contain `:`), pluggable `Validator`, `StaticCreds` helper that hashes stored and attempted passwords to fixed-length SHA-256 digests and compares with `crypto/subtle.ConstantTimeCompare` so the password-length side channel is closed (note: SHA-256 is used here for in-memory side-channel resistance, not at-rest password protection — use bcrypt/argon2 for that), identity retrieval via `From(c)` / `FromContext(ctx)`. Emits `WWW-Authenticate: Basic` on 401 with optional `realm` and `charset` parameters; suppresses the challenge for non-401 statuses (e.g. validator-returned 403). `Realm` is validated at `New()` for header-safe characters; `Charset`, when set, must be `"UTF-8"` (case-insensitive) per RFC 7617 §2.1. Registers both stdlib and native middleware paths.
- JWT authentication plugin (`plugins/jwt`): stdlib-only RFC 7519 implementation supporting HS256/384/512, RS256/384/512, ES256/384/512, and EdDSA. Token lookup from header / query / cookie via an ordered `Lookups` list (default `Authorization: Bearer`). Standard claim validation for `exp`, `nbf`, `iat`, `iss`, and `aud` with configurable `Leeway`. Optional `ClaimsValidator` hook with `*aarv.AppError` honored on both native and stdlib paths. `KeyFunc` receives the parsed JOSE header for `kid`-based dispatch and JWKS-style key rotation; `HMACSecret` is a sugar field for single-secret HS\* deployments. Public helpers: `SignToken`, `Parse`, `RefreshToken`, `From`, `FromContext`, `GetClaims[T]`. Registers both stdlib and native middleware paths.
- Codec benchmark harness (`codec/benchmarks/`) and per-codec READMEs for `codec/jsonv2`, `codec/segmentio`, and `codec/sonic`.

### Changed
- `codec/jsonv2` bumped to Go 1.26 and `github.com/go-json-experiment/json` v0.0.0-20260214004413-d219187c3433.
- `codec/segmentio` bumped to `github.com/segmentio/encoding` v0.5.4.
- `codec/sonic` bumped to `github.com/bytedance/sonic` v1.15.0.

### Notes
- The `plugins/apikey` and `plugins/basicauth` `Validator` signatures return `(identity any, err error)` rather than the `(bool, error)` shape sketched in `docs/tasks-md.md` §6.2/§6.3. The identity-returning shape matches the planned §6.4 Bearer Token validator and lets a single auth pass produce the caller identity without a second lookup.
- `plugins/basicauth` adds `WWW-Authenticate` emission and realm/charset validation on top of the original §6.3 spec. The challenge header is required by RFC 7235 for browsers to actually prompt for credentials, so this is correctness work, not scope creep.
- `plugins/jwt` rejects `"alg":"none"` unconditionally and validates the token's `alg` header against `Config.Algorithms` before any key resolution. The returned key is then type-asserted against the algorithm's required Go type (e.g. RS256 must get `*rsa.PublicKey`); together these close the classic alg-confusion attack.
- `plugins/jwt` enforces strict integer NumericDate values for `exp`, `nbf`, and `iat`: only JSON integers in `[0, 253402300799]` (year-9999 upper bound) are accepted. Fractional, string-shaped, negative, and millisecond-scale timestamps are rejected with `ErrInvalidNumericDate`. This is intentionally stricter than RFC 7519 §2 (which permits non-integer NumericDates) and is documented in the package GoDoc.
- `plugins/jwt` stores claims under a hardcoded `*aarv.Context` key (`jwtClaims`), accessed via `From(c)`. The original §6.1 task wording in `docs/tasks-md.md` allowed a configurable context key; the hardcoded form matches `apikey` / `basicauth` and prevents helper functions from silently missing when callers misconfigure it.
- `plugins/jwt` requires `Algorithms` to be non-empty when only `KeyFunc` is set (no silent HS256 fallback for asymmetric setups). When `HMACSecret` is set and `Algorithms` is empty, `[HS256]` is inferred. Both `KeyFunc` and `HMACSecret` set simultaneously is a configuration error.
- `plugins/jwt` `New(cfg)` panics on misconfiguration (parity with `apikey`/`basicauth`); `Parse` and `RefreshToken` validate the same `Config` but return typed sentinels (`ErrMissingKey`, `ErrNoAlgorithms`, `ErrConflictingKey`, `ErrSecretAlgMismatch`, `ErrInvalidLookup`, `ErrUnknownAlg`) so programmatic callers can branch via `errors.Is` without `recover`.
- `plugins/jwt` `KeyFunc` receives the JOSE header only; issuer-based key selection is not framework-supported because the iss claim is unverified at key-resolution time. Callers needing iss-based dispatch must decode unverified claims themselves.
- `plugins/jwt` `RefreshToken` preserves the verified token's JOSE header verbatim — `kid` and any other custom header parameters carry across the refresh, which is required for JWKS-style key rotation. Only `iat` and `exp` are rewritten on the claim side; `nbf`, `jti`, and all other claims are copied unchanged. The `alg` field is always rewritten from the verified alg to keep header/alg coherence; `typ` defaults to `"JWT"` when absent.

### Codec Submodules
- `codec/jsonv2`, `codec/segmentio`, and `codec/sonic` are tagged at `v0.5.0` alongside the core release; see the `Changed` section above for the per-codec dependency bumps.

## [0.4.4] - 2026-04-13

### Added
- Server-Sent Events (SSE) helper: `c.SSE()` returns an `SSEWriter` with `Send`, `Comment`, `Flush`, `Close`, and `Done` for client-disconnect detection. Multi-line `Data` fields are split across multiple `data:` lines per the SSE spec; `Event` and `ID` reject embedded newlines.
- Secure cookies: HMAC-signed and AES-encrypted cookie helpers in `securecookie.go`, with key rotation support.
- Multipart file upload: new `UploadedFile` API for multipart form handling with size limits and streamed access to file contents.
- Configurable panic recovery: the `recover` plugin now accepts a custom recovery handler so applications can return shaped error bodies on panic.
- Cross-platform graceful shutdown integration test that drives the full drain path via an external `Shutdown(ctx)` call without depending on POSIX signal delivery.
- Plugin integration tests that assert middleware execution, hook firing, nested plugin route mounting, and bidirectional decorator resolution.

### Changed
- Plugins now surface previously unchecked error returns instead of dropping them silently.
- Codebase-wide `gofmt -s` cleanup: struct field alignment, import order, and doc-comment list indentation.

### Codec Submodules
- `codec/jsonv2`, `codec/segmentio`, and `codec/sonic` are re-tagged at `v0.4.4` for version alignment. No source changes in this release.

## [0.4.3] - 2026-04-05

### Changed
- Lock-free ULID generation in the request ID path; reduced contention under high concurrency.
- Buffered header handling in the timeout middleware to avoid duplicate flushes.
- Stack-allocated log attribute slices in hot logging paths.
- Context reset logic and path matching streamlined for fewer allocations.

### Added
- Native middleware path tests covering additional built-in plugins.
- Inlined benchmark writer helper to remove an indirection in the request hot path.

## [0.4.0] - 2026-03-22

### Added
- Native middleware fast path for Aarv-owned middleware and plugins
- Expanded examples for binding, route groups, error handling, custom middleware, and middleware bridge behavior
- Direct lifecycle coverage for `PreRouting`, `PreParsing`, `PreValidation`, `PreHandler`, and `OnError`

### Changed
- Split the old monolithic `aarv.go` responsibilities into smaller core files while keeping `aarv.go` as the main entrypoint file
- Tightened request lifecycle cleanup so `OnSend` always runs before pooled context release
- Improved bind, query, trusted-proxy, hook, redirect, and 405-path hot-path behavior
- Added native plugin paths for logger, request ID, and encrypt middleware
- Removed the public benchmark modules from git history; benchmark numbers in the README now refer to internal/local benchmark harnesses rather than shipped repo modules

### Fixed
- `PreRouting` now fires correctly on the direct fast path
- `OnError` hook routing is centralized through `handleError(...)`
- Grouped exact and grouped dynamic route fast paths now avoid unnecessary fallback behavior
- Plugin wrapper pools for ETag, compression, timeout, and encrypt flows are reset more safely

## [0.3.0] - 2026-03-08

### Added
- Core framework APIs for routing, grouped routes, middleware, hooks, binding, validation, and structured error handling
- Pooled request context, pluggable JSON codecs, graceful shutdown, TLS, HTTP/2, and mutual TLS support
- Test helpers and expanded plugin coverage across the built-in plugin packages

### Changed
- Raised non-example package coverage to 98.7%
- Completed GoDoc coverage for the public API surface
- Hardened nil-handling and panic-recovery paths in the core app and middleware integration
- Improved TLS configuration handling by cloning caller configs, enforcing secure minimums, and honoring HTTP/2 disablement safely
- Redacted sensitive query parameters in the verbose logging plugin in addition to existing header and body redaction

### Security
- Completed an OWASP-focused review of the current framework surface
- Verified that standard and verbose logging avoid exposing secrets by default-sensitive redaction paths
- Ran `govulncheck ./...`; findings are limited to Go standard library issues in `go1.26.0`, fixed upstream in `go1.26.1`

### Performance
- 154K RPS with logger middleware (comparable to Gin/Mach)
- 151K RPS with encryption middleware
- ~82 allocs/op with full request ID tracking
- P50 latency: ~555µs, P99 latency: ~1.9ms

---

## Release Process

1. Update version in this file
2. Commit: `git commit -am "chore: prepare vX.Y.Z release"`
3. Tag: `git tag -a vX.Y.Z -m "Release vX.Y.Z"`
4. Push: `git push origin vX.Y.Z`
5. Create GitHub Release with notes from this file

[Unreleased]: https://github.com/nilshah80/aarv/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/nilshah80/aarv/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/nilshah80/aarv/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/nilshah80/aarv/compare/v0.4.4...v0.5.0
[0.4.4]: https://github.com/nilshah80/aarv/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/nilshah80/aarv/compare/v0.4.0...v0.4.3
[0.4.0]: https://github.com/nilshah80/aarv/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/nilshah80/aarv/compare/v0.1.0...v0.3.0
