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
	target := pattern
	if target == "" {
		target = path
	} else if renameToPattern {
		// Upgrade the span name to use the (low-cardinality) route
		// pattern now that we have it. OTel HTTP SemConv 1.20+ pattern.
		span.SetName(method + " " + pattern)
	}

	span.SetAttributes(
		attribute.String("http.method", method),
		attribute.String("http.target", target),
		attribute.Int("http.status_code", status),
	)
	if ua := r.UserAgent(); ua != "" {
		span.SetAttributes(attribute.String("http.user_agent", ua))
	}
	if ip := clientIP(r); ip != "" {
		span.SetAttributes(attribute.String("net.peer.ip", ip))
	}
	if requestID != "" {
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

// clientIP returns a best-effort client IP for the span's net.peer.ip
// attribute. Prefers RemoteAddr (TCP-level peer); X-Forwarded-For is left
// to applications to interpret since trust depends on proxy topology.
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
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
