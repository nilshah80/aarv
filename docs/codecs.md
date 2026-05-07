# Codecs guide

Aarv uses a `Codec` interface for JSON encode/decode operations. The root
module defaults to the standard library codec. Optional codec submodules let
applications trade dependencies, platform constraints, and performance.

## Default codec

The root module requires no third-party JSON dependency.

```go
app := aarv.New()
```

The default codec is the right choice for small services, libraries, and code
that values minimum dependency footprint over raw JSON throughput.

## Using a codec

Install one codec submodule:

```bash
go get github.com/nilshah80/aarv/codec/segmentio@v0.8.0
```

Wire it at app creation:

```go
import "github.com/nilshah80/aarv/codec/segmentio"

app := aarv.New(aarv.WithCodec(segmentio.New()))
```

The codec is used by:

- `c.JSON`
- `c.BindJSON`
- `Bind`
- `BindReq`
- OpenAPI request content type defaults when schema metadata is attached

## segmentio

`codec/segmentio` wraps `github.com/segmentio/encoding/json`.

Use it when:

- you want a measured performance boost without JIT behavior
- you need predictable server startup
- you want broad platform behavior

```bash
go get github.com/nilshah80/aarv/codec/segmentio@v0.8.0
```

```go
app := aarv.New(aarv.WithCodec(segmentio.New()))
```

This is the most conservative performance-oriented default.

## sonic

`codec/sonic` wraps `github.com/bytedance/sonic`.

Use it when:

- the deployment platform is supported by sonic
- JIT warmup and platform constraints are acceptable
- peak JSON throughput matters more than conservative portability

```bash
go get github.com/nilshah80/aarv/codec/sonic@v0.8.0
```

```go
app := aarv.New(aarv.WithCodec(sonic.New()))
```

`sonic.NewFastest()` uses sonic's fastest config and trades some strictness
for speed. Review the codec README before using it for public API input.

## jsonv2

`codec/jsonv2` wraps the Go JSON experiment.

Use it when:

- you are evaluating Go's experimental JSON behavior
- you want to test future compatibility
- you accept experiment churn

```bash
go get github.com/nilshah80/aarv/codec/jsonv2@v0.8.0
```

```go
app := aarv.New(aarv.WithCodec(jsonv2.New()))
```

Review the upstream experiment notes before relying on behavior details.

## Choosing a codec

| Need | Recommended codec |
|---|---|
| Minimal dependencies | default stdlib |
| Conservative performance improvement | `segmentio` |
| Maximum speed on supported platforms | `sonic` |
| Experimenting with future Go JSON | `jsonv2` |

Do not pick a codec only from microbenchmarks. Test representative request and
response structs, payload sizes, validation, and error paths.

## Benchmarks

Codec benchmark harnesses live under:

- root benchmark files for framework-level codec paths
- `codec/benchmarks` for codec package comparisons
- codec README files for package-specific notes

Run:

```bash
go test -bench=. -benchmem ./...
```

For codec submodules:

```bash
cd codec/segmentio
go test ./...
```

## Release model

Codec packages are submodules and use path-prefixed tags:

```bash
go get github.com/nilshah80/aarv/codec/segmentio@v0.8.0
go get github.com/nilshah80/aarv/codec/sonic@v0.8.0
go get github.com/nilshah80/aarv/codec/jsonv2@v0.8.0
```

Keep codec versions aligned with the root release unless there is a deliberate
reason to pin differently.

## Production checklist

- Keep the default codec until JSON cost is visible.
- Benchmark with service-shaped payloads before switching.
- Use `segmentio` as the conservative speed option.
- Validate sonic platform support before deployment.
- Treat `jsonv2` as experimental.
- Test codec submodules from their own module directories before release.

## See also

- [`examples/codec/`](../examples/codec/) — minimal demo with the default stdlib codec
- [`examples/codec-segmentio/`](../examples/codec-segmentio/) — wiring `segmentio.New()` via `aarv.WithCodec`
- [`examples/codec-sonic/`](../examples/codec-sonic/) — wiring `sonic.New()` and `sonic.NewFastest()`
- [`examples/codec-jsonv2/`](../examples/codec-jsonv2/) — wiring `jsonv2.New()`
