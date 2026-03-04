# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial framework implementation
- Type-safe request binding with generics (`Bind[Req, Res]`)
- Multi-source binding (path, query, header, cookie, body, form)
- Built-in validation engine with struct tags
- Fastify-style lifecycle hooks (OnRequest, PreHandler, OnResponse, etc.)
- Scoped plugin system with decorators
- Pooled Context with `sync.Pool` for minimal GC pressure
- Pluggable JSON codec support (encoding/json, segmentio, sonic, json/v2)
- Standard middleware compatibility (`func(http.Handler) http.Handler`)
- Graceful shutdown with signal handling
- TLS / HTTP/2 / mTLS support

### Plugins
- `plugins/logger` - Request logging middleware using slog
- `plugins/verboselog` - Full request/response dump logging with sensitive data redaction
- `plugins/encrypt` - AES-256-GCM encryption middleware for request/response bodies
- `plugins/securityheaders` - Security headers middleware (HSTS, CSP, etc.)

### Codecs
- `codec/segmentio` - High-performance JSON using segmentio/encoding
- `codec/sonic` - Fastest JSON using bytedance/sonic (amd64 only)
- `codec/jsonv2` - Modern JSON using go-json-experiment/json

## [0.1.0] - Unreleased

Initial public release.

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
