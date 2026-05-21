// Package otel provides OpenTelemetry tracing and metrics middleware for
// the aarv framework. The plugin:
//
//   - Extracts W3C trace context (traceparent, tracestate, baggage) from
//     incoming requests using the configured Propagator. The plugin does
//     not call Propagator.Inject — the inbound request's parent context is
//     made available on c.Context() for handlers that originate downstream
//     calls (e.g. via otelhttp.NewTransport on an outbound *http.Client),
//     which is where injection belongs.
//   - Starts a server span around each handler, named via SpanNameFunc
//     (default: "<METHOD> <RoutePattern>").
//   - Sets HTTP semantic-convention attributes on the span. The plugin
//     emits the modern semconv v1.37.0 keys
//     (http.request.method, url.path, http.route,
//     http.response.status_code, user_agent.original, client.address,
//     network.protocol.version) and, for one transitional minor release,
//     the legacy keys it used to emit exclusively
//     (http.method, http.target, http.status_code, http.user_agent,
//     net.peer.ip). request_id is emitted when available; it is an
//     aarv-specific addition with no semconv rename. The legacy
//     emissions are removed in the release after the dual-emit minor.
//   - Marks 5xx responses as span Status Error.
//   - Emits per-request counter / duration / size metrics via the configured
//     MeterProvider unless SuppressMetrics is set.
//   - Replaces aarv.Context.Logger() for the request lifetime with a
//     trace-correlated slog.Logger that has trace_id and span_id attached
//     (unless SuppressLogAttrs is set).
//
// # Bring your own Provider
//
// The plugin does not ship exporters or sampling configuration. Callers
// construct their own TracerProvider / MeterProvider — typically with an
// OTLP exporter and a Resource carrying service.name — and pass them via
// Config. This keeps the dependency footprint small (no exporter pulls)
// and lets users compose their own pipeline (sampling, batching, retry,
// auth) without plugin-specific knobs.
//
// # Quick start
//
//	import (
//	    "go.opentelemetry.io/otel"
//	    "go.opentelemetry.io/otel/sdk/trace"
//	    aaarv "github.com/nilshah80/aarv"
//	    aotel "github.com/nilshah80/aarv/plugins/otel"
//	)
//
//	tp := trace.NewTracerProvider(trace.WithResource(...))
//	otel.SetTracerProvider(tp)
//
//	app := aaarv.New()
//	app.Use(aotel.New(aotel.Config{TracerProvider: tp}))
package otel

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	// instrumentationName identifies this plugin as the originator of
	// emitted spans and metrics.
	instrumentationName = "github.com/nilshah80/aarv/plugins/otel"
)

// Config tunes the plugin.
type Config struct {
	// TracerProvider supplies tracers. Defaults to otel.GetTracerProvider().
	TracerProvider trace.TracerProvider

	// MeterProvider supplies meters. Defaults to otel.GetMeterProvider().
	MeterProvider metric.MeterProvider

	// Propagator extracts trace context from incoming request headers.
	// Defaults to otel.GetTextMapPropagator() (typically a TraceContext +
	// Baggage composite). This plugin does not call Propagator.Inject;
	// outbound propagation is the application's responsibility (e.g. via
	// otelhttp.NewTransport on outbound *http.Client).
	Propagator propagation.TextMapPropagator

	// SpanNameFunc renders the span name for a request. Defaults to
	// "<METHOD> <RoutePattern>" if RoutePattern is non-empty, else
	// "<METHOD> <Path>".
	SpanNameFunc func(method, path string) string

	// SuppressErrorStatus disables the 5xx-status-code → span Status Error
	// mapping. Zero value enables it; the plugin marks 5xx responses as
	// errored spans by default per OTel HTTP semconv recommendation.
	SuppressErrorStatus bool

	// SuppressMetrics disables the four standard HTTP server metrics.
	// Zero value emits metrics via the configured MeterProvider:
	//   - http.server.request.count                    counter
	//   - http.server.request.duration_seconds         histogram
	//   - http.server.request.size_bytes               histogram
	//   - http.server.response.size_bytes              histogram
	SuppressMetrics bool

	// SuppressLogAttrs disables trace_id/span_id injection into
	// aarv.Context.Logger(). Zero value injects them for the request
	// lifetime and restores the previous logger on handler return.
	SuppressLogAttrs bool

	// SkipPaths excludes specific request paths from span/metric recording.
	// Useful for healthchecks or the metrics-scrape endpoint itself.
	SkipPaths []string
}

