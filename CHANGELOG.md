# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added — Phase 12.6: ALP companion plugins

> Target tag: `v0.7.6` for the root module + new submodule tags
> `plugins/hmacauth-redis/v0.7.6`, `plugins/ratelimit-redis/v0.7.6`,
> `plugins/idempotency-redis/v0.7.6`. `plugins/hmacauth` lives in the
> root module and ships under the root tag.

- Core: `WithRouteIdempotencyTTL(d time.Duration) RouteOption` — overrides the idempotency middleware's global TTL for a specific route. Lookup is keyed on `(Method, RoutePattern())`, so all four dispatch paths (App fast, App dynamic, Group fast, Group dynamic) are supported with no per-path plumbing. `RouteInfo.IdempotencyTTL *time.Duration` exposes the value via `App.Routes()` for introspection. Negative durations panic at construction; a registered zero is honored as the per-route caching opt-out signal. Request-time accessor: `(*Context).RouteIdempotencyTTL() (time.Duration, bool)`. `App.routeIdempotencyTTL` index is read-only after the App starts serving.
- `plugins/hmacauth` (root module, stdlib-only): HMAC-SHA256 signed-request authentication. Verifies that every protected request was signed by a known client and is fresh (within `SkewSeconds`, default 300) and not a replay (within `NonceTTL`, default `2*SkewSeconds + 60s`). Canonical request bytes are exactly `METHOD\nPATH\nCANONICAL_QUERY\nHEX(SHA256(body))\nTIMESTAMP\nNONCE`; canonical query encodes per RFC 3986 §2.3 (uppercase `%HH`, NOT `application/x-www-form-urlencoded` — `url.QueryEscape` would silently break interop). All failure modes (missing header, malformed timestamp, skew, unknown client, bad signature, malformed signature hex, replay) collapse to a generic `401` so no information leaks externally; body overflow returns `413`. Body cap defaults to 1 MiB (parity with `plugins/idempotency`); `MaxBytesReader` is used on the native path so the `bodylimit` plugin's response shape is preserved. Re-injects body via `(*Context).SetBody` so downstream `aarv.Bind` still works. `Client { ClientID; Secret []byte; Secrets [][]byte; Identity any }` supports rotation: `compareAllSecrets` iterates every candidate without short-circuiting (timing-safe), and a missing client returns the zero `Client` rather than a placeholder so per-request HMAC cost does not depend on whether the client ID exists. `From(c) (Client, bool)` / `FromContext(ctx) (Client, bool)`. `StaticClients(map[string]Client) Validator` defensively copies secret slices at snapshot time. `NonceStore` interface: `SetNX(ctx, key, ttl) (fresh bool, err error)`. `MemoryNonceStore` for dev/single-process; `NewMemoryNonceStoreWithJanitor` runs a periodic eviction goroutine and returns a stop function for `app.OnShutdown`. Configuring `NonceStore = nil` disables replay protection and emits a one-time `slog.Warn`. Custom `ErrorHandler`, `Skipper`, configurable header names. Native and stdlib middleware paths registered via `aarv.RegisterNativeMiddleware`. Sentinel `errStop` keeps the stdlib short-circuit clean.
- `plugins/hmacauth` test vectors and client signer: `testdata/vectors.json` covers empty body, ASCII body, UTF-8 body, binary body, single+repeated query params, RFC 3986 vs form-urlencoded divergence, long path, body straddling SHA-256 64-byte block boundary, and GET/POST/PATCH/DELETE methods. `Vectors() []Vector` exposes the parsed slice. `Sign(req, client, body, now, nonce)` attaches the four signed-request headers in place. `Transport(client, opts...)` returns an `http.RoundTripper` that signs every outbound request — body must be replayable via `req.GetBody` (typed bytes/strings readers populate this automatically; `io.Pipe`-backed bodies fail with a clear error). `WithTransportNow`, `WithTransportNonce`, `WithTransportBase`, `WithTransportHeaders` for test injection and header-name overrides. Default nonce source: 16 bytes from `crypto/rand`, hex-encoded. Redirect policy lives on `http.Client.CheckRedirect` (where the stdlib expects it): `FailOnRedirect` returns `http.ErrUseLastResponse` so the caller observes the 3xx instead of silently following an unsigned redirect (recommended default); `ResignOnRedirect(client)` re-signs against the redirect target's URL+body via `req.GetBody`. `TestVectors_Update` regenerates expected signatures (`go test -update-vectors`); `TestVectors_NoPlaceholders` is the CI gate. Fuzz: `FuzzCanonicalQuery` checks determinism + RFC 3986 byte-set conformance.
- `plugins/idempotency` refinements (Track E2):
  - `CachedHeaders []string` (additive; nil-vs-empty contract): default allowlist `Content-Type`, `Content-Encoding`, `Cache-Control`, `Location`, `ETag`. Empty non-nil drops every header. `Set-Cookie`, `Authorization`, `WWW-Authenticate`, `X-Request-Id`, and the standard hop-by-hop set are ALWAYS stripped — the allowlist cannot opt back in to these. Filtering applied at both capture (`captureWriter.Snapshot`) and replay (`replayHeadersTo`) so cached entries written by an older middleware version are still safely replayed.
  - `CacheStatusFunc func(status int) bool` (additive): when non-nil, replaces `CacheStatuses` as the per-status caching predicate. Lets ALP express "cache 2xx, plus deterministic 4xx like 409, never 5xx" without enumerating every status.
  - Per-route TTL: `aarv.WithRouteIdempotencyTTL(d)` overrides the middleware's global TTL for that route. Resolved via `(*Context).RouteIdempotencyTTL()`. A registered zero TTL is the per-route caching opt-out (response is returned but not persisted) — the writer captures the response, but `shouldCacheForRequest` returns false before `Save`, so no Save is performed.
  - Payload-mismatch error code `idempotency_key_reused_with_different_payload` (exposed as `idempotency.PayloadMismatchErrorCode`): emitted on 422 responses when `HashRequestBody` is on and a retry's body differs. Earlier behavior used the generic `unprocessable_entity` code from `codeForStatus`; the specific code lets clients reliably distinguish payload drift from other 422s. Both native and stdlib paths emit the contract code; a custom `ErrorHandler` still receives the message but bypasses can fall through to the framework JSON shape carrying the code.
  - Native vs stdlib parity: every behavior change above lands in both `handleNative` and `handleStdlib`; refinement tests assert byte-equal responses across paths.
  - Documented order: `requestid → recover → hmacauth → idempotency → handler`. Idempotency runs AFTER hmacauth so the lock key is derived from an authenticated request — otherwise an unauthenticated caller could pollute the lock space with arbitrary keys.
