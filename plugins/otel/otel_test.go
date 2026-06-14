package otel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nilshah80/aarv"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// newTraceRecorder builds a TracerProvider whose spans land in an in-memory
// recorder for assertion. Tests inspect recorder.Ended() to find spans.
func newTraceRecorder() (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	return tp, rec
}

// newMetricReader builds a MeterProvider with a manual reader so tests can
// snapshot metrics on demand.
func newMetricReader() (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	return mp, r
}

func TestNew_StartsServerSpanPerRequest(t *testing.T) {
	tp, rec := newTraceRecorder()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{TracerProvider: tp}))
	app.Get("/users/{id}", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	span := spans[0]
	if span.SpanKind() != trace.SpanKindServer {
		t.Fatalf("span kind: want Server, got %v", span.SpanKind())
	}
	// Default span name uses the registered route pattern after dispatch
	// finalizes, so /users/{id} is preferred over /users/42.
	if name := span.Name(); name != "GET /users/{id}" {
		t.Fatalf("span name: want 'GET /users/{id}', got %q", name)
	}

	// Verify modern semconv v1.37.0 HTTP attributes.
	got := attrMap(span.Attributes())
	if got["http.request.method"] != "GET" {
		t.Fatalf("http.request.method: want GET, got %v", got["http.request.method"])
	}
	// url.path carries the raw request path (high cardinality is the
	// expected behavior on spans). The low-cardinality template lives
	// on http.route below.
	if got["url.path"] != "/users/42" {
		t.Fatalf("url.path: want /users/42 (raw path), got %v", got["url.path"])
	}
	if got["http.response.status_code"] != int64(200) {
		t.Fatalf("http.response.status_code: want 200, got %v", got["http.response.status_code"])
	}
	if got["http.route"] != "/users/{id}" {
		t.Fatalf("http.route: want /users/{id}, got %v", got["http.route"])
	}
}

// TestSemconv_ModernAttributesEmitted locks down the modern semconv v1.37.0
// attribute set on the span (legacy keys were removed in v0.9.6).
func TestSemconv_ModernAttributesEmitted(t *testing.T) {
	tp, rec := newTraceRecorder()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{TracerProvider: tp}))
	app.Get("/users/{id}", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req.Header.Set("User-Agent", "test-ua/1.0")
	req.RemoteAddr = "10.0.0.1:55555"
	app.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	got := attrMap(spans[0].Attributes())

	for _, attr := range []struct {
		key  string
		want any
	}{
		{"http.request.method", "GET"},
		{"http.response.status_code", int64(200)},
		{"user_agent.original", "test-ua/1.0"},
		{"client.address", "10.0.0.1"},
	} {
		if got[attr.key] != attr.want {
			t.Errorf("%s: want %v, got %v", attr.key, attr.want, got[attr.key])
		}
	}

	// url.path is the raw request path (semconv v1.37.0); the
	// low-cardinality template lives on http.route below.
	if got["url.path"] != "/users/42" {
		t.Errorf("url.path: want /users/42 (raw path), got %v", got["url.path"])
	}

	// http.route is the matched route pattern.
	if got["http.route"] != "/users/{id}" {
		t.Errorf("http.route: want /users/{id}, got %v", got["http.route"])
	}

	// network.protocol.version is modern-only too. The httptest request
	// reports HTTP/1.1, so we expect "1.1" verbatim.
	if got["network.protocol.version"] != "1.1" {
		t.Errorf("network.protocol.version: want 1.1, got %v", got["network.protocol.version"])
	}
}

