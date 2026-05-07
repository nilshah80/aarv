# Release policy

Aarv is pre-1.0, but releases should still be predictable for adopters. This
policy describes how root, plugin, and codec modules are versioned and what
compatibility expectations users should have.

## Module layout

| Area | Module | Tag shape |
|---|---|---|
| Core framework and root plugins | `github.com/nilshah80/aarv` | `v0.8.0` |
| Plugin submodules | `github.com/nilshah80/aarv/plugins/<name>` | `plugins/<name>/v0.8.0` |
| Codec submodules | `github.com/nilshah80/aarv/codec/<name>` | `codec/<name>/v0.8.0` |

Root plugins are stdlib-only and ship with the root module. Submodule plugins
and codecs carry third-party dependencies in their own `go.mod`.

## Version alignment

Normal releases should align root, plugin submodules, and codec submodules to
the same version number.

```text
v0.8.0
plugins/openapi/v0.8.0
plugins/prometheus/v0.8.0
codec/segmentio/v0.8.0
```

This keeps `go get` instructions simple and helps downstream services reason
about compatibility.

## Compatibility expectations

Even before v1.0, avoid breaking users unnecessarily.

Patch releases should be limited to:

- bug fixes
- documentation updates
- test and CI improvements
- dependency patch bumps in submodules
- security fixes that preserve API shape where possible

Minor releases may include:

- new plugins
- new route options
- new config fields
- new helpers
- behavior changes that are additive or opt-in

Breaking changes should be rare and clearly documented. Examples:

- removing or renaming exported APIs
- changing default security behavior in a way that can reject existing traffic
- changing middleware ordering requirements
- changing wire response shapes for existing errors
- changing route metadata semantics used by OpenAPI consumers

## Root dependency rule

The root module must remain zero external runtime dependencies. If a feature
needs third-party packages, put it in a submodule under `plugins/` or `codec/`.

Examples:

- `plugins/prometheus` depends on Prometheus client libraries.
- `plugins/otel` depends on OpenTelemetry packages.
- `plugins/sanitize` depends on `golang.org/x/text`.
- `codec/segmentio`, `codec/sonic`, and `codec/jsonv2` depend on alternate
  JSON implementations.

## Release preparation

Before tagging a release:

1. Update `CHANGELOG.md`.
2. Ensure root docs and package GoDoc match behavior.
3. Align submodule `require github.com/nilshah80/aarv` versions to the new root
   tag.
4. Run root tests.
5. Run submodule tests for every plugin and codec being released.
6. Run race tests for concurrency-sensitive areas.
7. Run vulnerability checks.
8. Tag root first.
9. Tag plugin and codec submodules with path-prefixed tags.
10. Push tags.
11. Create the GitHub release from the root tag and mention submodule tags.

## Verification commands

Root:

```bash
go test ./...
go test -race ./...
govulncheck ./...
```

Submodules:

```bash
for d in plugins/*/go.mod codec/*/go.mod; do
    (cd "$(dirname "$d")" && go test ./...)
done
```

External module resolution check:

```bash
go list -m github.com/nilshah80/aarv@v0.8.0
go list -m github.com/nilshah80/aarv/plugins/openapi@v0.8.0
go list -m github.com/nilshah80/aarv/codec/segmentio@v0.8.0
```

## GitHub release notes

A root GitHub release should include:

- summary of user-facing changes
- compatibility notes
- migration notes when needed
- verification summary
- list of submodule tags published with the release

Do not bury breaking changes under feature bullets. Put them in a dedicated
compatibility or migration section.

## Production support policy

For adopters, document:

- supported Go versions
- whether a plugin is root or submodule
- required middleware order for security-sensitive plugins
- storage/backend assumptions for Redis-backed plugins
- known buffering behavior for response-modifying middleware

This information should live in docs before the release is tagged.
