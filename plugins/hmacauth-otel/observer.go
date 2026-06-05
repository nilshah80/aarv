package hmacauthotel

import (
	"context"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/hmacauth"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// instrumentationName identifies this adapter as the originator of
// emitted spans. Surfaces in trace metadata; allows operators to
// distinguish adapter-emitted spans from manually-created ones.
const instrumentationName = "github.com/nilshah80/aarv/plugins/hmacauth-otel"

// NewObserver returns an hmacauth.Observer that emits one
// "auth.HMAC.verify" span per verification attempt with the canonical
// attribute schema described in the package doc.
//
// The returned Observer:
//
//   - Acquires its tracer from the configured TracerProvider, or from
//     otel.GetTracerProvider() when none is provided.
//   - Uses c.Context() as the parent when c is non-nil, else falls
//     back to context.Background() (a root span — non-aarv stdlib
//     mounts hit this path).
//   - Backdates the span by Event.Duration so its recorded window
//     reflects verification time, not observer-callback overhead.
//   - Sets codes.Error on any outcome other than OutcomeOK so trace
//     search filters keyed on span status work directly.
//   - Omits auth.response_status when Event.Status == 0 (the
//     verification succeeded and the handler decides the actual
//     response status downstream).
//   - Omits auth.skew_seconds for any outcome other than
//     OutcomeClockSkew, so the attribute is meaningful when present.
func NewObserver(opts ...Option) hmacauth.Observer {
	cfg := config{
		spanName: DefaultSpanName,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(c *aarv.Context, event hmacauth.Event) {
		tp := cfg.tracerProvider
		if tp == nil {
			tp = otel.GetTracerProvider()
		}
		tracer := tp.Tracer(instrumentationName)

		var parent context.Context
		if c != nil && c.Context() != nil {
			parent = c.Context()
		} else {
			parent = context.Background()
		}

		endTime := time.Now()
		startTime := endTime.Add(-event.Duration)

		_, span := tracer.Start(parent, cfg.spanName,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithTimestamp(startTime),
		)

		// Always-present attributes.
		span.SetAttributes(
			attribute.String("auth.client_id", event.ClientID),
			attribute.String("auth.outcome", string(event.Outcome)),
		)
		// Conditional attributes.
		if event.Status != 0 {
			span.SetAttributes(attribute.Int("auth.response_status", event.Status))
		}
		if event.Outcome == hmacauth.OutcomeClockSkew {
			span.SetAttributes(attribute.Int64("auth.skew_seconds", event.SkewSeconds))
		}

		// Status mapping.
		if event.Outcome != hmacauth.OutcomeOK {
			span.SetStatus(codes.Error, string(event.Outcome))
		}

		span.End(trace.WithTimestamp(endTime))
	}
}