// DefaultConfig returns a zero-value Config. Provided for API symmetry —
// the inverted Suppress* booleans mean Config{} produces the same
// behavior as DefaultConfig() everywhere.
func DefaultConfig() Config {
	return Config{}
}

// state holds the resolved config and pre-built instrumentation. Captured
// in the middleware closure; immutable after build.
type state struct {
	tracer trace.Tracer
	// defaultSpanNamer reports whether spanNameFunc is the package's
	// built-in defaultSpanName. When true, finalizeSpan upgrades the
	// span name from "<METHOD> <Path>" to "<METHOD> <RoutePattern>" once
	// dispatch finalizes; when false, a caller-supplied SpanNameFunc is
	// honored verbatim and never overwritten.
	defaultSpanNamer bool
	propagator       propagation.TextMapPropagator
	spanNameFunc     func(method, path string) string
	recordErrors     bool
	injectLogAttr    bool
	skip             map[string]struct{}

	requestCount metric.Int64Counter
	duration     metric.Float64Histogram
	requestSize  metric.Int64Histogram
	responseSize metric.Int64Histogram
}

// build resolves cfg defaults, fetches Tracer + Meter handles, and pre-
// constructs the four metric instruments. Returns nil-valued metric
// instruments when cfg.SuppressMetrics is true to avoid pulling a Meter
// from a Provider the user doesn't intend to use.
func build(cfg Config) *state {
	if cfg.TracerProvider == nil {
		cfg.TracerProvider = otel.GetTracerProvider()
	}
	if cfg.MeterProvider == nil {
		cfg.MeterProvider = otel.GetMeterProvider()
	}
	if cfg.Propagator == nil {
		cfg.Propagator = otel.GetTextMapPropagator()
	}
	defaultNamer := cfg.SpanNameFunc == nil
	if defaultNamer {
		cfg.SpanNameFunc = defaultSpanName
	}

	skip := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skip[p] = struct{}{}
	}

	s := &state{
		tracer:           cfg.TracerProvider.Tracer(instrumentationName),
		defaultSpanNamer: defaultNamer,
		propagator:       cfg.Propagator,
		spanNameFunc:     cfg.SpanNameFunc,
		recordErrors:     !cfg.SuppressErrorStatus,
		injectLogAttr:    !cfg.SuppressLogAttrs,
		skip:             skip,
	}

	if !cfg.SuppressMetrics {
		meter := cfg.MeterProvider.Meter(instrumentationName)
		s.requestCount = mustCounter(meter.Int64Counter("http.server.request.count"))
		s.duration = mustFloat64Histogram(meter.Float64Histogram("http.server.request.duration_seconds"))
		s.requestSize = mustInt64Histogram(meter.Int64Histogram("http.server.request.size_bytes"))
		s.responseSize = mustInt64Histogram(meter.Int64Histogram("http.server.response.size_bytes"))
	}

	return s
}

// mustCounter / mustFloat64Histogram / mustInt64Histogram panic if the
// instrument constructor returned an error. Real MeterProvider
// implementations only return errors on duplicate-with-conflicting-type
// registration, which is a programmer mistake we want surfaced loudly.
func mustCounter(c metric.Int64Counter, err error) metric.Int64Counter {
	if err != nil {
		panic("otel: failed to create instrument: " + err.Error())
	}
	return c
}

func mustFloat64Histogram(h metric.Float64Histogram, err error) metric.Float64Histogram {
	if err != nil {
		panic("otel: failed to create instrument: " + err.Error())
	}
	return h
}

func mustInt64Histogram(h metric.Int64Histogram, err error) metric.Int64Histogram {
	if err != nil {
		panic("otel: failed to create instrument: " + err.Error())
	}
	return h
}