// TestSemconv_LegacyKeysAbsent locks in the v0.9.6 removal: the legacy HTTP
// semconv keys must NOT appear on spans or metrics. http.status_class (an
// aarv-specific metric label) is intentionally retained.
func TestSemconv_LegacyKeysAbsent(t *testing.T) {
	tp, rec := newTraceRecorder()
	mp, mr := newMetricReader()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{TracerProvider: tp, MeterProvider: mp}))
	app.Get("/users/{id}", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req.Header.Set("User-Agent", "test-ua/1.0")
	req.RemoteAddr = "10.0.0.1:55555"
	app.ServeHTTP(httptest.NewRecorder(), req)

	// Spans must carry no legacy key.
	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	span := attrMap(spans[0].Attributes())
	for _, k := range []string{"http.method", "http.target", "http.status_code", "http.user_agent", "net.peer.ip"} {
		if _, ok := span[k]; ok {
			t.Errorf("span must not emit legacy key %q after v0.9.6", k)
		}
	}

	// Metrics must carry no legacy key but must retain http.status_class.
	rm := metricdata.ResourceMetrics{}
	if err := mr.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			for _, attrs := range collectAttributeSets(m.Data) {
				checked++
				for _, k := range []string{"http.method", "http.target", "http.status_code"} {
					if _, ok := attrs.Value(attribute.Key(k)); ok {
						t.Errorf("metric %q must not emit legacy key %q after v0.9.6", m.Name, k)
					}
				}
				// Modern metric keys must remain (http.route is additionally
				// guarded by TestSemconv_MetricsUseRouteNotURLPath); http.status_class
				// is the aarv-specific label that is intentionally retained.
				for _, k := range []string{"http.request.method", "http.response.status_code", "http.status_class"} {
					if _, ok := attrs.Value(attribute.Key(k)); !ok {
						t.Errorf("metric %q lost expected key %q", m.Name, k)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no metric data points collected — assertion never ran")
	}
}

// TestSemconv_MetricsUseRouteNotURLPath asserts the cardinality-control
// rule for OTel HTTP server metrics: the path-shaped attribute on
// metrics is http.route (low-cardinality template), not url.path
// (high-cardinality raw path). Including url.path on metrics would
// produce a label per distinct URL — typically a TSDB-killing mistake.
// This test catches a regression where the metric attribute set
// accidentally re-acquires url.path.
func TestSemconv_MetricsUseRouteNotURLPath(t *testing.T) {
	tp, _ := newTraceRecorder()
	mp, mr := newMetricReader()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{TracerProvider: tp, MeterProvider: mp}))
	app.Get("/users/{id}", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/42", nil))

	rm := metricdata.ResourceMetrics{}
	if err := mr.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	// Walk every metric's data points; check the attribute set on each.
	checked := 0
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			for _, attrs := range collectAttributeSets(m.Data) {
				checked++
				if v, ok := attrs.Value(attribute.Key("url.path")); ok {
					t.Errorf("metric %q has url.path = %q — metrics must use http.route, never url.path (cardinality)",
						m.Name, v.AsString())
				}
				if _, ok := attrs.Value(attribute.Key("http.route")); !ok {
					t.Errorf("metric %q missing http.route attribute", m.Name)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no metric data points were collected — assertion never ran")
	}
}

// collectAttributeSets returns every attribute.Set the metric data
// carries, regardless of whether it's a Sum/Histogram of int64 or
// float64. The metricdata package is generic-typed so we have to
// switch on the concrete data shape — matching the pattern in
// readCounter / readHistogramCount below.
func collectAttributeSets(data metricdata.Aggregation) []attribute.Set {
	var out []attribute.Set
	switch d := data.(type) {
	case metricdata.Sum[int64]:
		for _, dp := range d.DataPoints {
			out = append(out, dp.Attributes)
		}
	case metricdata.Sum[float64]:
		for _, dp := range d.DataPoints {
			out = append(out, dp.Attributes)
		}
	case metricdata.Gauge[int64]:
		for _, dp := range d.DataPoints {
			out = append(out, dp.Attributes)
		}
	case metricdata.Gauge[float64]:
		for _, dp := range d.DataPoints {
			out = append(out, dp.Attributes)
		}
	case metricdata.Histogram[int64]:
		for _, dp := range d.DataPoints {
			out = append(out, dp.Attributes)
		}
	case metricdata.Histogram[float64]:
		for _, dp := range d.DataPoints {
			out = append(out, dp.Attributes)
		}
	}
	return out
}

// TestNetworkProtocolVersion exercises the proto-string normalization
// directly: HTTP/2.0 collapses to "2" per OTel convention; HTTP/1.1 stays
// as "1.1"; empty or unrecognized inputs return "" so finalizeSpan can
// skip emitting the attribute rather than sending a confusing value.
func TestNetworkProtocolVersion(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"HTTP/1.0", "1.0"},
		{"HTTP/1.1", "1.1"},
		{"HTTP/2.0", "2"},
		{"HTTP/3", "3"},
		{"", ""},
		{"GIBBERISH", ""},
	}
	for _, c := range cases {
		if got := networkProtocolVersion(c.in); got != c.want {
			t.Errorf("networkProtocolVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNew_HandlerErrorMarksSpanError covers the case where a handler
// returns an error. The aarv framework converts the returned error into a
// 500 response via its default error handler before the middleware sees
// the result, so the otel plugin detects "handler errored" via the 5xx
// status code rather than an explicit RecordError call. This matches the
// OTel HTTP semconv recommendation (5xx → Error).
func TestNew_HandlerErrorMarksSpanError(t *testing.T) {
	tp, rec := newTraceRecorder()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{TracerProvider: tp}))

	wantErr := errors.New("boom")
	app.Get("/x", func(c *aarv.Context) error {
		return wantErr
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	span := spans[0]
	if span.Status().Code != codes.Error {
		t.Fatalf("span status: want Error, got %v", span.Status().Code)
	}
}

func TestNew_5xxStatusMarksSpanError(t *testing.T) {
	tp, rec := newTraceRecorder()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{TracerProvider: tp}))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(503, "down")
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Fatalf("5xx span status: want Error, got %v", spans[0].Status().Code)
	}
}

// TestNew_SuppressErrorStatusLeaves5xxUnset asserts SuppressErrorStatus
// gates BOTH the handler-error path and the 5xx-status path. Without the
// gate on 5xx, a user opting out of error status would still see Error on
// every 5xx response.
func TestNew_SuppressErrorStatusLeaves5xxUnset(t *testing.T) {
	tp, rec := newTraceRecorder()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{TracerProvider: tp, SuppressErrorStatus: true}))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(503, "down")
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	if spans[0].Status().Code == codes.Error {
		t.Fatalf("SuppressErrorStatus must leave 5xx span status Unset, got Error")
	}
}

