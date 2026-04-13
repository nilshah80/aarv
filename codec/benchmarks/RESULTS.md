# Codec Benchmark Results

A representative run of the codec benchmark suite. Treat these numbers as a
rough ordering and an alloc-profile guide, not an absolute ranking — actual
wins/losses vary with payload shape, CPU, and Go version.

## Environment

- **Host:** Apple M4 Max (16 cores)
- **OS / arch:** darwin/arm64
- **Go toolchain:** 1.26.1
- **Per-module Go minimums** (set by each module's `go.mod`, driven by its deps):
  - root `aarv`: **1.22.0** (no external deps)
  - `codec/segmentio`: **1.25.0** (from `segmentio/encoding v0.5.4`)
  - `codec/sonic`: **1.25.0** (from `bytedance/sonic v1.15.0`)
  - `codec/jsonv2`: **1.26** (from latest `go-json-experiment/json`)
  - `codec/benchmarks`: **1.26** (transitively via jsonv2)
- **Command:** `go test -bench=. -benchmem -run=^$ ./...` (from inside
  `codec/benchmarks/`)
- **Payloads:**
  - `small` — a single flat struct (~120 B JSON)
  - `medium` — 10 small records nested in a parent (~2 KB JSON)
  - `large` — 50 medium records (~100 KB JSON)

Times in **ns/op**, lower is better. **Bold** marks the fastest codec per row.

## Marshal (`MarshalBytes(v) → []byte`)

| Payload | stdlib  | segmentio  | sonic   | sonic-fastest | jsonv2  |
| ------- | ------: | ---------: | ------: | ------------: | ------: |
| small   |     335 |    **203** |     490 |           488 |     433 |
| medium  |   3 289 |  **1 893** |   4 387 |         4 406 |   3 947 |
| large   | 164 903 | **94 374** | 230 099 |       230 609 | 194 256 |

## Unmarshal (`UnmarshalBytes(data, v)`)

| Payload | stdlib  | segmentio | sonic       | sonic-fastest | jsonv2  |
| ------- | ------: | --------: | ----------: | ------------: | ------: |
| small   |   1 345 |       791 |         436 |       **428** |     706 |
| medium  |  14 488 |     8 401 |   **3 284** |         3 319 |   7 579 |
| large   | 703 275 |   450 701 | **158 202** |       159 881 | 377 094 |

## Encode (streaming `Encode(io.Writer, v)`)

| Payload | stdlib  | segmentio  | sonic   | sonic-fastest | jsonv2  |
| ------- | ------: | ---------: | ------: | ------------: | ------: |
| small   |     352 |    **199** |     530 |           526 |     457 |
| medium  |   3 484 |  **1 875** |   4 624 |         4 642 |   4 058 |
| large   | 167 981 | **95 473** | 236 481 |       237 116 | 201 001 |

## Decode (streaming `Decode(io.Reader, v)`)

| Payload | stdlib  | segmentio | sonic       | sonic-fastest | jsonv2  |
| ------- | ------: | --------: | ----------: | ------------: | ------: |
| small   |   1 531 |     3 893 |     **512** |           529 |     824 |
| medium  |  15 743 |    12 369 |   **3 642** |         3 735 |   8 049 |
| large   | 767 576 |   561 652 |     181 625 |   **173 212** | 397 457 |

## Takeaways

- **Encoding: segmentio wins across all sizes.** Roughly 2× the stdlib on
  large payloads with ~10× fewer allocations. Sonic's JIT/asm cost does not
  pay off for encoding on this workload.
- **Decoding: sonic dominates.** Roughly 4× stdlib on medium/large payloads.
  `sonic-fastest` is a tie with `sonic` here — validation is not the
  bottleneck for this shape, so the fast preset only helps when input has
  UTF-8/number validation hot spots.
- **segmentio streaming `Decode` is a trap.** It is the slowest of all five
  codecs on small payloads and allocates megabytes on large ones (buffered
  decoder warmup). Prefer its bytes API, or use a different codec for
  decode-heavy paths.
- **jsonv2** is never the winner but is consistently better than stdlib on
  decode and allocation-efficient. Reasonable pure-Go default.

## Picking one

If you can only ship one codec:

| Workload                            | Pick            |
| ----------------------------------- | --------------- |
| Write-heavy API                     | **segmentio**   |
| Read-heavy API (parsing bodies)     | **sonic**       |
| Mixed, single choice                | **sonic**       |
| Need pure-Go portability            | **segmentio**   |
| Want forward-looking stdlib-v2 path | **jsonv2**      |

## Reproducing

```sh
cd codec/benchmarks
go test -bench=. -benchmem -run=^$ ./...
```

The run above took ~83 s wall-clock. Narrow it with a `-bench` pattern, e.g.
`-bench=BenchmarkUnmarshal/large` to exercise only one axis.
