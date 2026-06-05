package prometheus

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nilshah80/aarv"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// freshRegistry returns a fresh, isolated *prometheus.Registry so each test
// gets its own metric namespace without polluting prometheus.DefaultRegisterer.
func freshRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

// counterValue returns the current value of a labeled counter, or zero if
// the metric or label combination is absent.
func counterValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range metrics {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			if labelMatch(m.Label, labels) {
				if m.Counter != nil {
					return m.Counter.GetValue()
				}
				if m.Gauge != nil {
					return m.Gauge.GetValue()
				}
			}
		}
	}
	return 0
}

// histogramSampleCount returns the total observation count for a labeled
// histogram series.
func histogramSampleCount(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) uint64 {
	t.Helper()
	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range metrics {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			if labelMatch(m.Label, labels) && m.Histogram != nil {
				return m.Histogram.GetSampleCount()
			}
		}
	}
	return 0
}

func labelMatch(got []*dto.LabelPair, want map[string]string) bool {
	if len(got) < len(want) {
		return false
	}
	for k, v := range want {
		found := false
		for _, lp := range got {
			if lp.GetName() == k && lp.GetValue() == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestNew_RecordsCounterAndDuration(t *testing.T) {
	reg := freshRegistry()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Registerer: reg}))
	app.Get("/users/{id}", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
		app.ServeHTTP(rec, req)
	}

	labels := map[string]string{"method": "GET", "path": "/users/{id}", "status": "200"}
	if got := counterValue(t, reg, "http_requests_total", labels); got != 3 {
		t.Fatalf("counter: want 3, got %v", got)
	}
	if got := histogramSampleCount(t, reg, "http_request_duration_seconds", labels); got != 3 {
		t.Fatalf("duration sample count: want 3, got %d", got)
	}
}

func TestNew_DefaultGroupPath_CollapsesDynamicRoutes(t *testing.T) {
	reg := freshRegistry()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Registerer: reg}))
	app.Get("/users/{id}", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	for _, id := range []string{"1", "2", "3", "4", "5"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/users/"+id, nil)
		app.ServeHTTP(rec, req)
	}

	// All five distinct request paths must collapse to a single label set.
	labels := map[string]string{"method": "GET", "path": "/users/{id}", "status": "200"}
	if got := counterValue(t, reg, "http_requests_total", labels); got != 5 {
		t.Fatalf("counter: want 5 collapsed under /users/{id}, got %v", got)
	}
}

func TestNew_CustomGroupPathHonored(t *testing.T) {
	reg := freshRegistry()
	cfg := Config{
		Registerer: reg,
		GroupPath: func(c *aarv.Context) string {
			return "ALL"
		},
	}
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/anything", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	app.ServeHTTP(rec, req)

	labels := map[string]string{"method": "GET", "path": "ALL", "status": "200"}
	if got := counterValue(t, reg, "http_requests_total", labels); got != 1 {
		t.Fatalf("custom group path not honored: got %v", got)
	}
}

func TestNew_EmptyGroupPathExcludes(t *testing.T) {
	reg := freshRegistry()
	cfg := Config{
		Registerer: reg,
		GroupPath: func(c *aarv.Context) string {
			return ""
		},
	}
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(rec, req)

	if got := counterValue(t, reg, "http_requests_total", nil); got != 0 {
		t.Fatalf("empty group path should exclude metrics: got %v", got)
	}
}

