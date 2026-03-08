# Aarv v0.3.0

Released: 2026-03-08

## Highlights

- Core framework is now backed by high test coverage across the root package and built-in plugins.
- Public API GoDoc coverage is complete for the non-example library surface.
- Error handling, panic recovery, nil-safety, and concurrent server access paths were audited and hardened.
- Security review is complete, including TLS default handling and secret redaction in logging.

## Included In This Release

- Routing, route groups, middleware chaining, lifecycle hooks, and plugin registration
- Type-safe request binding and validation
- Structured error handling and request-scoped helpers
- TLS, HTTP/2, and mutual TLS support
- Built-in plugins including logging, verbose logging, encryption, compression, CORS, security headers, static files, timeout, recovery, request ID, and health

## Quality Snapshot

- Non-example combined coverage: 98.7%
- Local test suite: `go test ./...` passing
- Non-example race test suite: `go test -race` passing
- `govulncheck ./...` run completed

## Security Note

`govulncheck` reported Go standard library vulnerabilities in `go1.26.0` that are fixed in `go1.26.1`. No project-specific or imported-package vulnerabilities were reported as directly affecting this codebase. Upgrade the Go toolchain to `go1.26.1` or newer as part of release publication.
