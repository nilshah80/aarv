# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/nilshah80/aarv/compare/v0.4.4...HEAD
[0.4.4]: https://github.com/nilshah80/aarv/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/nilshah80/aarv/compare/v0.4.0...v0.4.3
[0.4.0]: https://github.com/nilshah80/aarv/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/nilshah80/aarv/compare/v0.1.0...v0.3.0