- `plugins/hmacauth-redis` (separate submodule, requires `github.com/redis/go-redis/v9`): Redis-backed `hmacauth.NonceStore` for multi-instance deployments. `SetNX` uses the atomic `SET key value NX EX ttl` primitive. Configurable `KeyPrefix` (default `aarv:hmacauth:nonce:`). `Config.Client` required (panics on nil — silently degrading to a no-op store would defeat replay protection). Rejects `ttl <= 0` at the boundary so configuration drift surfaces immediately. Compile-time interface check `var _ hmacauth.NonceStore = (*Store)(nil)`.
- `plugins/ratelimit-redis` (separate submodule, requires `github.com/redis/go-redis/v9`): Redis-backed token bucket via a single atomic Lua script (read → refill → decrement → reset). Configurable Limit/Window/Burst, KeyFunc (default `(*aarv.Context).RealIP()`), SkipPaths, Skipper, custom LimitHandler, KeyPrefix (default `aarv:ratelimit:`), StatusCode/Message. Header parity with `plugins/ratelimit`: `X-RateLimit-Limit/Remaining/Reset` set on every admitted and denied request, `Retry-After` (seconds, ≥1) on 429. **Redis-error policy is fail-closed by default** (returns 503 with code `rate_limit_unavailable`) — silent disablement on transient outages is the wrong default for auth-tied rate limit groups. `FailOpenOnRedisError: true` opts in to admit-on-error. The fail-open path admits and forwards without setting rate-limit headers, since their values would not reflect real bucket state. Decision is parallel to `plugins/ratelimit` (not a refactor of the in-process limiter) so existing callers are unaffected.
- `plugins/idempotency-redis` (separate submodule, requires `github.com/redis/go-redis/v9`): Redis-backed `idempotency.Store` + `idempotency.WaitableStore`. Lock and response live in separate keys (`lock:<key>` / `resp:<key>`) so a handler that errors before writing a response unlocks without leaving a phantom cached response. Lock acquired with configurable `LockTTL` (default 30s) so a crashed process cannot pin a key forever. Response is JSON-encoded (status, headers, base64 body, payload hash). `Wait` uses Redis pub/sub on a per-key channel with an immediate `Get` short-circuit to close the subscribe-after-publish race. Malformed cached entries are treated as a cache miss (not a 500) so a future schema version cannot break older middleware. Compile-time interface checks for both `Store` and `WaitableStore`.

