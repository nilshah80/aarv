# aarv jsonv2 codec

A JSON codec for [aarv](https://github.com/nilshah80/aarv) backed by
[`github.com/go-json-experiment/json`](https://github.com/go-json-experiment/json),
the experimental v2 JSON package that is the basis for the proposed
`encoding/json/v2` standard library addition.

## When to use it

- You want to track the future direction of Go's standard JSON library.
- You need v2 semantics: stricter defaults, better error messages, and
  cleaner handling of `omitempty`, `omitzero`, inline fields, etc.
- You want a pure-Go codec with no platform restrictions.

## Install

```sh
go get github.com/nilshah80/aarv/codec/jsonv2
```

Lives in its own Go module so the main `aarv` module stays light.

## Usage

```go
import (
    "github.com/nilshah80/aarv"
    "github.com/nilshah80/aarv/codec/jsonv2"
)

app := aarv.New(aarv.WithCodec(jsonv2.New()))
```

## Notes

- The upstream package is **experimental**. Its API and exact behavior may
  shift before it lands in the standard library — pin the version you depend
  on.
- Some v1 quirks are intentionally not preserved; review the
  [v2 README](https://github.com/go-json-experiment/json) for differences if
  you're migrating an existing service.
- Recorded numbers against the other codecs live in
  [`../benchmarks/RESULTS.md`](../benchmarks/RESULTS.md). The suite is its own
  Go module — reproduce with `cd codec/benchmarks && go test -bench=. -benchmem ./...`
  (running from the repo root will not work).