func TestNew_4xxNotMarkedAsError(t *testing.T) {
	tp, rec := newTraceRecorder()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{TracerProvider: tp}))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(404, "not found")
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	if spans[0].Status().Code == codes.Error {
		t.Fatalf("4xx span status: should not be Error, got Error")
	}
}

func TestNew_TraceparentExtractedFromIncoming(t *testing.T) {
	tp, rec := newTraceRecorder()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		TracerProvider: tp,
		Propagator:     propagation.TraceContext{},
	}))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	// Construct a valid traceparent and assert the resulting span links to it.
	const tp1 = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("traceparent", tp1)
	app.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	parent := spans[0].Parent()
	if !parent.IsValid() {
		t.Fatalf("expected span to have valid parent extracted from traceparent")
	}
	if parent.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id: got %s", parent.TraceID().String())
	}
}

func TestNew_MetricsRecordedByDefault(t *testing.T) {
	tp, _ := newTraceRecorder()
	mp, mr := newMetricReader()

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		TracerProvider: tp,
		MeterProvider:  mp,
	}))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		app.ServeHTTP(httptest.NewRecorder(), req)
	}

	rm := metricdata.ResourceMetrics{}
	if err := mr.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	count := readCounter(t, &rm, "http.server.request.count")
	if count != 3 {
		t.Fatalf("counter: want 3, got %d", count)
	}
}