### Notes — Phase 12.6

- The `plugins/idempotency` refinements are additive and backwards-compatible. Two existing tests (`TestReplay_VerbatimResponse`, `TestNativeAndStdlib_ProduceIdenticalReplay`) updated to opt-in to custom-header caching via `CachedHeaders: []string{"Content-Type", "X-Custom"}` — under the new default allowlist, custom application headers are no longer persisted unless explicitly listed. This is the documented behavior shift; no API changed.
- `RouteInfo.IdempotencyTTL` is `*time.Duration` (pointer) so "unset" is distinguishable from "set-to-zero". Distinguishing matters because zero is the caching opt-out signal — collapsing them would silently disable caching on every route that omitted the option.
- `plugins/hmacauth` `Transport` does NOT own redirect policy. A `RoundTripper` cannot cleanly intercept redirects — `http.Client.CheckRedirect` does. Set `Client.CheckRedirect = hmacauth.FailOnRedirect` for the recommended default; use `ResignOnRedirect(client)` only when transparent redirect following is genuinely required.
- All three new submodules carry `replace github.com/nilshah80/aarv => ../..` for local development. Lifted at release time so tagged module bytes can be fetched via the Go proxy with a published aarv version. Same release dance as Phase 11/12.
- Redis dependency: `github.com/redis/go-redis/v9 v9.7.3`. Earlier versions (v9.7.0 → v9.7.2) carry GO-2025-3540 (potential out-of-order responses when `CLIENT SETINFO` times out during connection establishment); v9.7.3 fixes it. govulncheck is clean across all three submodules at v9.7.3.

### Release plumbing — Phase 12.6

- Release sequence: tag `v0.7.6` (root) first, then `plugins/hmacauth-redis/v0.7.6`, `plugins/ratelimit-redis/v0.7.6`, `plugins/idempotency-redis/v0.7.6`. `plugins/hmacauth` is in the root module and has no separate tag.
- Before tagging the submodules: lift `replace github.com/nilshah80/aarv => ../..` directives, bump each submodule's `require github.com/nilshah80/aarv` line to `v0.7.6`, run `go mod tidy`, re-run `go test -race ./...` without the replace directive (catches "works locally, broken on the proxy" failure mode), then tag.
- After tagging: from a temp directory outside the repo, verify `go list -m github.com/nilshah80/aarv@v0.7.6` and the submodule paths resolve via the Go proxy.
- Restore `replace` directives in a `chore: restore plugin replace directives for local dev` commit (matches the post-release pattern used in 0.7.5).

## [0.7.5] - 2026-05-02

### Added — Phase 12: TLS helpers + h2c

