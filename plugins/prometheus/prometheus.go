// Package prometheus provides Prometheus metrics middleware for the aarv
// framework. The plugin records the four standard HTTP server metrics:
//
//   - http_requests_total{method, path, status}                    counter
//   - http_request_duration_seconds{method, path, status}          histogram
//   - http_requests_in_flight                                       gauge
//   - http_response_size_bytes{method, path, status}               histogram
//
// All metrics carry a configurable Namespace and Subsystem. Names follow
// Prometheus instrumentation idioms; the package does not invent
// aarv-specific field names.
//
// # Cardinality control
//
// Label cardinality is the principal production risk. The default GroupPath
// callback collapses paths to their registered aarv route pattern (e.g.
// "/users/{id}") via aarv.Context.RoutePattern, falling back to the raw
// path for anything outside the registered route table (404s, mounted
// handlers, etc.).
//
// Without GroupPath, requests to /users/1, /users/2, /users/3 would each
// produce a unique label set and blow up Prometheus storage. Callers
// deploying to high-cardinality dynamic-route environments should review
// GroupPath against their routing topology.
//
// # Mounting the /metrics endpoint
//
// Register Handler() as a regular aarv route (not via App.Mount, which
// triggers a 307 redirect for the canonical "/metrics" path):
//
//	app := aarv.New()
//	cfg := prom.Config{}
//	app.Use(prom.New(cfg))
//	scrape := prom.Handler(cfg)
//	app.Get("/metrics", func(c *aarv.Context) error {
//	    scrape.ServeHTTP(c.Response(), c.Request())
//	    return nil
//	})
//
// Use cfg.SkipPaths to exclude "/metrics" from being recorded against
// itself, and apply your own auth or rate-limit middleware around the
// scrape route as needed.
package prometheus

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// DefaultBuckets is a sensible default histogram for HTTP request latencies
// (in seconds), spanning sub-millisecond responses through ~10s long-tail
// requests. Override via Config.Buckets for systems with different SLA
// targets.
var DefaultBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// SubMillisecondBuckets is a histogram preset for low-latency services
// where typical request durations fall below the 1ms first bucket of
// DefaultBuckets. The lowest bucket is 100µs; the slice still spans up
// to 5s so 5xx long-tails remain observable. Pass to Config.Buckets:
//
//	prometheus.New(prometheus.Config{Buckets: prometheus.SubMillisecondBuckets})
//
// Symptom this fixes: with DefaultBuckets, a service whose p50 sits at
// ~150µs collapses every request into the first bucket and
// histogram_quantile(0.5, …) reports the bucket boundary (1ms) regardless
// of the real distribution.
var SubMillisecondBuckets = []float64{
	0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025,
	0.05, 0.1, 0.25, 0.5, 1, 2.5, 5,
}

// DefaultSizeBuckets is a default histogram for response-body sizes, in
// bytes. Spans 100B JSON responses through ~10MB downloads.
var DefaultSizeBuckets = []float64{
	100, 1_000, 10_000, 100_000, 1_000_000, 10_000_000,
}

// Config tunes the plugin.
type Config struct {
	// Namespace is the Prometheus metric prefix. Empty by default; README
	// examples use "aarv".
	Namespace string

	// Subsystem is the second-level metric prefix. Empty by default.
	Subsystem string

	// Buckets configures the request-duration histogram in seconds. Empty
	// uses DefaultBuckets.
	Buckets []float64

	// SizeBuckets configures the response-size histogram in bytes. Empty
	// uses DefaultSizeBuckets.
	SizeBuckets []float64

	// GroupPath maps a request to its label value for the "path" dimension.
	// Defaults to the registered aarv route pattern when available, falling
	// back to the raw path. Override to apply custom normalization (e.g.
	// collapsing path-based tenant identifiers).
	//
	// Returning the empty string causes the request to be excluded from all
	// metrics; useful for dropping 404s or known noise.
	GroupPath func(c *aarv.Context) string

	// Registerer is the Prometheus registry into which metrics are
	// registered. Defaults to prometheus.DefaultRegisterer. Tests typically
	// pass a fresh prometheus.NewRegistry() to isolate runs.
	Registerer prometheus.Registerer

	// Custom is a list of additional collectors to register alongside the
	// built-in metrics. Provide application-specific gauges/counters here
	// rather than mutating DefaultRegisterer separately.
	Custom []prometheus.Collector

	// SkipPaths is an exact-match list of request paths excluded from
	// metric recording. Useful for healthchecks or the /metrics endpoint
	// itself.
	SkipPaths []string
}