func TestNew_SkipPathsExcluded(t *testing.T) {
	reg := freshRegistry()
	cfg := Config{
		Registerer: reg,
		SkipPaths:  []string{"/healthz"},
	}
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/healthz", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})
	app.Get("/echo", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	for _, path := range []string{"/healthz", "/healthz", "/echo"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		app.ServeHTTP(rec, req)
	}

	echoLabels := map[string]string{"method": "GET", "path": "/echo", "status": "200"}
	healthLabels := map[string]string{"method": "GET", "path": "/healthz", "status": "200"}
	if got := counterValue(t, reg, "http_requests_total", echoLabels); got != 1 {
		t.Fatalf("echo counter: want 1, got %v", got)
	}
	if got := counterValue(t, reg, "http_requests_total", healthLabels); got != 0 {
		t.Fatalf("healthz must be skipped: got %v", got)
	}
}

func TestNew_NamespaceAndSubsystemPrefix(t *testing.T) {
	reg := freshRegistry()
	cfg := Config{
		Registerer: reg,
		Namespace:  "aarv",
		Subsystem:  "test",
	}
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(rec, req)

	labels := map[string]string{"method": "GET", "path": "/x", "status": "200"}
	if got := counterValue(t, reg, "aarv_test_http_requests_total", labels); got != 1 {
		t.Fatalf("namespace/subsystem prefix not applied: got %v", got)
	}
}

func TestNew_CustomCollectorRegistered(t *testing.T) {
	reg := freshRegistry()
	custom := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "custom_thing_total",
		Help: "test",
	})
	custom.Inc()
	custom.Inc()

	cfg := Config{
		Registerer: reg,
		Custom:     []prometheus.Collector{custom},
	}
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))

	if got := counterValue(t, reg, "custom_thing_total", nil); got != 2 {
		t.Fatalf("custom collector not registered: got %v", got)
	}
}

func TestNew_InFlightGaugeIncrementsAndDecrements(t *testing.T) {
	reg := freshRegistry()
	gateEntered := make(chan struct{})
	gateRelease := make(chan struct{})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Registerer: reg}))
	app.Get("/slow", func(c *aarv.Context) error {
		close(gateEntered)
		<-gateRelease
		return c.Text(http.StatusOK, "ok")
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/slow", nil)
		app.ServeHTTP(rec, req)
	}()

	<-gateEntered
	if got := counterValue(t, reg, "http_requests_in_flight", nil); got != 1 {
		t.Fatalf("in-flight gauge during request: want 1, got %v", got)
	}
	close(gateRelease)
	<-done
	if got := counterValue(t, reg, "http_requests_in_flight", nil); got != 0 {
		t.Fatalf("in-flight gauge after request: want 0, got %v", got)
	}
}

func TestHandler_ScrapesRegisteredMetrics(t *testing.T) {
	reg := freshRegistry()
	cfg := Config{Registerer: reg, SkipPaths: []string{"/metrics"}}
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	// Register the scrape endpoint as a regular route. Mount appends a
	// trailing slash and triggers a 307 redirect when scraped at /metrics
	// (no slash); a plain Get keeps the canonical Prometheus path.
	scrape := Handler(cfg)
	app.Get("/metrics", func(c *aarv.Context) error {
		scrape.ServeHTTP(c.Response(), c.Request())
		return nil
	})
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	// Generate one request, then scrape metrics.
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		app.ServeHTTP(rec, req)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics endpoint status: want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "http_requests_total") {
		t.Fatalf("metrics output missing requests_total: %s", body)
	}
}

func TestRecordingWriter_StatusAndByteCount(t *testing.T) {
	reg := freshRegistry()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Registerer: reg}))
	app.Get("/sized", func(c *aarv.Context) error {
		return c.Text(http.StatusCreated, "01234567890123456789") // 20 bytes
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sized", nil)
	app.ServeHTTP(rec, req)

	labels := map[string]string{"method": "GET", "path": "/sized", "status": "201"}
	if got := histogramSampleCount(t, reg, "http_response_size_bytes", labels); got != 1 {
		t.Fatalf("size histogram: want 1 sample, got %d", got)
	}
}

// TestNew_StdlibPath drives the middleware via the plain http.Handler chain
// without going through aarv routing — exercises the stdlib branch where
// no aarv.Context is reachable from the request. The path label falls back
// to r.URL.Path in that case.
func TestNew_StdlibPath(t *testing.T) {
	reg := freshRegistry()
	mw := New(Config{Registerer: reg})
	handler := mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/from-stdlib", nil)
	handler.ServeHTTP(rec, req)

	labels := map[string]string{"method": "GET", "path": "/from-stdlib", "status": "200"}
	if got := counterValue(t, reg, "http_requests_total", labels); got != 1 {
		t.Fatalf("stdlib path counter: want 1, got %v", got)
	}
}

func TestNew_ConcurrentRequestsRaceClean(t *testing.T) {
	reg := freshRegistry()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Registerer: reg}))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			app.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	labels := map[string]string{"method": "GET", "path": "/x", "status": "200"}
	if got := counterValue(t, reg, "http_requests_total", labels); got != 50 {
		t.Fatalf("counter under concurrency: want 50, got %v", got)
	}
}

