package hmacauthotel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/hmacauth"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// fixtureProvider returns a fresh TracerProvider for tests, plus the
// exporter and a shutdown hook. Returns the provider as a pointer so
// it satisfies the trace.TracerProvider interface.
func fixtureProvider() (*sdktrace.TracerProvider, *tracetest.InMemoryExporter, func()) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	cleanup := func() { _ = tp.Shutdown(context.Background()) }
	return tp, exporter, cleanup
}

func TestObserver_SpanNameDefault(t *testing.T) {
	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()
	obs := NewObserver(WithTracerProvider(tp))

	obs(nil, hmacauth.Event{
		Outcome:  hmacauth.OutcomeOK,
		ClientID: "tester",
		Duration: time.Millisecond,
	})

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if got := spans[0].Name; got != DefaultSpanName {
		t.Fatalf("span name = %q, want %q", got, DefaultSpanName)
	}
}

func TestObserver_SpanNameOverride(t *testing.T) {
	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()
	obs := NewObserver(WithTracerProvider(tp), WithSpanName("custom.span"))

	obs(nil, hmacauth.Event{Outcome: hmacauth.OutcomeOK, Duration: time.Millisecond})

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "custom.span" {
		t.Fatalf("span name = %q, want %q", spans[0].Name, "custom.span")
	}
}

func TestObserver_AttributesAllOutcomes(t *testing.T) {
	cases := []struct {
		outcome hmacauth.Outcome
		status  int
		skew    int64
	}{
		{hmacauth.OutcomeOK, 0, 0},
		{hmacauth.OutcomeUnauthorized, 401, 0},
		{hmacauth.OutcomeSignatureInvalid, 401, 0},
		{hmacauth.OutcomeClockSkew, 401, 17},
		{hmacauth.OutcomeReplayDetected, 401, 0},
		{hmacauth.OutcomeBodyTooLarge, 413, 0},
	}
	for _, tc := range cases {
		t.Run(string(tc.outcome), func(t *testing.T) {
			tp, exporter, cleanup := fixtureProvider()
			defer cleanup()
			obs := NewObserver(WithTracerProvider(tp))

			obs(nil, hmacauth.Event{
				Outcome:     tc.outcome,
				ClientID:    "client-1",
				Status:      tc.status,
				SkewSeconds: tc.skew,
				Duration:    time.Millisecond,
			})

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("spans = %d, want 1", len(spans))
			}
			attrs := spans[0].Attributes
			want := map[string]attribute.Value{
				"auth.client_id": attribute.StringValue("client-1"),
				"auth.outcome":   attribute.StringValue(string(tc.outcome)),
			}
			if tc.status != 0 {
				want["auth.response_status"] = attribute.IntValue(tc.status)
			}
			if tc.outcome == hmacauth.OutcomeClockSkew {
				want["auth.skew_seconds"] = attribute.Int64Value(tc.skew)
			}
			got := map[string]attribute.Value{}
			for _, a := range attrs {
				got[string(a.Key)] = a.Value
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("attr %s = %v, want %v", k, got[k], v)
				}
			}
			// Reverse: ensure no unexpected attrs (Status should NOT
			// appear when zero; skew should NOT appear unless outcome
			// is ClockSkew).
			if tc.status == 0 {
				if _, ok := got["auth.response_status"]; ok {
					t.Errorf("auth.response_status set on zero-status outcome")
				}
			}
			if tc.outcome != hmacauth.OutcomeClockSkew {
				if _, ok := got["auth.skew_seconds"]; ok {
					t.Errorf("auth.skew_seconds set on non-skew outcome")
				}
			}
		})
	}
}

func TestObserver_StatusErrorOnNonOK(t *testing.T) {
	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()
	obs := NewObserver(WithTracerProvider(tp))

	obs(nil, hmacauth.Event{
		Outcome: hmacauth.OutcomeSignatureInvalid,
		Status:  401,
		Duration: time.Millisecond,
	})

	spans := exporter.GetSpans()
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("status code = %v, want Error for OutcomeSignatureInvalid", spans[0].Status.Code)
	}
}

func TestObserver_StatusUnsetOnOK(t *testing.T) {
	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()
	obs := NewObserver(WithTracerProvider(tp))

	obs(nil, hmacauth.Event{Outcome: hmacauth.OutcomeOK, Duration: time.Millisecond})

	if got := exporter.GetSpans()[0].Status.Code; got != codes.Unset {
		t.Fatalf("status code = %v, want Unset for OutcomeOK", got)
	}
}

func TestObserver_OmitsZeroStatus(t *testing.T) {
	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()
	obs := NewObserver(WithTracerProvider(tp))

	obs(nil, hmacauth.Event{Outcome: hmacauth.OutcomeOK, Status: 0, Duration: time.Millisecond})

	for _, a := range exporter.GetSpans()[0].Attributes {
		if string(a.Key) == "auth.response_status" {
			t.Fatalf("auth.response_status set when Status == 0 (got %v)", a.Value)
		}
	}
}

