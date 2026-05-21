# aarv prometheus plugin

Prometheus metrics middleware for [aarv](https://github.com/nilshah80/aarv).
Records the four standard HTTP server metrics:

- `http_requests_total{method, path, status}` — counter
- `http_request_duration_seconds{method, path, status}` — histogram
- `http_requests_in_flight` — gauge
- `http_response_size_bytes{method, path, status}` — histogram

Lives in its own Go module. Pull it with:

```
go get github.com/nilshah80/aarv/plugins/prometheus
```

## Quick start

```go
import (
    "github.com/nilshah80/aarv"
    prom "github.com/nilshah80/aarv/plugins/prometheus"
)

func main() {
    cfg := prom.Config{
        Namespace: "aarv",
        SkipPaths: []string{"/metrics"},
    }
    app := aarv.New()
    app.Use(prom.New(cfg))

    // Expose /metrics as a regular aarv route. App.Mount appends a
    // trailing slash and redirects clients scraping at "/metrics" — Get
    // gives clients the canonical path.
    scrape := prom.Handler(cfg)
    app.Get("/metrics", func(c *aarv.Context) error {
        scrape.ServeHTTP(c.Response(), c.Request())
        return nil
    })

    app.Get("/users/{id}", func(c *aarv.Context) error {
        return c.Text(200, "ok")
    })

    _ = app.Listen(":8080")
}
```

## Cardinality control

The `path` label is the production risk. Without grouping, requests to
`/users/1`, `/users/2`, `/users/3` produce three distinct label sets and
blow up Prometheus storage.

The default `GroupPath` collapses paths to their registered aarv route
pattern via `aarv.Context.RoutePattern`:

```
GET /users/1 -> /users/{id}
GET /users/2 -> /users/{id}
GET /users/3 -> /users/{id}
```

For requests without a matched aarv route (404s, mounted handlers, plain
http.Handler usage outside the framework), the default falls back to the
raw URL path.

To drop unmatched routes from metrics entirely, supply a custom
`GroupPath`:

```go
cfg.GroupPath = func(c *aarv.Context) string {
    p := c.RoutePattern()
    if p == "" {
        return "" // exclude unmatched routes from metrics
    }
    return p
}
```

Returning the empty string from `GroupPath` excludes the request from all
four metrics — useful for noise reduction when probes or scanners hit
random paths.

## Test isolation

Each test should use a fresh `prometheus.NewRegistry()` to avoid
cross-test pollution of `DefaultRegisterer`:

```go
reg := prometheus.NewRegistry()
app.Use(prom.New(prom.Config{Registerer: reg}))
// ...
metrics, _ := reg.Gather()
```

## Histogram bucket presets

Two presets are exported. Pick one or supply your own slice via
`Config.Buckets`:

- `DefaultBuckets` — `1ms .. 10s`. Fits typical web APIs whose p50 sits in
  the 10–100ms range.
- `SubMillisecondBuckets` — `100µs .. 5s`. Use for low-latency services
  (in-cache redirects, edge proxies, internal RPC) where p50 falls below
  the 1ms first bucket of `DefaultBuckets`. Symptom that signals you need
  this: `histogram_quantile(0.5, …)` reports `0.001` regardless of load —
  every request is collapsing into the first `DefaultBuckets` bucket.

```go
cfg := prom.Config{
    Namespace: "aarv",
    Buckets:   prom.SubMillisecondBuckets,
}
```

## What this plugin does NOT do

- It does not pick exporters or scrape endpoints — `Handler(cfg)` returns
  an `http.Handler` that you mount where you want via a regular route
  (`app.Get("/metrics", …)`); auth/rate-limit gating is the caller's
  responsibility.
- It does not invent aarv-specific metric names. Names follow Prometheus
  HTTP server conventions so existing dashboards work without translation.
- It does not auto-register custom collectors — pass them via
  `Config.Custom` and the plugin will register them on the same registerer
  as the built-in metrics.