// TestBuildMetrics_NilRegistererFallsBackToDefault exercises the nil-Registerer
// branch in buildMetrics. We use a unique namespace so the registration
// against DefaultRegisterer cannot collide with concurrent tests or repeat
// runs of the same binary.
func TestBuildMetrics_NilRegistererFallsBackToDefault(t *testing.T) {
	defer func() {
		// Recover any double-registration panic from a repeat run.
		_ = recover()
	}()
	_ = New(Config{Namespace: "aarv_test_nil_registerer"})
}

func TestHandler_NilRegistererFallsBackToDefault(t *testing.T) {
	// Just verify the handler returns a non-nil http.Handler — exercising
	// the nil-registerer branch. We do not assert what the default registry
	// contains because other tests may have populated it.
	h := Handler(Config{})
	if h == nil {
		t.Fatal("Handler returned nil")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handler status: want 200, got %d", rec.Code)
	}
}

func TestRecordingWriter_PoolReuse(t *testing.T) {
	w1 := acquireRecordingWriter(httptest.NewRecorder())
	w1.WriteHeader(http.StatusTeapot)
	if _, err := w1.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	releaseRecordingWriter(w1)

	w2 := acquireRecordingWriter(httptest.NewRecorder())
	if w2.Status() != http.StatusOK {
		t.Fatalf("acquired writer not reset: status %d", w2.Status())
	}
	if w2.BytesWritten() != 0 {
		t.Fatalf("acquired writer not reset: bytes %d", w2.BytesWritten())
	}
	releaseRecordingWriter(w2)
}

func TestRecordingWriter_WriteHeaderIdempotent(t *testing.T) {
	rw := acquireRecordingWriter(httptest.NewRecorder())
	rw.WriteHeader(http.StatusCreated)
	rw.WriteHeader(http.StatusInternalServerError) // second call ignored for status tracking
	if rw.Status() != http.StatusCreated {
		t.Fatalf("status not preserved after second WriteHeader: %d", rw.Status())
	}
}

func TestRecordingWriter_WriteWithoutExplicitHeader(t *testing.T) {
	rw := acquireRecordingWriter(httptest.NewRecorder())
	if _, err := io.WriteString(rw, "hello"); err != nil {
		t.Fatal(err)
	}
	if rw.BytesWritten() != 5 {
		t.Fatalf("BytesWritten: want 5, got %d", rw.BytesWritten())
	}
}

func TestReleaseRecordingWriter_NilSafe(t *testing.T) {
	releaseRecordingWriter(nil) // must not panic
}

// TestNew_StdlibPath_SkipPathsExcluded exercises the stdlib skip branch
// via direct middleware invocation (without aarv routing).
// TestNew_PanicInNextDoesNotSkewInFlight asserts that a panic from inside
// next.ServeHTTP / next(c) leaves http_requests_in_flight at the value
// it had BEFORE the request — i.e. the deferred Inc/Dec pair brackets
// the panic. Without the defer, a panicking handler would permanently
// skew the gauge upward.
func TestNew_PanicInNextDoesNotSkewInFlight(t *testing.T) {
	t.Run("stdlib path", func(t *testing.T) {
		reg := freshRegistry()
		mw := New(Config{Registerer: reg})
		handler := mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		}))
		func() {
			defer func() { _ = recover() }()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			handler.ServeHTTP(rec, req)
		}()
		if got := counterValue(t, reg, "http_requests_in_flight", nil); got != 0 {
			t.Fatalf("in-flight gauge after panic: want 0, got %v", got)
		}
	})

	t.Run("native path", func(t *testing.T) {
		reg := freshRegistry()
		app := aarv.New(aarv.WithBanner(false))
		app.Use(New(Config{Registerer: reg}))
		app.Get("/x", func(c *aarv.Context) error {
			panic("boom")
		})
		func() {
			defer func() { _ = recover() }()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			app.ServeHTTP(rec, req)
		}()
		if got := counterValue(t, reg, "http_requests_in_flight", nil); got != 0 {
			t.Fatalf("native in-flight gauge after panic: want 0, got %v", got)
		}
	})
}