func TestNew_SkipPathsExcludesAll(t *testing.T) {
	tp, rec := newTraceRecorder()
	mp, mr := newMetricReader()

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		TracerProvider: tp,
		MeterProvider:  mp,
		SkipPaths:      []string{"/healthz"},
	}))
	app.Get("/healthz", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)

	if got := len(rec.Ended()); got != 0 {
		t.Fatalf("spans on skip path: want 0, got %d", got)
	}
	rm := metricdata.ResourceMetrics{}
	if err := mr.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	if c := readCounter(t, &rm, "http.server.request.count"); c != 0 {
		t.Fatalf("metrics on skip path: want 0, got %d", c)
	}
}

func TestNew_LogInjection_AttachesTraceAndSpanID(t *testing.T) {
	tp, _ := newTraceRecorder()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	app := aarv.New(
		aarv.WithBanner(false),
		aarv.WithLogger(logger),
	)
	app.Use(New(Config{TracerProvider: tp}))
	app.Get("/x", func(c *aarv.Context) error {
		c.Logger().Info("hello")
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	if !strings.Contains(out, "trace_id") {
		t.Fatalf("log missing trace_id: %s", out)
	}
	if !strings.Contains(out, "span_id") {
		t.Fatalf("log missing span_id: %s", out)
	}
}

func TestNew_LoggerRestoredAfterHandler(t *testing.T) {
	tp, _ := newTraceRecorder()

	app := aarv.New(aarv.WithBanner(false))
	var inHandler, afterHandler *slog.Logger
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if c, ok := aarv.FromRequest(r); ok {
				afterHandler = c.Logger()
			}
		})
	})
	app.Use(New(Config{TracerProvider: tp}))
	app.Get("/x", func(c *aarv.Context) error {
		inHandler = c.Logger()
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)

	if inHandler == afterHandler {
		t.Fatalf("logger not restored after otel middleware unwound")
	}
}

func TestNew_StdlibPath(t *testing.T) {
	// Drive the middleware via a plain http.Handler chain to exercise the
	// stdlib path branch (no aarv Context).
	tp, rec := newTraceRecorder()
	mw := New(Config{TracerProvider: tp})
	handler := mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	rrec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/raw", nil)
	handler.ServeHTTP(rrec, req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
}

func TestNew_ConcurrentRaceClean(t *testing.T) {
	tp, _ := newTraceRecorder()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{TracerProvider: tp}))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			app.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()
}

func TestDefaultConfig_Shape(t *testing.T) {
	cfg := DefaultConfig()
	// Inverted Suppress* booleans: zero value enables every behavior.
	if cfg.SuppressErrorStatus || cfg.SuppressMetrics || cfg.SuppressLogAttrs {
		t.Fatalf("DefaultConfig: expected all Suppress* false, got %+v", cfg)
	}
}

func TestDefaultSpanName(t *testing.T) {
	if got := defaultSpanName("GET", "/x"); got != "GET /x" {
		t.Fatalf("defaultSpanName: %q", got)
	}
}

func TestClientIP_StripsPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.1:54321"
	if got := clientIP(r); got != "192.0.2.1" {
		t.Fatalf("clientIP: want 192.0.2.1, got %q", got)
	}
}

func TestClientIP_NoPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "no-port-here"
	if got := clientIP(r); got != "no-port-here" {
		t.Fatalf("clientIP no-port: got %q", got)
	}
}

func TestLoggerWithSpan_Nil(t *testing.T) {
	if got := loggerWithSpan(nil, nil); got != nil {
		t.Fatalf("loggerWithSpan nil base: want nil, got %v", got)
	}
}

func TestLoggerWithSpan_InvalidSpanContextReturnsBase(t *testing.T) {
	base := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	// A no-op span has an invalid SpanContext; loggerWithSpan should
	// short-circuit and return base unchanged.
	got := loggerWithSpan(base, trace.SpanFromContext(context.Background()))
	if got != base {
		t.Fatalf("loggerWithSpan with invalid span context should return base unchanged")
	}
}