// metrics holds the four built-in collectors plus the resolved config bits
// the middleware closure needs at request time. Its fields are immutable
// once buildMetrics returns.
type metrics struct {
	requests  *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	inFlight  prometheus.Gauge
	respSize  *prometheus.HistogramVec
	groupPath func(c *aarv.Context) string
	skip      map[string]struct{}
}

// buildMetrics resolves cfg defaults, constructs the collectors, registers
// them on the configured Registerer, and registers any Custom collectors.
// Panics on registration error (typically a duplicate metric name when the
// same plugin is constructed twice against the same registry).
func buildMetrics(cfg Config) *metrics {
	if cfg.Registerer == nil {
		cfg.Registerer = prometheus.DefaultRegisterer
	}
	if len(cfg.Buckets) == 0 {
		cfg.Buckets = DefaultBuckets
	}
	if len(cfg.SizeBuckets) == 0 {
		cfg.SizeBuckets = DefaultSizeBuckets
	}
	if cfg.GroupPath == nil {
		cfg.GroupPath = defaultGroupPath
	}

	skip := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skip[p] = struct{}{}
	}

	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: cfg.Namespace,
		Subsystem: cfg.Subsystem,
		Name:      "http_requests_total",
		Help:      "Total HTTP requests processed, labeled by method, path, and status.",
	}, []string{"method", "path", "status"})

	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: cfg.Namespace,
		Subsystem: cfg.Subsystem,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request latency distribution in seconds.",
		Buckets:   cfg.Buckets,
	}, []string{"method", "path", "status"})

	inFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: cfg.Namespace,
		Subsystem: cfg.Subsystem,
		Name:      "http_requests_in_flight",
		Help:      "Number of HTTP requests currently being served.",
	})

	respSize := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: cfg.Namespace,
		Subsystem: cfg.Subsystem,
		Name:      "http_response_size_bytes",
		Help:      "HTTP response size distribution in bytes.",
		Buckets:   cfg.SizeBuckets,
	}, []string{"method", "path", "status"})

	cfg.Registerer.MustRegister(requests, duration, inFlight, respSize)
	for _, c := range cfg.Custom {
		cfg.Registerer.MustRegister(c)
	}

	return &metrics{
		requests:  requests,
		duration:  duration,
		inFlight:  inFlight,
		respSize:  respSize,
		groupPath: cfg.GroupPath,
		skip:      skip,
	}
}

// defaultGroupPath returns c.RoutePattern when set, falling back to c.Path.
// This keeps cardinality bounded for matched aarv routes while still
// reporting unmatched paths verbatim — callers wanting to drop 404s should
// supply a custom GroupPath that returns "" when c.RoutePattern is empty.
func defaultGroupPath(c *aarv.Context) string {
	if p := c.RoutePattern(); p != "" {
		return p
	}
	return c.Path()
}

