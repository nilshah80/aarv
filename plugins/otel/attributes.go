// Package otel — span attributes, span finalization, log correlation.

package otel

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Modern HTTP semantic-convention attribute keys
// (go.opentelemetry.io/otel/semconv/v1.37.0). Hardcoded as strings rather
// than imported from the semconv package to keep the indirect dependency
// surface flat — the values are stable across semconv versions for these
// well-known HTTP attributes.
const (
	attrHTTPRequestMethod      = "http.request.method"
	attrURLPath                = "url.path"
	attrHTTPRoute              = "http.route"
	attrHTTPResponseStatusCode = "http.response.status_code"
	attrUserAgentOriginal      = "user_agent.original"
	attrClientAddress          = "client.address"
	attrNetworkProtocolVersion = "network.protocol.version"
)

// finalizeSpan applies HTTP semconv attributes, records errors, and sets
// the span status. Called once per request after the handler returns.
//
// The pattern argument carries c.RoutePattern() when available. When it
// is non-empty AND renameToPattern is true, the span name is upgraded to
// "<METHOD> <PATTERN>" — this is the cardinality-control rename that the
// default SpanNameFunc relies on. When renameToPattern is false (caller
// supplied a custom SpanNameFunc), the span name set at dispatch time is
// honored verbatim.
func finalizeSpan(span trace.Span, method, path, pattern string, status int, r *http.Request, requestID string, handlerErr error, recordErrors, renameToPattern bool) {
	if !span.IsRecording() {
		return
	}
	if pattern != "" && renameToPattern {
		// Upgrade the span name to use the (low-cardinality) route
		// pattern now that we have it. OTel HTTP SemConv 1.20+ pattern.
		span.SetName(method + " " + pattern)
	}

	// Modern HTTP semantic-convention attributes (semconv v1.37.0). These
	// are what current Tempo TraceQL queries, Datadog/Honeycomb auto-
	// discovery, and off-the-shelf Grafana dashboards expect.
	//
	// url.path is the raw request path. The low-cardinality matched
	// route template goes on http.route below — never on url.path,
	// because dashboards joining url.path to per-URL request volumes
	// would lose the high-cardinality view they expect.
	span.SetAttributes(
		attribute.String(attrHTTPRequestMethod, method),
		attribute.String(attrURLPath, path),
		attribute.Int(attrHTTPResponseStatusCode, status),
	)
	if pattern != "" {
		// http.route is the low-cardinality matched route pattern; pairs
		// naturally with the Prometheus plugin's `path` label and lets
		// trace search filter by registered route rather than by raw URL.
		span.SetAttributes(attribute.String(attrHTTPRoute, pattern))
	}
	if proto := networkProtocolVersion(r.Proto); proto != "" {
		span.SetAttributes(attribute.String(attrNetworkProtocolVersion, proto))
	}

	if ua := r.UserAgent(); ua != "" {
		span.SetAttributes(attribute.String(attrUserAgentOriginal, ua))
	}
	if ip := clientIP(r); ip != "" {
		span.SetAttributes(attribute.String(attrClientAddress, ip))
	}
	if requestID != "" {
		// request_id is an aarv-specific addition, not part of OTel HTTP
		// semconv; it has no legacy/modern split.
		span.SetAttributes(attribute.String("request_id", requestID))
	}

	if !recordErrors {
		// SuppressErrorStatus is set; leave the span status Unset for all
		// outcomes including 5xx and handler errors.
		return
	}
	if handlerErr != nil {
		span.RecordError(handlerErr)
		span.SetStatus(codes.Error, handlerErr.Error())
		return
	}
	if status >= 500 {
		span.SetStatus(codes.Error, http.StatusText(status))
		return
	}
	// 4xx is not an error per OTel HTTP semconv (server-side); leave
	// status Unset for both 2xx and 4xx.
}

// clientIP returns a best-effort client IP for the span's client.address
// attribute. Prefers RemoteAddr (TCP-level peer);
// X-Forwarded-For is left to applications to interpret since trust depends
// on proxy topology.
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}

// networkProtocolVersion converts an http.Request.Proto value (e.g.
// "HTTP/1.1", "HTTP/2.0") into the canonical OTel network.protocol.version
// form ("1.1", "2"). Returns "" when proto is empty or has no recognizable
// version suffix; callers should skip the attribute in that case rather
// than emitting an empty string.
func networkProtocolVersion(proto string) string {
	if proto == "" {
		return ""
	}
	const prefix = "HTTP/"
	if !strings.HasPrefix(proto, prefix) {
		return ""
	}
	ver := proto[len(prefix):]
	// Normalize "2.0" → "2" per OTel convention; leave "1.1" as is.
	if ver == "2.0" {
		return "2"
	}
	return ver
}

// loggerWithSpan returns a logger derived from base, with trace_id and
// span_id attached for log/trace correlation. Used by handleStdlib /
// handleNative to swap the request logger for the request lifetime.
func loggerWithSpan(base *slog.Logger, span trace.Span) *slog.Logger {
	if base == nil {
		return base
	}
	sc := span.SpanContext()
	if !sc.IsValid() {
		return base
	}
	return base.With(
		"trace_id", sc.TraceID().String(),
		"span_id", sc.SpanID().String(),
	)
}

// backgroundContext returns a fresh context.Background. Indirected so
// tests can assert callers use a clean context for metric recording rather
// than threading the request context (which would tie metric exemplars to
// the active span — desirable for some pipelines but out of scope for
// this plugin's defaults).
func backgroundContext() context.Context {
	return context.Background()
}