// TestNew_CustomSpanNameFunc_NotOverriddenByPattern verifies that a
// caller-supplied SpanNameFunc is honored verbatim — finalizeSpan must NOT
// rename the span to "<METHOD> <RoutePattern>" once dispatch sets the
// pattern. The default-namer flag tracked on state is what gates the
// rename; only the package's built-in defaultSpanName triggers it.
func TestNew_CustomSpanNameFunc_NotOverriddenByPattern(t *testing.T) {
	tp, rec := newTraceRecorder()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		TracerProvider: tp,
		SpanNameFunc: func(method, path string) string {
			return "custom:" + method + " " + path
		},
	}))
	app.Get("/users/{id}", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	app.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	// SpanNameFunc returned "custom:GET /users/42" at dispatch time.
	// finalizeSpan must NOT have rewritten it to "GET /users/{id}".
	if name := spans[0].Name(); name != "custom:GET /users/42" {
		t.Fatalf("custom span name overwritten: got %q (want %q)",
			name, "custom:GET /users/42")
	}
}

// TestFinalizeSpan_OptionalAttributesPresentWhenAvailable exercises the
// TRUE branches of the user-agent, client-ip, and request-id checks (the
// FALSE branches are exercised by ...OmittedWhenEmpty below).
func TestFinalizeSpan_OptionalAttributesPresentWhenAvailable(t *testing.T) {
	tp, rec := newTraceRecorder()
	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "GET /x")

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("User-Agent", "TestAgent/1.0")
	r.RemoteAddr = "192.0.2.7:1234"
	finalizeSpan(span, "GET", "/x", "/x", 200, r, "rid-7", nil, true, true)
	span.End()

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	got := attrMap(spans[0].Attributes())
	if got["user_agent.original"] != "TestAgent/1.0" {
		t.Fatalf("user_agent.original: want TestAgent/1.0, got %v", got["user_agent.original"])
	}
	if got["client.address"] != "192.0.2.7" {
		t.Fatalf("client.address: want 192.0.2.7, got %v", got["client.address"])
	}
	if got["request_id"] != "rid-7" {
		t.Fatalf("request_id: want rid-7, got %v", got["request_id"])
	}
}

// TestFinalizeSpan_NonRecordingSpanShortCircuits exercises the early-return
// branch when the span isn't recording (e.g. dropped by sampler).
func TestFinalizeSpan_NonRecordingSpanShortCircuits(t *testing.T) {
	noop := trace.SpanFromContext(context.Background()) // non-recording
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	finalizeSpan(noop, "GET", "/x", "", 200, r, "", nil, true, true)
	// No assertions: the function must return without panicking.
}

// TestFinalizeSpan_OptionalAttributesOmittedWhenEmpty exercises the
// false branches of the user-agent, client-ip, and request-id checks.
func TestFinalizeSpan_OptionalAttributesOmittedWhenEmpty(t *testing.T) {
	tp, rec := newTraceRecorder()
	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "GET /x")

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Del("User-Agent")
	r.RemoteAddr = ""
	finalizeSpan(span, "GET", "/x", "", 200, r, "", nil, true, true)
	span.End()

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	got := attrMap(spans[0].Attributes())
	if _, ok := got["user_agent.original"]; ok {
		t.Fatal("user_agent.original must be absent when User-Agent is empty")
	}
	if _, ok := got["client.address"]; ok {
		t.Fatal("client.address must be absent when RemoteAddr is empty")
	}
	if _, ok := got["request_id"]; ok {
		t.Fatal("request_id must be absent when requestID is empty")
	}
}

