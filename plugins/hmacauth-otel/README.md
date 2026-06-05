# aarv hmacauth-otel plugin

OpenTelemetry adapter for [`plugins/hmacauth`](../hmacauth/)'s `Observer`
hook. Emits one `auth.HMAC.verify` span per verification attempt with
the canonical attribute schema:

| Attribute | Value | When set |
|---|---|---|
| `auth.client_id` | `Event.ClientID` | Always |
| `auth.outcome` | `string(Event.Outcome)` | Always |
| `auth.response_status` | `Event.Status` | Only when non-zero |
| `auth.skew_seconds` | `Event.SkewSeconds` | Only when `Event.Outcome == OutcomeClockSkew` |

Span status is set to `codes.Error` on any outcome other than
`OutcomeOK`, so `{ name = "auth.HMAC.verify" && status = error }` works
as a Tempo / Honeycomb / Grafana filter.

Lives in its own Go module:

```
go get github.com/nilshah80/aarv/plugins/hmacauth-otel
```

## Why this is a separate module

`plugins/hmacauth` lives in the root aarv module, which is intentionally
zero-dependency. Importing `go.opentelemetry.io/otel` into the root
would force OTel onto every aarv consumer — including those who don't
care about tracing.

The `Observer` hook + this companion module is the design that lets
root stay zero-dep while shipping a turnkey OTel adapter for consumers
who do want tracing.

## Quick start

```go
package main

import (
    "github.com/nilshah80/aarv"
    "github.com/nilshah80/aarv/plugins/hmacauth"
    hmacotel "github.com/nilshah80/aarv/plugins/hmacauth-otel"
)

func main() {
    cfg := hmacauth.Config{
        Validator: /* … */,
        // Wires hmacauth to emit one OTel span per verification.
        // Uses otel.GetTracerProvider() so a globally-installed
        // provider is picked up automatically.
        Observer: hmacotel.NewObserver(),
    }

    app := aarv.New()
    app.Use(hmacauth.New(cfg))
    _ = app.Listen(":8080")
}
```

## Bring your own provider

Pass a `TracerProvider` explicitly when you want to scope spans to a
non-global tracer (typical in test code, or when running multiple
isolated apps in one process):

```go
cfg.Observer = hmacotel.NewObserver(
    hmacotel.WithTracerProvider(tp),
    hmacotel.WithSpanName("auth.HMAC.verify"), // optional override
)
```

## Span lifecycle

The Observer fires AFTER `hmacauth` has finished verification, with the
wall-clock duration carried in `Event.Duration`. A naive
`tracer.Start()` then `span.End()` inside the Observer would measure
the callback overhead (≈ nanoseconds), not the verification window.

To produce a span whose recorded interval reflects the actual
verification time, this adapter back-dates the span:

```go
endTime := time.Now()
startTime := endTime.Add(-event.Duration)
_, span := tracer.Start(parent, name, trace.WithTimestamp(startTime))
defer span.End(trace.WithTimestamp(endTime))
```

This means trace UIs show `auth.HMAC.verify` occupying the correct
slice of the request timeline.

## Parent context

- When the Observer receives a non-nil `*aarv.Context` with a non-nil
  `c.Context()`, that context is used as the parent. The emitted span
  becomes a child of whatever upstream span is on the request.
- When `c == nil` (stdlib mounts with the framework bridge disabled),
  the span is rooted at `context.Background()`. No panic, no warning;
  the trace shows an orphan auth span.

## Non-goals

- This module does NOT carry exporter / sampler configuration. The
  consumer constructs their own `TracerProvider` and either installs
  it globally (`otel.SetTracerProvider(tp)`) or passes it via
  `WithTracerProvider(tp)`.
- This module does NOT emit metrics. The `Observer` hook receives
  `Event.Duration` for callers who want to record verify-latency
  histograms themselves; that's a separate concern from spans.
- This module does NOT wrap the hmacauth middleware. The hook fires
  from inside hmacauth's existing pipeline; this adapter is purely a
  span emitter.