- Core: `App.ListenServer(srv *http.Server, serve func() error, protocol string) error` — public lifecycle entry point so plugin packages constructing custom `*http.Server` (autocert, h2c) inherit the framework's OnStartup/banner/serve/signal/OnShutdown/Shutdown sequence verbatim. New sentinels `ErrNilServer` / `ErrNilServeFunc`. Existing `Listen` / `ListenTLS` / `ListenMutualTLS` refactored to delegate. The `protocol` argument is display text only (banner + startup log).
- Core: `App.TLSConfig() *tls.Config` and `App.MutualTLSConfig() *tls.Config` — exported helpers returning `tls.Config.Clone()` of the framework's hardened TLS config (`MinVersion >= TLS 1.2`; `WithDisableHTTP2` forces exact `["http/1.1"]` rather than filtering "h2"). Plugins building their own server start from these so framework-wide TLS hardening applies.
- Core: `App.applyServerTLSPolicy` (internal) sets `srv.TLSNextProto` to an empty map when `WithDisableHTTP2` is set, blocking `net/http`'s implicit HTTP/2 auto-config. Respects caller-supplied `TLSNextProto`.
- Core: `WithCertReload(interval time.Duration)` — opt-in cert/key hot-reload for `ListenTLS` / `ListenMutualTLS`. Polls `(ModTime, Size)` of the cert/key passed to the listener and re-loads on change. Default interval 30s, minimum 1s applied after defaulting. New `CertReloader` type (one-shot lifecycle: `Start(ctx) error`, `Stop()`, `GetCertificate(...)`); sentinels `ErrReloaderStarted`, `ErrReloaderStopped`, `ErrCertReloadConflict`, `ErrCertReloaderEmpty`. Stat-before-load on construction so a fast cert replacement between stat and load cannot be missed by the next poll. Polling goroutine transitions state to `stopped` on ctx-cancel exit so a future `Start` correctly returns `ErrReloaderStopped`. Malformed reload preserves the previous certificate and logs WARN. For `ListenMutualTLS` the server cert/key reload; the client CA file is loaded once at startup. Conflict with caller-supplied `TLSConfig.GetCertificate` returns `ErrCertReloadConflict` from the listener call. WARN once when set on plain `Listen` (HTTP).
- Core: `OnStartup` hooks now sort by priority before running (matching every other phase). Previously fired in registration order because `ensureReady`'s sort happened after OnStartup.
- Lifecycle correctness: signal-path order is `serve-start → OnShutdown → srv.Shutdown → serve-return → cleanup` (cleanup runs AFTER the listener has fully drained so transport-coupled resources cannot race in-flight handshakes). Cleanup also runs on OnStartup failure so cert reloader goroutines do not leak.
- `plugins/autocert` (separate submodule, requires `golang.org/x/crypto`): wraps Let's Encrypt / ACME via `golang.org/x/crypto/acme/autocert` and integrates it into the aarv lifecycle. `Manager(cfg) (*autocert.Manager, error)` constructs a configured manager (HostPolicy required — returns `ErrHostPolicyRequired`); `Listen` and `ListenWithManager` run the HTTPS server through `app.ListenServer`. `ConfigureTLS` hook for caller-controlled TLS tunables; framework hardening (TLS 1.2 floor, `WithDisableHTTP2` policy) re-applied after the hook so it cannot weaken security. `ACMEChallengeHandler` interface lets `RedirectHandler` accept a fake during tests without pulling the concrete manager. `RedirectConfig` / `RedirectHandler` / `RedirectServer` / `ListenRedirect` for the HTTP-01 challenge + HTTP→HTTPS redirect listener; control-char host validation, bare-IPv6 bracketing for `Location` headers, default port stripping (`:80` and `:443` omitted), conservative slowloris-resistant timeout defaults (5s ReadHeaderTimeout, 10s ReadTimeout, 60s IdleTimeout — set to a negative duration to disable). Sentinels `ErrHostPolicyRequired`, `ErrNilManager`, `ErrNilApp`. ACME `DirectoryURL` wired via `acme.Client`; empty leaves `mgr.Client` nil. Cache directory created `os.MkdirAll(dir, 0700)` (best-effort across platforms).
- `plugins/h2c` (separate submodule, requires `golang.org/x/net`): HTTP/2-cleartext for internal-mesh / sidecar deployments where TLS terminates upstream. `Wrap(h http.Handler, cfg) (http.Handler, error)` — wraps any handler. `Listen(app, addr, cfg) error` — runs the App through `app.ListenServer`. `MaxFirstRequestBytes` (default 1 MiB) bounds the first-request memory exposure documented by `x/net/http2/h2c`; negative disables. `MaxReadFrameSize` validated against RFC 7540 §6.5.2 [16384, 16777215]; out-of-range returns `ErrInvalidFrameSize` BEFORE any lifecycle hook runs. Documents the cleartext threat model (internal-only, behind trusted TLS terminator) and the `WriteTimeout`-vs-streaming caveat. Sentinels `ErrInvalidFrameSize`, `ErrNilHandler`, `ErrNilApp`.

### Added — Phase 12.5: OpenAPI generator + viewers