// TestFinalizeSpan_HandlerErrorPath exercises the RecordError + SetStatus
// branch. The aarv framework swallows handler errors before middleware
// sees them, so the path is not reachable through the public API; this
// test calls finalizeSpan directly to lock the behavior in for any future
// caller that DOES pass an error (e.g. a wrapping middleware).
func TestFinalizeSpan_HandlerErrorPath(t *testing.T) {
	tp, rec := newTraceRecorder()
	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "GET /x")

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	wantErr := errors.New("boom")
	finalizeSpan(span, "GET", "/x", "", 200, r, "", wantErr, true, true)
	span.End()

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans: want 1, got %d", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Fatalf("status: want Error, got %v", spans[0].Status().Code)
	}
	events := spans[0].Events()
	if len(events) == 0 {
		t.Fatal("expected RecordError event")
	}
}

// TestNew_PanicInNextStillEndsSpan asserts the deferred span.End and
// recordingWriter release run on panic, so a downstream panic neither
// loses the span nor leaks the pooled writer. The span ends without
// HTTP semconv attributes (since finalizeSpan is reached only on the
// non-panic path), but it IS exported.
func TestNew_PanicInNextStillEndsSpan(t *testing.T) {
	t.Run("stdlib path", func(t *testing.T) {
		tp, rec := newTraceRecorder()
		mw := New(Config{TracerProvider: tp})
		handler := mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		}))
		func() {
			defer func() { _ = recover() }()
			rrec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			handler.ServeHTTP(rrec, req)
		}()
		if got := len(rec.Ended()); got != 1 {
			t.Fatalf("ended spans after stdlib panic: want 1, got %d", got)
		}
	})

	t.Run("native path", func(t *testing.T) {
		tp, rec := newTraceRecorder()
		app := aarv.New(aarv.WithBanner(false))
		app.Use(New(Config{TracerProvider: tp}))
		app.Get("/x", func(c *aarv.Context) error {
			panic("boom")
		})
		func() {
			defer func() { _ = recover() }()
			rrec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			app.ServeHTTP(rrec, req)
		}()
		if got := len(rec.Ended()); got != 1 {
			t.Fatalf("ended spans after native panic: want 1, got %d", got)
		}
	})
}

// TestNew_StdlibPath_SkipPathsExcluded exercises the stdlib skip branch via
// direct middleware invocation (without aarv routing).
func TestNew_StdlibPath_SkipPathsExcluded(t *testing.T) {
	tp, rec := newTraceRecorder()
	mw := New(Config{TracerProvider: tp, SkipPaths: []string{"/skipme"}})
	called := false
	handler := mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rrec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/skipme", nil)
	handler.ServeHTTP(rrec, req)

	if !called {
		t.Fatal("next not called for skipped path")
	}
	if got := len(rec.Ended()); got != 0 {
		t.Fatalf("skip path span: want 0, got %d", got)
	}
}

// TestRecordingWriter_Unwrap_RoundTrip ensures http.ResponseController can
// reach the underlying ResponseWriter through the otel wrapper.
func TestRecordingWriter_Unwrap_RoundTrip(t *testing.T) {
	underlying := httptest.NewRecorder()
	rw := acquireRecordingWriter(underlying)
	defer releaseRecordingWriter(rw)

	if rw.Unwrap() != underlying {
		t.Fatal("Unwrap did not return the underlying ResponseWriter")
	}

	rc := http.NewResponseController(rw)
	if err := rc.Flush(); err != nil {
		t.Fatalf("ResponseController.Flush via Unwrap: %v", err)
	}
}

// TestRecordHTTPMetrics_NoOpWhenSuppressed verifies the early-return when
// metrics are suppressed (state.requestCount is nil).
// errMeter is a minimal Meter whose instrument constructors all return
// the same error. Used to exercise build's panic-on-instrument-failure
// branches, which are defensive code paths unreachable with real
// MeterProvider implementations.
type errMeter struct {
	embedded.Meter
	err error
}

