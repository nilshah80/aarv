# Observability guide

Aarv keeps observability optional. The root module provides lightweight
request IDs, logging, health checks, verbose audit logging, and pprof. Metrics
and tracing live in submodule plugins so OpenTelemetry and Prometheus
dependencies do not enter the zero-dependency core.

## Recommended stack

For production APIs, start with:

```go
app.Use(
    aarv.Recovery(),
    requestid.New(),
    logger.New(logger.Config{SkipPaths: []string{"/health", "/ready", "/live"}}),
)
```

Then add metrics or tracing when the service is deployed behind a collector:

```go
app.Use(prometheus.New(prometheus.Config{
    Namespace: "aarv",
    SkipPaths: []string{"/metrics", "/health", "/ready", "/live"},
}))

app.Use(otel.New(otel.Config{
    SkipPaths: []string{"/metrics", "/health", "/ready", "/live"},
}))
```

Keep health and metrics paths out of request logs and request metrics unless
you intentionally want probe traffic included.

## Request IDs

`plugins/requestid` reads an incoming request ID from `X-Request-ID` or
generates a ULID. It writes the ID to the response header and stores it on
`*aarv.Context`.

```go
app.Use(requestid.New(requestid.Config{
    Header: "X-Request-ID",
    Prefix: "api-",
}))
```

Use `requestid.FastConfig()` for high-throughput internal systems where the ID
is only a correlation value, not a security token. The fast generator uses
process-seeded pseudo-randomness; keep the default generator when stronger
randomness matters.

Handlers and error hooks can read the request ID:

```go
c.Logger().Info("handled request", "request_id", c.RequestID())
```

## Access logging

`plugins/logger` is the normal access logger. Use skip paths for health and
metrics endpoints.

```go
app.Use(logger.New(logger.Config{
    SkipPaths: []string{"/health", "/ready", "/live", "/metrics"},
}))
```

`plugins/verboselog` captures fuller request and response data. Treat it as an
audit/debug tool, not as a default production logger for all routes.

```go
app.Use(verboselog.New(verboselog.Config{
    MaxBodySize: 4096,
    SkipPaths:   []string{"/health", "/metrics"},
}))
```

Do not log secrets, tokens, cookies, authorization headers, or large bodies.
Scope verbose logging to bounded routes.

## Health, readiness, and liveness

`plugins/health` intercepts health paths and returns JSON responses.

```go
app.Use(health.New(health.Config{
    HealthPath: "/health",
    ReadyPath:  "/ready",
    LivePath:   "/live",
    ReadyCheck: func() bool { return db.Ping() == nil },
    Info: map[string]any{
        "version": buildVersion,
    },
}))
```

Use readiness for dependency checks that decide whether the service should
receive traffic. Use liveness for whether the process should be restarted.
Keep both endpoints cheap.

## Prometheus

`plugins/prometheus` records:

- `http_requests_total`
- `http_request_duration_seconds`
- `http_requests_in_flight`
- `http_response_size_bytes`

Install the submodule:

```bash
go get github.com/nilshah80/aarv/plugins/prometheus@v0.8.0
```

Mount `/metrics` as a route, not with `App.Mount`, to avoid canonical path
redirects.

```go
cfg := prometheus.Config{
    Namespace: "aarv",
    SkipPaths: []string{"/metrics", "/health", "/ready", "/live"},
}
app.Use(prometheus.New(cfg))

scrape := prometheus.Handler(cfg)
app.Get("/metrics", func(c *aarv.Context) error {
    scrape.ServeHTTP(c.Response(), c.Request())
    return nil
})
```

The default path label uses the registered route pattern such as
`/users/{id}`. Review any custom `GroupPath` function carefully; raw IDs,
tenant names, user IDs, or arbitrary paths will create high-cardinality
metrics.

## OpenTelemetry

`plugins/otel` extracts inbound trace context, starts a server span, records
HTTP attributes and metrics, and injects `trace_id` and `span_id` into
`c.Logger()`.

Install the submodule:

```bash
go get github.com/nilshah80/aarv/plugins/otel@v0.8.0
```

The plugin does not configure exporters. Build your own providers and pass
them in:

```go
app.Use(otelplugin.New(otelplugin.Config{
    TracerProvider: tp,
    MeterProvider:  mp,
    SkipPaths:      []string{"/metrics", "/health", "/ready", "/live"},
}))
```

Outbound propagation belongs in your outbound HTTP client, for example with
`otelhttp.NewTransport`, using `c.Context()`.

## pprof

`plugins/pprof` exposes process internals. Never expose it to untrusted
clients.

```go
auth := basicauth.New(basicauth.Config{
    Validator: validateOpsUser,
})

app.Mount(pprof.DefaultPrefix, pprof.Handler(pprof.Config{
    AuthMiddleware: auth,
}))
```

Prefer mounting pprof only on private admin listeners or behind an IP allowlist
plus authentication.

## Production checklist

- Put recovery before logging so panics are recorded.
- Put request IDs before logging, tracing, errors, and metrics.
- Skip probe paths in logs, traces, and metrics unless intentionally measured.
- Keep Prometheus path labels low-cardinality.
- Configure OTel exporters outside the plugin.
- Protect pprof with auth and network controls.
- Use verbose body logging only on bounded, non-secret routes.