- Core: `RouteInfo` extended with `Summary`, `OperationID`, `RequestType`, `ResponseType`, `Responses`, `RequestContentType` so introspection consumers (chiefly the OpenAPI plugin) read everything they need without re-deriving from the handler closure. `App.Routes()` now returns a deep copy (slice + per-element `Tags` + `Responses` map) — callers can freely mutate the result without corrupting framework state. `RequestType` / `ResponseType` are immutable `reflect.Type` and intentionally shared.
- Core: `WithSchema(req, res any) RouteOption` (panics on `(nil, nil)` at construction), `WithSchemaTypes(req, res reflect.Type) RouteOption` (precise escape hatch), `WithResponse(status int, description string)` (status validated 100..599 at construction), `WithRequestContentType(ct string)`. Pointer types are unwrapped to value types; schemas always represent `T`, never `*T`.
- Core: `BindRoute[Req, Res any](app, method, pattern, fn, opts...)` and `BindGroupRoute[Req, Res any](g, method, pattern, fn, opts...)` — typed convenience helpers that auto-attach `WithSchemaTypes` derived from the type parameters via `reflect.TypeFor`. Free functions (Go does not support generic methods).
- Core: `App.CodecContentType() string` accessor. `routeInfoFromConfig` uses it as the default `RequestContentType` so an App configured with `WithCodec(NonJSONCodec{})` generates a spec that declares the right media type without per-route overrides.
- `plugins/openapi` (separate submodule, stdlib only for the JSON path; pulls `sigs.k8s.io/yaml` for the YAML endpoint): generates an OpenAPI 3.1 / JSON Schema 2020-12 spec from RouteInfo. `New(app, cfg) (*Plugin, error)` registers `/openapi.json` and `/openapi.yaml` (defaults overridable via `Config.JSONPath` / `YAMLPath`; both auto-added to the `Exclude` list so the spec does not document its own endpoints). Lazy build via `sync.Once` on first request; cached for App lifetime. Component dedup keyed by `reflect.Type` identity into `#/components/schemas/{Name}` with placeholder-pattern cycle detection (recursive types terminate). Component naming: bare `TypeName` first, sanitized `pkgpath_TypeName` on collision, numeric suffix as last resort. Anonymous structs inlined. `validate:""` tag mapping for `required`, `min`/`max`/`gte`/`lte`/`gt`/`lt`/`len`, `oneof`, `email`, `url`, `uuid`, `regex`, `unique` (unknown rules ignored with `slog.Debug`); `validate:"required"` overrides JSON `omitempty`. Nullable fields rendered in JSON Schema 2020-12 union form (`type: ["X","null"]`, or `oneOf: [{$ref}, {type: null}]` for refs) — the deprecated 3.0 `nullable: true` keyword is never emitted. Catch-all aarv path patterns `{name...}` normalize to `{name}`. `Config.SecuritySchemes` populates `components.securitySchemes`. `Config.Include` (when non-nil) is the SOLE filter; `Exclude` is path-prefix matching otherwise. `DefaultExclude = ["/openapi.json", "/openapi.yaml", "/docs", "/redoc"]`. Custom `JSONPath` / `YAMLPath` are auto-added to `Exclude` when their endpoints are enabled. Request and response media types follow `App.CodecContentType()` by default; per-route `WithRequestContentType` overrides. Sentinels `ErrNilApp`. Test seam `jsonToYAMLFn` exposes the otherwise-unreachable conversion-failure branch.
- `plugins/openapi-ui` (separate submodule, stdlib + `embed`): mounts Swagger UI and ReDoc viewers against the spec. `Mount(app, cfg) error` registers `/docs` and `/redoc` (and their `/static/` asset subtrees); `SwaggerHandler` / `ReDocHandler` for callers wiring routes manually. `SwaggerStaticFS` / `ReDocStaticFS` accessors expose the embedded dist for custom mounts. HTML escaping on all caller-controlled fields (`Title` / `SpecURL` / `staticBase`). `SkipMount = "-"` sentinel suppresses a viewer (empty path is consumed by `applyDefaults`). Vendored real upstream bundles (Swagger UI v5.17.14 Apache 2.0, ReDoc v2.1.5 MIT) per `ASSETS.md`; both LICENSE files are tested for existence to catch accidental removal during asset updates. CSP-friendly: Swagger UI initializer is served as an external script (`script-src 'self'` is sufficient — no per-script-hash maintenance burden).