func (m errMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, m.err
}
func (m errMeter) Int64UpDownCounter(string, ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	return nil, m.err
}
func (m errMeter) Int64Histogram(string, ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	return nil, m.err
}
func (m errMeter) Int64Gauge(string, ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	return nil, m.err
}
func (m errMeter) Int64ObservableCounter(string, ...metric.Int64ObservableCounterOption) (metric.Int64ObservableCounter, error) {
	return nil, m.err
}
func (m errMeter) Int64ObservableUpDownCounter(string, ...metric.Int64ObservableUpDownCounterOption) (metric.Int64ObservableUpDownCounter, error) {
	return nil, m.err
}
func (m errMeter) Int64ObservableGauge(string, ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	return nil, m.err
}
func (m errMeter) Float64Counter(string, ...metric.Float64CounterOption) (metric.Float64Counter, error) {
	return nil, m.err
}
func (m errMeter) Float64UpDownCounter(string, ...metric.Float64UpDownCounterOption) (metric.Float64UpDownCounter, error) {
	return nil, m.err
}
func (m errMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return nil, m.err
}
func (m errMeter) Float64Gauge(string, ...metric.Float64GaugeOption) (metric.Float64Gauge, error) {
	return nil, m.err
}
func (m errMeter) Float64ObservableCounter(string, ...metric.Float64ObservableCounterOption) (metric.Float64ObservableCounter, error) {
	return nil, m.err
}
func (m errMeter) Float64ObservableUpDownCounter(string, ...metric.Float64ObservableUpDownCounterOption) (metric.Float64ObservableUpDownCounter, error) {
	return nil, m.err
}
func (m errMeter) Float64ObservableGauge(string, ...metric.Float64ObservableGaugeOption) (metric.Float64ObservableGauge, error) {
	return nil, m.err
}
func (m errMeter) RegisterCallback(metric.Callback, ...metric.Observable) (metric.Registration, error) {
	return nil, m.err
}

type errMeterProvider struct {
	embedded.MeterProvider
	err error
}

func (p errMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return errMeter{err: p.err}
}

// TestBuild_PanicsOnInstrumentFailures exercises the panic branch in
// mustCounter (the first call). The histogram helpers' panic branches
// are exercised directly in TestMustHistogramHelpers_Panic below.
func TestBuild_PanicsOnInstrumentFailures(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("build did not panic on Meter error")
		}
	}()
	_ = build(Config{
		MeterProvider: errMeterProvider{err: errors.New("bad-meter")},
	})
}

// TestMustHistogramHelpers_Panic exercises the panic branches of the
// must* helpers directly, since build's mustCounter panics first when a
// single error-returning meter is provided.
func TestMustHistogramHelpers_Panic(t *testing.T) {
	t.Run("mustFloat64Histogram", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("mustFloat64Histogram did not panic")
			}
		}()
		_ = mustFloat64Histogram(nil, errors.New("bad"))
	})
	t.Run("mustInt64Histogram", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("mustInt64Histogram did not panic")
			}
		}()
		_ = mustInt64Histogram(nil, errors.New("bad"))
	})
	// strconv.Itoa is only used by the previous (now-deleted) loop test;
	// reference it here so the import stays needed.
	_ = strconv.Itoa(0)
}

func TestRecordHTTPMetrics_NoOpWhenSuppressed(t *testing.T) {
	s := build(Config{SuppressMetrics: true})
	if s.requestCount != nil {
		t.Fatal("expected suppressed metrics state")
	}
	// Calling recordHTTPMetrics on a suppressed state must not panic.
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	s.recordHTTPMetrics(context.Background(), "GET", "", 200, 0, 0, 0)
	_ = r
}

func TestRecordingWriter_Pool(t *testing.T) {
	rw := acquireRecordingWriter(httptest.NewRecorder())
	rw.WriteHeader(http.StatusCreated)
	rw.WriteHeader(http.StatusInternalServerError)
	if rw.Status() != http.StatusCreated {
		t.Fatalf("status not preserved: %d", rw.Status())
	}
	if _, err := rw.Write([]byte("xy")); err != nil {
		t.Fatal(err)
	}
	if rw.BytesWritten() != 2 {
		t.Fatalf("BytesWritten: want 2, got %d", rw.BytesWritten())
	}
	releaseRecordingWriter(rw)
	releaseRecordingWriter(nil) // must not panic
}

