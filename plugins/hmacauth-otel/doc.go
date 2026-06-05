// Package hmacauthotel is the OpenTelemetry adapter for
// plugins/hmacauth's Observer hook. It converts each verification
// Event into an "auth.HMAC.verify" span carrying the canonical
// attribute schema:
//
//   - auth.client_id      — Event.ClientID
//   - auth.outcome        — string(Event.Outcome)
//   - auth.response_status — Event.Status (omitted when zero)
//   - auth.skew_seconds   — Event.SkewSeconds (only when outcome = ClockSkew)
//
// The span is backdated by Event.Duration so its recorded window
// matches the actual verification time, not the observer-callback
// overhead. Span status is set to codes.Error on any outcome other
// than OutcomeOK so Tempo / Honeycomb / Grafana filters like
// `{ name = "auth.HMAC.verify" && status = error }` work directly.
//
// # Why this is a separate module
//
// plugins/hmacauth lives in the root aarv module, which is
// intentionally zero-dependency. Importing go.opentelemetry.io/otel
// into the root would force OTel onto every aarv consumer, even
// those who don't care about tracing. The Observer hook + companion
// module split lets root stay zero-dep while shipping a turnkey OTel
// adapter for consumers who do want tracing.
//
// # Quick start
//
//	import (
//	    "github.com/nilshah80/aarv/plugins/hmacauth"
//	    hmacotel "github.com/nilshah80/aarv/plugins/hmacauth-otel"
//	)
//
//	cfg := hmacauth.Config{ /* … */ }
//	cfg.Observer = hmacotel.NewObserver()  // uses otel.GetTracerProvider()
//	app.Use(hmacauth.New(cfg))
//
// # Bring your own provider
//
// Pass a TracerProvider explicitly when you want to scope spans to a
// non-global tracer (typical in test code, or when running multiple
// apps in one process):
//
//	cfg.Observer = hmacotel.NewObserver(
//	    hmacotel.WithTracerProvider(tp),
//	    hmacotel.WithSpanName("auth.HMAC.verify"),
//	)
package hmacauthotel