### Notes — Phase 12

- `App.ListenServer` lifecycle order is signal-aware: on signal, `OnShutdown` hooks fire while the listener is still open (preserves pre-PR-12.0 hook semantics — hooks may emit "shutting down" notices or drain dependencies that themselves rely on the service still accepting traffic), then `srv.Shutdown` drains in-flight requests, then transport-coupled `cleanup` runs. On serve-return path, the same order applies but `srv.Shutdown` is a no-op.
- The `protocol` argument to `ListenServer` is display text only — it appears in the banner and the startup log line. It has no transport semantics; routing through it is purely cosmetic.
- `App.Routes()` returning a deep copy is a behavior change from prior versions, but non-breaking: callers that previously mutated the result were already in undefined-behavior territory.
- Test seams `listenServerSignalNotify`, `listenServerSignalStop`, `newCertReloader` (root) and `jsonToYAMLFn` (openapi) and `userCacheDir` (autocert) are package-private vars overridable in unit tests via `t.Cleanup`. They are not part of the public API and must not be reassigned at runtime in production code (no internal locking).
- `plugins/openapi-ui` ships real upstream Swagger UI / ReDoc dist bundles; the `swagger-initializer.js` is custom (reads spec URL from a `data-spec-url` attribute on its own script tag) so the CSP stays at `script-src 'self'` even when the dist is updated. Update procedure documented in `plugins/openapi-ui/ASSETS.md`.
- All four new submodules carry `replace github.com/nilshah80/aarv => ../..` for local development. Lifted at release time so tagged module bytes can be fetched via the Go proxy with a published aarv version. Same release dance as Phase 11.

### Release plumbing