// defaultSpanName produces "<METHOD> <path>" at dispatch time. The
// framework resolves the route pattern only after dispatch, so the
// SpanNameFunc signature is method/path only. To get low-cardinality
// span names, leave SpanNameFunc nil — the plugin uses defaultSpanName
// at dispatch time and finalizeSpan upgrades the name to
// "<METHOD> <RoutePattern>" once the pattern is known.
//
// A caller-supplied SpanNameFunc is honored verbatim; finalizeSpan does
// not overwrite custom names. If you supply a SpanNameFunc and want
// pattern-based naming, consult c.RoutePattern() yourself in a
// route-level middleware that fires after dispatch and call
// span.SetName.
func defaultSpanName(method, path string) string {
	return method + " " + path
}

// New returns aarv middleware that records OpenTelemetry traces and metrics
// for every request. Apply once via app.Use().
func New(cfg Config) aarv.Middleware {
	s := build(cfg)

	stdlib := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, skipped := s.skip[r.URL.Path]; skipped {
				next.ServeHTTP(w, r)
				return
			}
			s.handleStdlib(w, r, next)
		})
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if _, skipped := s.skip[c.Path()]; skipped {
				return next(c)
			}
			return s.handleNative(c, next)
		}
	})

	return aarv.RegisterNativeMiddleware(stdlib, native)
}

// handleStdlib serves a request through the stdlib middleware path,
// extracting trace context, starting a server span, and forwarding to next.
// Metrics and span finalization run after next returns.
func (s *state) handleStdlib(w http.ResponseWriter, r *http.Request, next http.Handler) {
	ctx := s.propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	method := r.Method
	path := r.URL.Path
	spanName := s.spanNameFunc(method, path)

	ctx, span := s.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	r = r.WithContext(ctx)

	rw := acquireRecordingWriter(w)
	// Defer release immediately after acquire so a panic in next.ServeHTTP
	// cannot leak the pooled writer. LIFO order ensures any deferred
	// SetResponse/SetLogger restorations registered below run BEFORE the
	// release returns the writer to the pool.
	defer releaseRecordingWriter(rw)

	c, hasCtx := aarv.FromRequest(r)
	if hasCtx {
		// Make framework writes (c.JSON / default error handler) flow
		// through the recording writer so we capture status + size.
		origRes := c.Response()
		c.SetResponse(rw)
		defer c.SetResponse(origRes)

		if s.injectLogAttr {
			origLogger := c.Logger()
			c.SetLogger(loggerWithSpan(origLogger, span))
			defer c.SetLogger(origLogger)
		}
	}

	start := time.Now()
	next.ServeHTTP(rw, r)
	duration := time.Since(start)

	pattern := ""
	if hasCtx {
		pattern = c.RoutePattern()
	}
	requestID := ""
	if hasCtx {
		requestID = c.RequestID()
	}
	finalizeSpan(span, method, path, pattern, rw.statusCode, r, requestID, nil, s.recordErrors, s.defaultSpanNamer)
	s.recordHTTPMetrics(ctx, method, pattern, path, rw.statusCode, r.ContentLength, rw.bytesWritten, duration)
}

// handleNative serves a request through the aarv native HandlerFunc path.
// Same semantics as handleStdlib but with direct *aarv.Context access.
func (s *state) handleNative(c *aarv.Context, next aarv.HandlerFunc) error {
	r := c.RawRequest()
	ctx := s.propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	method := c.Method()
	path := c.Path()
	spanName := s.spanNameFunc(method, path)

	ctx, span := s.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	r = r.WithContext(ctx)
	c.SetContext(ctx)

	origRes := c.Response()
	rw := acquireRecordingWriter(origRes)
	// Defer release immediately so a panic in next(c) cannot leak the
	// pooled writer. The deferred SetResponse / SetLogger restorations
	// below register later and therefore run first under LIFO unwind,
	// before the writer is returned to the pool.
	defer releaseRecordingWriter(rw)
	c.SetResponse(rw)
	defer c.SetResponse(origRes)

	if s.injectLogAttr {
		origLogger := c.Logger()
		c.SetLogger(loggerWithSpan(origLogger, span))
		defer c.SetLogger(origLogger)
	}

	start := time.Now()
	err := next(c)
	duration := time.Since(start)

	requestID := c.RequestID()
	finalizeSpan(span, method, path, c.RoutePattern(), rw.statusCode, r, requestID, err, s.recordErrors, s.defaultSpanNamer)
	s.recordHTTPMetrics(ctx, method, c.RoutePattern(), path, rw.statusCode, r.ContentLength, rw.bytesWritten, duration)
	return err
}

