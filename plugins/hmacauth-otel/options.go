package hmacauthotel

import (
	"go.opentelemetry.io/otel/trace"
)

// Option configures a NewObserver call. Apply via NewObserver(opts ...).
type Option func(*config)

// config holds resolved option values. Internal.
type config struct {
	tracerProvider trace.TracerProvider
	spanName       string
}

// DefaultSpanName is the span name emitted by Observer when not
// overridden via WithSpanName. Matches the wording used in ALP's
// pre-v0.9.0 traceHMAC wrapper so dashboards keyed on the literal
// span name continue to work after migrating to this adapter.
const DefaultSpanName = "auth.HMAC.verify"

// WithTracerProvider sets the OpenTelemetry TracerProvider the
// Observer uses to acquire its tracer. When unset, the Observer
// falls back to otel.GetTracerProvider() at every call (so a
// late-installed global provider is picked up automatically).
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) { c.tracerProvider = tp }
}

// WithSpanName overrides the span name. Empty string is ignored —
// the default name is used in that case.
func WithSpanName(name string) Option {
	return func(c *config) {
		if name != "" {
			c.spanName = name
		}
	}
}