func TestObserver_OmitsSkewWhenNotClockSkew(t *testing.T) {
	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()
	obs := NewObserver(WithTracerProvider(tp))

	// Pass a non-zero SkewSeconds with a non-ClockSkew outcome; the
	// attribute must NOT appear on the span.
	obs(nil, hmacauth.Event{
		Outcome:     hmacauth.OutcomeUnauthorized,
		Status:      401,
		SkewSeconds: 100,
		Duration:    time.Millisecond,
	})

	for _, a := range exporter.GetSpans()[0].Attributes {
		if string(a.Key) == "auth.skew_seconds" {
			t.Fatalf("auth.skew_seconds leaked into non-ClockSkew outcome")
		}
	}
}

func TestObserver_DefaultProviderFallback(t *testing.T) {
	// Install our recorder as the GLOBAL TracerProvider, then call
	// NewObserver with no WithTracerProvider option. The Observer must
	// pick up the global at call time.
	prevTP := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prevTP) })

	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()
	otel.SetTracerProvider(tp)

	obs := NewObserver() // no options

	obs(nil, hmacauth.Event{Outcome: hmacauth.OutcomeOK, Duration: time.Millisecond})

	if got := len(exporter.GetSpans()); got != 1 {
		t.Fatalf("global provider not used: spans = %d", got)
	}
}

func TestObserver_NilContextParenting(t *testing.T) {
	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()
	obs := NewObserver(WithTracerProvider(tp))

	// Must not panic on c == nil; span becomes a root span.
	obs(nil, hmacauth.Event{Outcome: hmacauth.OutcomeOK, Duration: time.Millisecond})

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	// Root span has a zero parent span ID.
	if spans[0].Parent.SpanID().IsValid() {
		t.Fatalf("parent span ID should be zero for nil-context Observer, got %v",
			spans[0].Parent.SpanID())
	}
}

func TestObserver_SpanIsBackdated(t *testing.T) {
	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()
	obs := NewObserver(WithTracerProvider(tp))

	const verifyDuration = 25 * time.Millisecond
	before := time.Now()
	obs(nil, hmacauth.Event{Outcome: hmacauth.OutcomeOK, Duration: verifyDuration})
	after := time.Now()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	span := spans[0]
	got := span.EndTime.Sub(span.StartTime)

	// Allow a small tolerance for time.Now() drift between span Start
	// and End calls (observer captures endTime ONCE, then uses it for
	// both span.Start(WithTimestamp(end-duration)) and
	// span.End(WithTimestamp(end)). So the recorded window should be
	// exactly verifyDuration, modulo SDK clock resolution.
	const tolerance = 2 * time.Millisecond
	if got < verifyDuration-tolerance || got > verifyDuration+tolerance {
		t.Fatalf("span window = %v, want ~%v (verify duration)", got, verifyDuration)
	}
	if span.StartTime.Before(before.Add(-verifyDuration)) || span.EndTime.After(after) {
		t.Fatalf("span window [%v..%v] outside expected envelope [%v..%v]",
			span.StartTime, span.EndTime, before.Add(-verifyDuration), after)
	}
}

// TestObserver_UsesContextWhenAvailable exercises the parent-context
// branch: when c is non-nil and c.Context() carries a parent span, the
// emitted span must be parented under it. Uses App.AcquireContext to
// obtain a real *aarv.Context — the framework's public test surface.
func TestObserver_UsesContextWhenAvailable(t *testing.T) {
	tp, exporter, cleanup := fixtureProvider()
	defer cleanup()

	// Start a parent span; capture its context.
	tracer := tp.Tracer("test")
	parentCtx, parentSpan := tracer.Start(context.Background(), "parent")
	defer parentSpan.End()
	wantParentSpanID := parentSpan.SpanContext().SpanID()

	// Build a real *aarv.Context with the parent context attached.
	app := aarv.New(aarv.WithBanner(false))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	c := app.AcquireContext(rec, req)
	defer app.ReleaseContext(c)
	c.SetContext(parentCtx)

	obs := NewObserver(WithTracerProvider(tp))
	obs(c, hmacauth.Event{Outcome: hmacauth.OutcomeOK, Duration: time.Millisecond})

	// Need to find the child span: tracetest collects all ended spans in
	// the exporter; parentSpan is still open. The Observer-emitted span is
	// the only ended span.
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	gotParent := spans[0].Parent.SpanID()
	if !gotParent.IsValid() {
		t.Fatal("parent span ID should be set when *aarv.Context carries a parent context")
	}
	if gotParent != wantParentSpanID {
		t.Fatalf("parent span ID = %v, want %v", gotParent, wantParentSpanID)
	}
}