// recordingWriter intercepts WriteHeader / Write to track status code and
// bytes written. Implements http.ResponseWriter and forwards transparently.
// Pooled to avoid per-request allocation.
type recordingWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
	wroteHeader  bool
}

var recordingWriterPool = sync.Pool{
	New: func() any { return &recordingWriter{} },
}

func acquireRecordingWriter(w http.ResponseWriter) *recordingWriter {
	rw := recordingWriterPool.Get().(*recordingWriter)
	rw.ResponseWriter = w
	rw.statusCode = http.StatusOK
	rw.bytesWritten = 0
	rw.wroteHeader = false
	return rw
}

func releaseRecordingWriter(rw *recordingWriter) {
	if rw == nil {
		return
	}
	rw.ResponseWriter = nil
	recordingWriterPool.Put(rw)
}

func (rw *recordingWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.statusCode = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *recordingWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.wroteHeader = true
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// Unwrap returns the underlying ResponseWriter so http.ResponseController
// (and middleware that depends on Flusher / Hijacker / Pusher / etc.) can
// reach past the recording wrapper. Without Unwrap, ResponseController
// refuses to delegate and downstream streaming/hijacking silently breaks
// under the otel middleware.
func (rw *recordingWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// recordHTTPMetrics emits the four standard HTTP server metrics on the
// per-request meter handles. statusStr converts the status to a label
// value usable for OTel attributes.
func (s *state) recordHTTPMetrics(ctx interface{ Done() <-chan struct{} }, method, pattern, path string, status int, reqSize, respSize int64, dur time.Duration) {
	if s.requestCount == nil {
		return
	}
	// legacyTarget preserves the pre-migration http.target metric label
	// shape: the matched route pattern when known, raw path otherwise.
	// Kept for one transitional minor so dashboards keyed on the old
	// label continue to work; the next release drops the legacy emit.
	legacyTarget := pattern
	if legacyTarget == "" {
		legacyTarget = path
	}

	// Modern semconv v1.37.0 metric attributes. Per the HTTP server
	// metrics conventions, the low-cardinality dimension on metrics is
	// http.route — url.path belongs on spans (where high cardinality is
	// the desired behavior) but NOT on metrics, where per-URL labels
	// would explode TSDB cardinality. We therefore emit http.route here
	// and intentionally do NOT emit url.path on the metric attribute set.
	attrSet := []attribute.KeyValue{
		// Modern.
		attribute.String(attrHTTPRequestMethod, method),
		attribute.Int(attrHTTPResponseStatusCode, status),
		// Legacy.
		attribute.String(legacyAttrHTTPMethod, method),
		attribute.String(legacyAttrHTTPTarget, legacyTarget),
		attribute.Int(legacyAttrHTTPStatusCode, status),
		// http.status_class is an aarv-specific addition; no semconv
		// rename, kept as-is.
		attribute.String("http.status_class", strconv.Itoa(status/100)+"xx"),
	}
	if pattern != "" {
		attrSet = append(attrSet, attribute.String(attrHTTPRoute, pattern))
	}
	attrs := metric.WithAttributes(attrSet...)
	// We pass context.Background-equivalent to avoid leaking the OTel
	// span context into metric attributes (Meter SDKs derive exemplars
	// from the active span; the scope of this is metric.AddOption tuning
	// outside our purview).
	c := backgroundContext()
	s.requestCount.Add(c, 1, attrs)
	s.duration.Record(c, dur.Seconds(), attrs)
	if reqSize > 0 {
		s.requestSize.Record(c, reqSize, attrs)
	}
	if respSize > 0 {
		s.responseSize.Record(c, respSize, attrs)
	}
	_ = ctx
}
