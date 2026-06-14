# aarv otel plugin

OpenTelemetry tracing and metrics middleware for [aarv](https://github.com/nilshah80/aarv).

- W3C `traceparent` / `tracestate` / `baggage` **extract** from incoming request headers via the configured Propagator. The plugin does not call `Propagator.Inject`; outbound calls from inside a handler should propagate context themselves (e.g. `otelhttp.NewTransport` wrapping the outbound client, which uses the same Propagator).
- One server span per request, named `<METHOD> <RoutePattern>` (low-cardinality)
- HTTP semantic-convention attributes — modern (`semconv v1.37.0`) keys: `http.request.method`, `url.path`, `http.route`, `http.response.status_code`, `user_agent.original`, `client.address`, `network.protocol.version`; plus `request_id` when available (aarv-specific). The legacy keys (`http.method`, `http.target`, `http.status_code`, `http.user_agent`, `net.peer.ip`) were dual-emitted through v0.9.5 and **removed in v0.9.6** — only the modern keys are emitted now. If you have not already, migrate any TraceQL / dashboard queries to the modern keys.
- 5xx responses set the span status to `Error`
- The four standard HTTP server metrics via the configured MeterProvider
- Trace-correlated `slog` logger: `trace_id` and `span_id` injected into `aarv.Context.Logger()`

Lives in its own Go module:

```
go get github.com/nilshah80/aarv/plugins/otel
```

## Bring your own Provider

The plugin does not ship exporters or sampling configuration. You construct
your own `TracerProvider` / `MeterProvider` — typically with an OTLP
exporter and a `Resource` carrying `service.name` — and pass them via
`Config`. This keeps the dependency footprint small and lets you compose
your own pipeline (sampling, batching, retry, auth) without
plugin-specific knobs.

## Quick start

```go
package main

import (
    "context"
    "log"

    "github.com/nilshah80/aarv"
    aotel "github.com/nilshah80/aarv/plugins/otel"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
)

func main() {
    ctx := context.Background()
    exp, err := otlptracehttp.New(ctx)
    if err != nil {
        log.Fatal(err)
    }
    res, _ := resource.Merge(resource.Default(), resource.NewWithAttributes(
        semconv.SchemaURL,
        semconv.ServiceName("api"),
    ))
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithResource(res),
        sdktrace.WithBatcher(exp),
    )
    defer tp.Shutdown(ctx)
    otel.SetTracerProvider(tp)

    app := aarv.New()
    app.Use(aotel.New(aotel.Config{})) // zero-value Config = defaults via Suppress* booleans

    app.Get("/users/{id}", func(c *aarv.Context) error {
        c.Logger().Info("looked up user")
        return c.Text(200, "ok")
    })

    _ = app.Listen(":8080")
}
```

A request to `/users/42` produces:

- A span named `GET /users/{id}` (route pattern, not the resolved path)
- An access log line carrying `trace_id` and `span_id` matching the span
- Counter, duration, and size histograms incremented under `http.server.*`

## Why span names follow the route pattern

The middleware initially names the span using the raw path
(`<METHOD> <Path>`) at request start, then renames it to the route
pattern in the post-handler finalize step once `RoutePattern` is known.
This keeps span names low-cardinality (`/users/{id}` vs millions of
`/users/N` distinct names).

If a request matches no aarv route (404 or `App.Mount`), the span name
stays at the raw path.

## Log correlation

Unless `Config.SuppressLogAttrs` is set, the plugin replaces
`aarv.Context.Logger()` for the request lifetime with a `slog.Logger`
that has the active span's `trace_id` and `span_id` attached. Inside
the handler, calls to `c.Logger().Info(...)` automatically include
those fields. The original logger is restored when the handler returns.

## What this plugin does NOT do

- It does not configure exporters, samplers, or batchers — those live on
  the user-supplied Provider.
- It does not understand `X-Forwarded-For` for `client.address` — the value
  comes from `r.RemoteAddr` only. Trust depends on proxy topology, which
  is application-specific.
- It does not call `span.RecordError` for handler errors — the aarv
  framework converts handler errors into HTTP responses before the
  middleware sees them, so error detection happens via the 5xx status
  code (matching OTel HTTP semconv recommendation).
- It does not call `Propagator.Inject` on outgoing requests. Inject
  belongs in the outbound HTTP client (use `otelhttp.NewTransport` from
  `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` on
  your `*http.Client`, or call `Propagator.Inject` yourself when
  constructing requests inside a handler).
