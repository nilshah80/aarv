# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/nilshah80/aarv/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/nilshah80/aarv/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/nilshah80/aarv/compare/v0.4.4...v0.5.0
[0.4.4]: https://github.com/nilshah80/aarv/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/nilshah80/aarv/compare/v0.4.0...v0.4.3
[0.4.0]: https://github.com/nilshah80/aarv/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/nilshah80/aarv/compare/v0.1.0...v0.3.0