// New returns aarv middleware that records the four built-in HTTP server
// metrics for every request. Apply once via app.Use(); the resulting
// middleware registers both stdlib and native paths and runs in either.
//
// Panics if the configured collectors cannot be registered (typically a
// duplicate metric name on the same registerer — fix by passing a fresh
// prometheus.NewRegistry() per app or by avoiding multiple New calls
// against DefaultRegisterer).
func New(cfg Config) aarv.NativeMiddleware {
	m := buildMetrics(cfg)

	stdlib := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, skipped := m.skip[r.URL.Path]; skipped {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			// Defer Dec and release immediately after acquiring resources
			// so a panic in next.ServeHTTP cannot permanently skew
			// http_requests_in_flight or leak a pooled StatusRecorder.
			// LIFO defer order: SetResponse(orig) restores c.res first,
			// then releaseRecordingWriter returns the writer to the pool,
			// then inFlight.Dec adjusts the gauge.
			m.inFlight.Inc()
			defer m.inFlight.Dec()
			rw := acquireRecordingWriter(w)
			defer releaseRecordingWriter(rw)

			// If an aarv Context is reachable, point c.res at the recording
			// writer so framework writes (c.JSON/c.Text and the default
			// error handlers used by 404/405 paths) flow through it. The
			// next.ServeHTTP(rw, r) call also covers handlers that ignore
			// c.res and write directly to the http.ResponseWriter passed
			// down the chain. Restore c.res on the way out so we don't
			// leak the recording writer past this request.
			c, hasCtx := aarv.FromRequest(r)
			if hasCtx {
				orig := c.Response()
				c.SetResponse(rw)
				defer c.SetResponse(orig)
			}
			next.ServeHTTP(rw, r)

			path := pathLabel(c, r, m.groupPath)
			if path == "" {
				return
			}
			status := strconv.Itoa(rw.Status())
			m.requests.WithLabelValues(r.Method, path, status).Inc()
			m.duration.WithLabelValues(r.Method, path, status).Observe(time.Since(start).Seconds())
			m.respSize.WithLabelValues(r.Method, path, status).Observe(float64(rw.BytesWritten()))
		})
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if _, skipped := m.skip[c.Path()]; skipped {
				return next(c)
			}
			start := time.Now()
			// Same panic-safety pattern as the stdlib path: defer Dec and
			// release immediately so a panic in next(c) cannot skew the
			// in-flight gauge or leak the pooled writer.
			m.inFlight.Inc()
			defer m.inFlight.Dec()
			orig := c.Response()
			rw := acquireRecordingWriter(orig)
			defer releaseRecordingWriter(rw)
			c.SetResponse(rw)
			defer c.SetResponse(orig)
			err := next(c)

			path := m.groupPath(c)
			if path == "" {
				return err
			}
			status := strconv.Itoa(rw.Status())
			m.requests.WithLabelValues(c.Method(), path, status).Inc()
			m.duration.WithLabelValues(c.Method(), path, status).Observe(time.Since(start).Seconds())
			m.respSize.WithLabelValues(c.Method(), path, status).Observe(float64(rw.BytesWritten()))
			return err
		}
	})

	return aarv.RegisterNativeMiddleware(stdlib, native)
}

// Handler returns an http.Handler that exposes the registered metrics in
// Prometheus text format. Register it as a regular aarv route (not via
// App.Mount, which appends a trailing slash and triggers a 307 redirect
// for "/metrics"):
//
//	scrape := prom.Handler(cfg)
//	app.Get("/metrics", func(c *aarv.Context) error {
//	    scrape.ServeHTTP(c.Response(), c.Request())
//	    return nil
//	})
//
// The handler reads from cfg.Registerer (or the default registerer when
// cfg.Registerer is nil), so it pairs naturally with a preceding New(cfg)
// call that uses the same registry.
func Handler(cfg Config) http.Handler {
	reg := cfg.Registerer
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	gatherer, ok := reg.(prometheus.Gatherer)
	if !ok {
		// DefaultRegisterer is also a Gatherer; custom Registerer
		// implementations that aren't Gatherers fall back to the default.
		gatherer = prometheus.DefaultGatherer
	}
	return promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{})
}

// pathLabel computes the path label value for the stdlib path. When an
// aarv Context is reachable from the request (typical with the framework
// bridge enabled), the configured GroupPath is consulted; otherwise we
// fall back to the raw URL path so metrics still record for plain
// http.Handler usage.
func pathLabel(c *aarv.Context, r *http.Request, groupPath func(c *aarv.Context) string) string {
	if c != nil {
		return groupPath(c)
	}
	return r.URL.Path
}

// recordingWriterPool pools *aarv.StatusRecorder instances so the metrics
// middleware doesn't allocate one per request on hot paths. StatusRecorder
// is the framework's canonical status/byte observer; its Reset(w) method
// is the explicit re-bind contract pools depend on.
var recordingWriterPool = sync.Pool{
	New: func() any { return aarv.NewStatusRecorder(nil) },
}

func acquireRecordingWriter(w http.ResponseWriter) *aarv.StatusRecorder {
	rw := recordingWriterPool.Get().(*aarv.StatusRecorder)
	rw.Reset(w)
	return rw
}

func releaseRecordingWriter(rw *aarv.StatusRecorder) {
	if rw == nil {
		return
	}
	rw.Reset(nil)
	recordingWriterPool.Put(rw)
}
