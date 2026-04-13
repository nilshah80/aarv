# aarv segmentio codec

A drop-in JSON codec for [aarv](https://github.com/nilshah80/aarv) backed by
[`github.com/segmentio/encoding/json`](https://github.com/segmentio/encoding),
a faster, allocation-friendly replacement for `encoding/json` that keeps full
behavioral compatibility with the standard library.

## When to use it

- You want a meaningful speedup over `encoding/json` without changing tags or
  struct shapes.
- You need pure-Go portability (works on every architecture Go supports).
- You don't want JIT warmup or any of sonic's platform constraints.

## Install

```sh
go get github.com/nilshah80/aarv/codec/segmentio
```

This codec lives in its own Go module so the main `aarv` module stays
dependency-free.

## Usage

```go
import (
    "github.com/nilshah80/aarv"
    "github.com/nilshah80/aarv/codec/segmentio"
)

app := aarv.New(aarv.WithCodec(segmentio.New()))
```

That's it — every `c.JSON(...)` and request-body bind on the app now goes
through segmentio's encoder.

## Notes

- Behavior matches `encoding/json`, including struct tags, `json.Marshaler`,
  and `omitempty`.
- Recorded head-to-head numbers against sonic and jsonv2 live in
  [`../benchmarks/RESULTS.md`](../benchmarks/RESULTS.md). The suite is its own
  Go module — reproduce with `cd codec/benchmarks && go test -bench=. -benchmem ./...`
  (running from the repo root will not work).