func TestNew_StdlibPath_SkipPathsExcluded(t *testing.T) {
	reg := freshRegistry()
	mw := New(Config{Registerer: reg, SkipPaths: []string{"/skipme"}})
	called := false
	handler := mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/skipme", nil)
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("next not called for skipped path")
	}
	if got := counterValue(t, reg, "http_requests_total", nil); got != 0 {
		t.Fatalf("skip path metric: want 0, got %v", got)
	}
}

// nonGathererRegisterer implements prometheus.Registerer but not
// prometheus.Gatherer — used to exercise the Handler fallback that swaps in
// DefaultGatherer when the configured Registerer cannot gather.
type nonGathererRegisterer struct{}

func (nonGathererRegisterer) Register(prometheus.Collector) error  { return nil }
func (nonGathererRegisterer) MustRegister(...prometheus.Collector) {}
func (nonGathererRegisterer) Unregister(prometheus.Collector) bool { return false }

func TestHandler_NonGathererRegistererFallsBack(t *testing.T) {
	cfg := Config{Registerer: nonGathererRegisterer{}}
	h := Handler(cfg)
	if h == nil {
		t.Fatal("Handler returned nil")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
}

// TestRecordingWriter_Unwrap_RoundTrip ensures http.ResponseController can
// reach the underlying writer through the wrapper. Without Unwrap, the
// controller refuses to delegate and downstream flush/hijack/push operations
// silently fail.
func TestRecordingWriter_Unwrap_RoundTrip(t *testing.T) {
	underlying := httptest.NewRecorder()
	rw := acquireRecordingWriter(underlying)
	defer releaseRecordingWriter(rw)

	if rw.Unwrap() != underlying {
		t.Fatal("Unwrap did not return the underlying ResponseWriter")
	}

	// http.ResponseController consults Unwrap to reach Flusher; httptest's
	// recorder implements Flusher so this round-trips successfully.
	rc := http.NewResponseController(rw)
	if err := rc.Flush(); err != nil {
		t.Fatalf("ResponseController.Flush via Unwrap: %v", err)
	}
}

// TestPathLabel_NoContextFallsBack verifies the helper that computes the
// path label when the stdlib path runs without a reachable aarv.Context.
func TestPathLabel_NoContextFallsBack(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/raw", nil)
	got := pathLabel(nil, req, defaultGroupPath)
	if got != "/raw" {
		t.Fatalf("pathLabel without context: want /raw, got %q", got)
	}
}

// TestDefaultGroupPath_FallsBackToRawPath exercises the branch in
// defaultGroupPath where c.RoutePattern is empty (e.g. unmatched routes).
// The 404 path goes through the middleware on its way back up; the path
// label falls back to c.Path() — verbatim "/missing" in this case.
func TestDefaultGroupPath_FallsBackToRawPath(t *testing.T) {
	reg := freshRegistry()
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Registerer: reg}))
	// Register at least one route so the app routes properly; missing path
	// triggers 404 which still flows through the middleware.
	app.Get("/registered", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	app.ServeHTTP(rec, req)

	labels := map[string]string{"method": "GET", "path": "/missing", "status": "404"}
	if got := counterValue(t, reg, "http_requests_total", labels); got != 1 {
		t.Fatalf("404 fallback path label: want 1 at /missing, got %v", got)
	}
}