func TestBackgroundContext_NotNil(t *testing.T) {
	if backgroundContext() == nil {
		t.Fatal("backgroundContext returned nil")
	}
}

// TestBuild_NilProvidersUseGlobalDefaults exercises the defaulting branches
// in build for TracerProvider/MeterProvider/Propagator/SpanNameFunc, and
// confirms zero-value Config produces a metric-instrumented state via the
// inverted Suppress* semantics.
func TestBuild_NilProvidersUseGlobalDefaults(t *testing.T) {
	s := build(Config{})
	if s.tracer == nil {
		t.Fatal("tracer not initialized from global default")
	}
	if s.propagator == nil {
		t.Fatal("propagator not initialized from global default")
	}
	if s.spanNameFunc == nil {
		t.Fatal("spanNameFunc not initialized")
	}
	// Zero-value Config means SuppressMetrics is false -> instruments
	// must be populated.
	if s.requestCount == nil {
		t.Fatal("metrics instruments should be populated when SuppressMetrics is false")
	}
}

// TestBuild_SuppressMetricsLeavesInstrumentsNil covers the SuppressMetrics
// path in build.
func TestBuild_SuppressMetricsLeavesInstrumentsNil(t *testing.T) {
	s := build(Config{SuppressMetrics: true})
	if s.requestCount != nil {
		t.Fatal("requestCount should be nil when SuppressMetrics is true")
	}
	if s.duration != nil {
		t.Fatal("duration should be nil when SuppressMetrics is true")
	}
}

// TestNew_RequestSizeRecordedWhenBodyPresent exercises the requestSize > 0
// branch in recordHTTPMetrics.
func TestNew_RequestSizeRecordedWhenBodyPresent(t *testing.T) {
	tp, _ := newTraceRecorder()
	mp, mr := newMetricReader()

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		TracerProvider: tp,
		MeterProvider:  mp,
	}))
	app.Post("/upload", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	body := strings.NewReader("hello world")
	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.ContentLength = int64(body.Len())
	app.ServeHTTP(httptest.NewRecorder(), req)

	rm := metricdata.ResourceMetrics{}
	if err := mr.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	if got := readHistogramCount(t, &rm, "http.server.request.size_bytes"); got != 1 {
		t.Fatalf("request size histogram count: want 1, got %d", got)
	}
}

// readHistogramCount returns the total observation count for a float64
// histogram metric across all data points.
func readHistogramCount(t *testing.T, rm *metricdata.ResourceMetrics, name string) uint64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			h64, ok := m.Data.(metricdata.Histogram[int64])
			if ok {
				var total uint64
				for _, dp := range h64.DataPoints {
					total += dp.Count
				}
				return total
			}
			h, ok2 := m.Data.(metricdata.Histogram[float64])
			if ok2 {
				var total uint64
				for _, dp := range h.DataPoints {
					total += dp.Count
				}
				return total
			}
			t.Fatalf("metric %s has unexpected type %T", name, m.Data)
		}
	}
	return 0
}

// --- helpers ---

// attrMap turns a slice of attribute.KeyValue into a map of string->any
// for ergonomic test assertions.
func attrMap(kvs []attribute.KeyValue) map[string]any {
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		switch kv.Value.Type() {
		case attribute.STRING:
			m[string(kv.Key)] = kv.Value.AsString()
		case attribute.INT64:
			m[string(kv.Key)] = kv.Value.AsInt64()
		case attribute.BOOL:
			m[string(kv.Key)] = kv.Value.AsBool()
		default:
			m[string(kv.Key)] = kv.Value.Emit()
		}
	}
	return m
}

// readCounter sums the int64 counter datapoints for the named metric across
// all recorded series in rm.
func readCounter(t *testing.T, rm *metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %s is not Sum[int64]", name)
			}
			var total int64
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
			return total
		}
	}
	return 0
}