- New `examples/`: `autocert-letsencrypt` (Let's Encrypt staging by default with explicit production switch), `cert-hot-reload` (with `gencerts.sh` for self-signed cert generation), `h2c-internal` (server + client snippet), `openapi-spec` (smoke-tested end-to-end). All examples are CI-excluded per the existing root-test grep filter.
- New `docs/`: `tls.md` (defaults / `WithCertReload` / mTLS / HSTS placement / autocert / h2c / OCSP non-claim / lifecycle), `openapi.md` (quickstart / metadata sources / validate-tag mapping / required-field precedence / components / nullable encoding / catch-all / security schemes / non-goals).
- README plugin table reorganized into three columns (root features / root plugins / submodule plugins).
- CI auto-discovers new submodules via the existing `test-plugin-submodules` job — no workflow edit required.

## [0.7.0] - 2026-05-02

### Added — Phase 10 Security Plugins
- IP filter plugin (`plugins/ipfilter`, root module, stdlib-only): allowlist or denylist filtering against a set of CIDRs (or bare IPs auto-converted to /32 / /128). Default source IP is `(*aarv.Context).RealIP()`; overridable via `IPFunc` for proxy fronts. Invalid CIDRs panic in `New` (parity with jwt). Empty CIDRs in `ModeAllowlist` panics — an empty allowlist would block all traffic, almost always a misconfiguration. Empty/unparseable source IP fails closed in allowlist mode and fails open in denylist mode (documented).
- Throttle plugin (`plugins/throttle`, root module, stdlib-only): bounds in-flight request count via a `chan struct{}` semaphore with optional bounded queue. Queue token is released as soon as the goroutine acquires a slot or its wait times out — never held for the duration of the handler — so queue depth is exactly `QueueSize` regardless of handler latency. Slot release is deferred so handler errors and panics don't leak slots; release fires before the panic propagates so Recovery middleware behavior is unchanged. Returns 503 (configurable) on queue full or wait timeout.
- Rate limit plugin (`plugins/ratelimit`, root module, stdlib-only): token-bucket (default) and sliding-window algorithms over a sharded keyed store. Default key is `(*aarv.Context).RealIP()`; custom `KeyFunc` for per-user / per-API-key limits. Headers `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` always set; `Retry-After` set on 429. **No goroutines started in `New(cfg)`**: stale entries are pruned in-line via a deterministic `atomic.Uint64` counter — every Nth call sweeps one shard, cycling through all shards over time. `NewWithCleanup(cfg)` returns a periodic janitor goroutine plus a stop function, wired via `app.OnShutdown`. Sweep counter advances on every limiter check (admitted + denied), so denial pressure also drives cleanup.
- CSRF plugin (`plugins/csrf`, root module, stdlib-only): double-submit cookie pattern with `crypto/subtle` constant-time comparison over base64-decoded tokens. Default `CookieHTTPOnly: false` for SPA/API ergonomics (the classic double-submit pattern requires JS to copy the cookie into the request header); server-rendered apps can set `CookieHTTPOnly: true` and read `csrf.Token(c)` to inject into HTML/meta/form. `SafeMethods` follows nil-vs-empty semantics: `nil` → defaults `{GET, HEAD, OPTIONS, TRACE}`; `[]string{}` → every method requires a token. Optional `FormField` fallback for non-AJAX forms.
- Sanitize plugin (`plugins/sanitize`, **separate submodule** — pulls `golang.org/x/text/unicode/norm`, root stays zero-dep): JSON request-body sanitizer. Recursively walks decoded JSON, applying stdlib HTML stripping, NFC Unicode normalization, and any caller-supplied `SanitizerFunc`s, then replaces `r.Body`. Field allowlist/blocklist, configurable Content-Types, body-size cap with 413 response on overflow. Invalid JSON passes through unchanged — sanitizer is not a body validator.
- Idempotency plugin (`plugins/idempotency`, root module, stdlib-only): RFC-aligned `Idempotency-Key` middleware. First request locks the key, captures the response (status + headers + body), saves under the key with TTL, and returns it. Retries replay verbatim with `Idempotency-Replayed: true` response header. Capture writer has an explicit overflow state machine: under-cap responses never write to the underlying writer until the middleware commits them after Save (so Save failure can decline to commit); over-cap responses transition to passthrough mid-stream, flush captured headers + buffered prefix with `Idempotency-Cached: false; reason=size`, and forward the rest unchanged — bounded memory regardless of response size. `MemoryStore` implements both the minimal `Store` interface and the optional `WaitableStore` extension; `ConflictWait` uses `WaitableStore` when available and falls back cleanly to `ConflictReject` (immediate 409, no polling) for non-waitable stores. `SafeMethods` and `CacheStatuses` both follow nil-vs-empty semantics, allowing GET to participate in idempotency when explicitly requested.

### Notes — Phase 10
- `SafeMethods` and `CacheStatuses` (idempotency), and `SafeMethods` (csrf), all use a deliberate `nil` vs `[]T{}` distinction. `nil` means "use built-in defaults"; an empty non-nil slice means "no defaults — honor verbatim". This is the only way to opt every method into idempotency or CSRF protection. Three dedicated tests per slice assert each form behaves correctly.
- `idempotency.ConflictWait` requires the configured `Store` to implement `WaitableStore`. Stores that don't are treated identically to `ConflictReject`: they return 409 immediately on contention. There is no polling fallback — that would invite busy waits or timing bugs. `MemoryStore` implements both interfaces from day one; future Redis/Postgres stores can opt in by adding a single `Wait(ctx, key)` method without breaking the minimal `Store` contract.
- `ratelimit.New(cfg)` and `idempotency.NewMemoryStore()` start no background goroutines. Cleanup is lazy and in-line. Callers wanting a periodic janitor use `ratelimit.NewWithCleanup(cfg)` or `idempotency.NewMemoryStoreWithJanitor(sweep)` — both return a stop function meant to be wired into `app.OnShutdown`. There is no `Plugin()` constructor for either, matching the existing `bodylimit` / `cors` / `secure` / `etag` / `timeout` / `recover` shape.
- `plugins/sanitize` is the only Phase 10 plugin that lives outside the root module. NFC normalization needs `golang.org/x/text/unicode/norm`, and the root module's strict zero-dependency policy disallows that import. The submodule joins the existing `plugins/prometheus` and `plugins/otel` release dance: `replace github.com/nilshah80/aarv => ../..` during development, lifted at tag time.
- Distributed implementations of ratelimit and idempotency (Redis, Postgres) are intentionally **deferred to a later release** (v0.7.x or v0.8.0). The `Store` / `WaitableStore` contract for idempotency, and the (TBD) backend interface for ratelimit, need real production traffic against the in-memory implementations before the contracts can be locked down.

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