// TestNew_StdlibPath_EmptyGroupPathExcludes exercises the stdlib branch
// where pathLabel returns "" — the early-exit path. We hit a 404 (which
// goes through the stdlib middleware chain in a.handler.ServeHTTP) with a
// GroupPath that returns "" to drop the metric.
func TestNew_StdlibPath_EmptyGroupPathExcludes(t *testing.T) {
	reg := freshRegistry()
	cfg := Config{
		Registerer: reg,
		GroupPath: func(c *aarv.Context) string {
			return ""
		},
	}
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/registered", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	// 404 path goes through a.handler.ServeHTTP -> stdlib middleware chain.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	app.ServeHTTP(rec, req)

	if got := counterValue(t, reg, "http_requests_total", nil); got != 0 {
		t.Fatalf("stdlib path empty group: expected no metrics, got %v", got)
	}
}

// TestNew_NativePath_EmptyGroupPathExcludes exercises the native-path
// branch where GroupPath returns "". A route registered as an aarv handler
// with global native middleware fires the native fast path.
func TestNew_NativePath_EmptyGroupPathExcludes(t *testing.T) {
	reg := freshRegistry()
	cfg := Config{
		Registerer: reg,
		GroupPath: func(c *aarv.Context) string {
			return ""
		},
	}
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(rec, req)

	if got := counterValue(t, reg, "http_requests_total", nil); got != 0 {
		t.Fatalf("native path empty group path: expected 0 metric, got %v", got)
	}
}

// TestSubMillisecondBuckets_Shape asserts the documented properties of
// the preset: starts at 100µs, monotonically increasing, ends at 5s. This
// is the contract every consumer relies on when choosing the preset over
// DefaultBuckets.
func TestSubMillisecondBuckets_Shape(t *testing.T) {
	if len(SubMillisecondBuckets) == 0 {
		t.Fatal("SubMillisecondBuckets must not be empty")
	}
	if got := SubMillisecondBuckets[0]; got != 0.0001 {
		t.Fatalf("SubMillisecondBuckets[0] = %v, want 0.0001 (100µs)", got)
	}
	last := SubMillisecondBuckets[len(SubMillisecondBuckets)-1]
	if last != 5 {
		t.Fatalf("SubMillisecondBuckets last = %v, want 5", last)
	}
	for i := 1; i < len(SubMillisecondBuckets); i++ {
		if SubMillisecondBuckets[i] <= SubMillisecondBuckets[i-1] {
			t.Fatalf("SubMillisecondBuckets not strictly increasing at index %d: %v then %v",
				i, SubMillisecondBuckets[i-1], SubMillisecondBuckets[i])
		}
	}
}

// TestSubMillisecondBuckets_AppliedViaConfig confirms the preset is
// accepted by Config.Buckets and produces the expected bucket boundaries
// on the duration histogram. Guards against a future refactor that
// changes how Buckets is consumed.
func TestSubMillisecondBuckets_AppliedViaConfig(t *testing.T) {
	reg := freshRegistry()
	cfg := Config{
		Registerer: reg,
		Buckets:    SubMillisecondBuckets,
	}
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(cfg))
	app.Get("/x", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(rec, req)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var hist *dto.Histogram
	for _, mf := range mfs {
		if mf.GetName() != "http_request_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if h := m.GetHistogram(); h != nil {
				hist = h
				break
			}
		}
	}
	if hist == nil {
		t.Fatal("http_request_duration_seconds histogram not found")
	}
	got := hist.GetBucket()
	if len(got) != len(SubMillisecondBuckets) {
		t.Fatalf("bucket count = %d, want %d", len(got), len(SubMillisecondBuckets))
	}
	for i, b := range got {
		if b.GetUpperBound() != SubMillisecondBuckets[i] {
			t.Fatalf("bucket[%d] upper = %v, want %v", i, b.GetUpperBound(), SubMillisecondBuckets[i])
		}
	}
}
