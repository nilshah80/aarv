# aarv sonic codec

A high-performance JSON codec for [aarv](https://github.com/nilshah80/aarv)
backed by [`github.com/bytedance/sonic`](https://github.com/bytedance/sonic),
a JIT-accelerated JSON library from ByteDance. On supported platforms sonic is
typically the fastest JSON option available to Go programs.

## When to use it

- You're CPU-bound on JSON encode/decode and want the absolute fastest option.
- Your deployment targets `amd64` or `arm64` (other architectures fall back to
  a slower path or are unsupported).
- You're willing to accept a heavier dependency (assembly, JIT loader) in
  exchange for throughput.

## Install

```sh
go get github.com/nilshah80/aarv/codec/sonic
```

Lives in its own Go module so the main `aarv` module stays light.

## Usage

```go
import (
    "github.com/nilshah80/aarv"
    "github.com/nilshah80/aarv/codec/sonic"
)

app := aarv.New(aarv.WithCodec(sonic.New()))
```

### `NewFastest` — skip validation

```go
app := aarv.New(aarv.WithCodec(sonic.NewFastest()))
```

`NewFastest` uses `sonic.ConfigFastest`, which trades strict UTF-8 / number
validation for a meaningful speed boost. Use it when you trust your input
sources.

### `Pretouch` — eliminate first-request JIT latency

Sonic JIT-compiles encoders/decoders the first time it sees a type. Call
`Pretouch` at startup with your request and response types so the cost is
paid before traffic arrives:

```go
sonic.Pretouch(MyRequest{}, MyResponse{}, []Item{})
```

Or on a specific codec instance:

```go
c := sonic.New()
_ = c.Pretouch(MyRequest{}, MyResponse{})
```

## Notes

- Supported architectures: `amd64`, `arm64`. On other platforms sonic falls
  back to a reflective path and you should prefer `segmentio` or `jsonv2`.
- Recorded numbers against the other codecs live in
  [`../benchmarks/RESULTS.md`](../benchmarks/RESULTS.md). The suite is its own
  Go module — reproduce with `cd codec/benchmarks && go test -bench=. -benchmem ./...`
  (running from the repo root will not work).
