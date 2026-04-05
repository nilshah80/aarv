// Package verboselog provides full request/response logging middleware for the aarv framework.
//
// Unlike the standard logger plugin which logs metadata only, verboselog captures and logs:
//   - Request: method, path, headers, query params, and body
//   - Response: status, headers, and body
//
// This is useful for debugging, auditing, and API monitoring. Use with caution in production
// as it may log sensitive data and has performance overhead from body buffering.
//
// When body logging is enabled (the default), this middleware captures request body bytes
// as the handler reads them and buffers the response body up to MaxBodySize. This makes it
// a bounded-buffer middleware — avoid applying it to routes that serve very large responses
// unless body logging is disabled via Config.
//
// Usage:
//
//	// Enable JSON output for slog first
//	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
//
//	app := aarv.New()
//	app.Use(verboselog.New())
package verboselog

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
)

var bodyBufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// Config holds configuration for the dump logger middleware.
type Config struct {
	// SkipPaths is a list of URL paths to exclude from logging.
	SkipPaths []string

	// Level is the slog level used for logging.
	// Default: slog.LevelInfo.
	Level slog.Level

	// LogRequestBody enables logging of request body.
	// Only bytes actually consumed by the downstream handler are captured;
	// if the handler does not read the body, it will not appear in the log.
	// Default: true.
	LogRequestBody bool

	// LogResponseBody enables logging of response body.
	// Default: true.
	LogResponseBody bool

	// LogRequestHeaders enables logging of request headers.
	// Default: true.
	LogRequestHeaders bool

	// LogResponseHeaders enables logging of response headers.
	// Default: true.
	LogResponseHeaders bool

	// LogQueryParams enables logging of query parameters.
	// Default: true.
	LogQueryParams bool

	// LogClientIP enables logging of client IP address.
	// Default: true.
	LogClientIP bool

	// LogUserAgent enables logging of User-Agent header.
	// Default: true.
	LogUserAgent bool

	// LogContentInfo enables logging of content-type and content-length.
	// Default: true.
	LogContentInfo bool

	// LogLatencyMS enables logging of latency in milliseconds as float.
	// Default: true.
	LogLatencyMS bool

	// MaxBodySize is the maximum body size to log (in bytes).
	// Bodies larger than this are truncated with "[truncated]" marker.
	// Default: 64KB.
	MaxBodySize int

	// SensitiveHeaders is a list of header names to redact.
	// Values are replaced with "[REDACTED]".
	// Default: Authorization, Cookie, Set-Cookie, X-API-Key.
	SensitiveHeaders []string

	// SensitiveFields is a list of JSON field names to redact in body.
	// This is a simple string replacement - not full JSON parsing.
	// Default: password, token, secret, api_key, apikey.
	SensitiveFields []string

	// RedactSensitive enables sensitive data redaction.
	// Set to false to disable all redaction for maximum performance.
	// Default: true.
	RedactSensitive bool

	// RedactSurfaces narrows where sensitive data redaction is applied.
	// When empty and RedactSensitive is true, redaction applies to all supported
	// surfaces: request headers, response headers, query params, request body,
	// and response body.
	//
	// Use this to selectively redact only specific surfaces, for example:
	// []RedactionSurface{RedactRequestBody, RedactResponseBody}.
	RedactSurfaces []RedactionSurface
}

// RedactionSurface identifies a log surface where sensitive data can be redacted.
type RedactionSurface string

const (
	RedactRequestHeaders  RedactionSurface = "request_headers"
	RedactResponseHeaders RedactionSurface = "response_headers"
	RedactQueryParams     RedactionSurface = "query_params"
	RedactRequestBody     RedactionSurface = "request_body"
	RedactResponseBody    RedactionSurface = "response_body"
)

// DefaultConfig returns the default dump logger configuration.
func DefaultConfig() Config {
	return Config{
		Level:              slog.LevelInfo,
		LogRequestBody:     true,
		LogResponseBody:    true,
		LogRequestHeaders:  true,
		LogResponseHeaders: true,
		LogQueryParams:     true,
		LogClientIP:        true,
		LogUserAgent:       true,
		LogContentInfo:     true,
		LogLatencyMS:       true,
		RedactSensitive:    true,
		MaxBodySize:        64 * 1024, // 64KB
		SensitiveHeaders: []string{
			"Authorization",
			"Cookie",
			"Set-Cookie",
			"X-API-Key",
			"X-Auth-Token",
		},
		SensitiveFields: []string{
			"password",
			"token",
			"secret",
			"api_key",
			"apikey",
			"credit_card",
			"creditcard",
			"ssn",
		},
	}
}

// MinimalConfig returns a minimal configuration for maximum performance.
// Only logs method, path, status, and latency.
func MinimalConfig() Config {
	return Config{
		Level:              slog.LevelInfo,
		LogRequestBody:     false,
		LogResponseBody:    false,
		LogRequestHeaders:  false,
		LogResponseHeaders: false,
		LogQueryParams:     false,
		LogClientIP:        false,
		LogUserAgent:       false,
		LogContentInfo:     false,
		LogLatencyMS:       false,
		RedactSensitive:    false,
		MaxBodySize:        0,
	}
}

// bodyCapturingWriter captures the response body while writing through.
type bodyCapturingWriter struct {
	http.ResponseWriter
	body         *bytes.Buffer
	statusCode   int
	maxBodySize  int
	bytesWritten int64
	written      bool
}

type redactionPattern struct {
	match string
}

func (w *bodyCapturingWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *bodyCapturingWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.written = true
	}

	// Capture body up to max size
	remaining := w.maxBodySize - w.body.Len()
	if remaining > 0 {
		if len(b) <= remaining {
			_, _ = w.body.Write(b)
		} else {
			_, _ = w.body.Write(b[:remaining])
		}
	}

	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += int64(n)
	return n, err
}

func (w *bodyCapturingWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *bodyCapturingWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// bodyTeeReader wraps a request body, capturing up to limit bytes
// for logging while passing the full stream to downstream handlers.
type bodyTeeReader struct {
	reader io.ReadCloser
	buf    *bytes.Buffer
	limit  int
}

func (t *bodyTeeReader) Read(p []byte) (int, error) {
	n, err := t.reader.Read(p)
	if n > 0 {
		remaining := t.limit - t.buf.Len()
		if remaining > 0 {
			t.buf.Write(p[:min(n, remaining)])
		}
	}
	return n, err
}

func (t *bodyTeeReader) Close() error {
	return t.reader.Close()
}

func acquireBodyBuffer() *bytes.Buffer {
	buf := bodyBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

func releaseBodyBuffer(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	if buf.Cap() > aarv.MaxPooledBufferCap {
		return // discard oversized buffer
	}
	buf.Reset()
	bodyBufferPool.Put(buf)
}

// clientIP extracts the client IP from the request.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return xff
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// extractHeaders builds a map of header name to value, redacting sensitive headers.
func extractHeaders(headers http.Header, sensitiveHeaders map[string]struct{}, redact bool) map[string]string {
	m := make(map[string]string, len(headers))
	for k, v := range headers {
		if redact {
			if _, sensitive := sensitiveHeaders[k]; sensitive {
				m[k] = "[REDACTED]"
				continue
			}
		}
		m[k] = firstOrJoin(v)
	}
	return m
}

// extractQueryParams builds a map of query parameter name to value, redacting sensitive fields.
func extractQueryParams(values url.Values, sensitiveFields map[string]struct{}, redact bool) map[string]string {
	m := make(map[string]string, len(values))
	for k, v := range values {
		if redact {
			if _, sensitive := sensitiveFields[strings.ToLower(k)]; sensitive {
				m[k] = "[REDACTED]"
				continue
			}
		}
		m[k] = firstOrJoin(v)
	}
	return m
}

// formatBodyForLog truncates and optionally redacts a body byte slice for logging.
func formatBodyForLog(body []byte, maxSize int, redact bool, patterns []redactionPattern) string {
	if len(body) == 0 {
		return ""
	}
	var s string
	if len(body) > maxSize {
		s = string(body[:maxSize]) + "[truncated]"
	} else {
		s = string(body)
	}
	if redact {
		s = redactSensitiveBody(s, patterns)
	}
	return s
}

// formatRespBodyForLog truncates and optionally redacts a response body buffer for logging.
func formatRespBodyForLog(buf *bytes.Buffer, maxSize int, redact bool, patterns []redactionPattern) string {
	if buf.Len() == 0 {
		return ""
	}
	var s string
	if buf.Len() >= maxSize {
		s = buf.String() + "[truncated]"
	} else {
		s = buf.String()
	}
	if redact {
		s = redactSensitiveBody(s, patterns)
	}
	return s
}

// New creates a full request/response dump logger middleware.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	if cfg.MaxBodySize < 0 {
		cfg.MaxBodySize = 0
	}

	// Build skip paths set
	skipPaths := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skipPaths[p] = struct{}{}
	}

	// Build sensitive headers set (lowercase for case-insensitive matching)
	sensitiveHeaders := make(map[string]struct{}, len(cfg.SensitiveHeaders))
	for _, h := range cfg.SensitiveHeaders {
		sensitiveHeaders[http.CanonicalHeaderKey(h)] = struct{}{}
	}

	// Build sensitive field set for body and query redaction.
	sensitiveFields := make(map[string]struct{}, len(cfg.SensitiveFields))
	for _, field := range cfg.SensitiveFields {
		sensitiveFields[strings.ToLower(field)] = struct{}{}
	}
	redactionPatterns := buildRedactionPatterns(cfg.SensitiveFields)
	redactSurfaces := make(map[RedactionSurface]struct{}, len(cfg.RedactSurfaces))
	for _, surface := range cfg.RedactSurfaces {
		redactSurfaces[surface] = struct{}{}
	}
	shouldRedact := func(surface RedactionSurface) bool {
		if !cfg.RedactSensitive {
			return false
		}
		if len(redactSurfaces) == 0 {
			return true
		}
		_, ok := redactSurfaces[surface]
		return ok
	}

	// Pre-compute per-surface redaction booleans (avoids function call + map lookup per header)
	redactReqHeaders := shouldRedact(RedactRequestHeaders)
	redactRespHeaders := shouldRedact(RedactResponseHeaders)
	redactQueryParams := shouldRedact(RedactQueryParams)
	redactReqBody := shouldRedact(RedactRequestBody)
	redactRespBody := shouldRedact(RedactResponseBody)

	m := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Skip logging for configured paths
			if _, ok := skipPaths[path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()

			// Get request ID
			requestID := ""
			if c, ok := aarv.FromRequest(r); ok {
				requestID = c.RequestID()
			}

			// Capture request body using tee reader so downstream gets the full body.
			// The tee only records bytes the handler actually reads — it never
			// drains unread bytes, so slow/streaming uploads are not stalled.
			var reqBodyBuf *bytes.Buffer
			if cfg.LogRequestBody && r.Body != nil && r.ContentLength != 0 {
				reqBodyBuf = acquireBodyBuffer()
				r.Body = &bodyTeeReader{
					reader: r.Body,
					buf:    reqBodyBuf,
					limit:  cfg.MaxBodySize + 1,
				}
			}

			// Build request headers map
			var reqHeaders map[string]string
			if cfg.LogRequestHeaders {
				reqHeaders = extractHeaders(r.Header, sensitiveHeaders, redactReqHeaders)
			}

			// Build query params map (only if enabled)
			var queryParams map[string]string
			if cfg.LogQueryParams {
				qv := r.URL.Query()
				if len(qv) > 0 {
					queryParams = extractQueryParams(qv, sensitiveFields, redactQueryParams)
				}
			}

			// Create response body capturing writer
			respBodyBuf := acquireBodyBuffer()
			// Defer cleanup of pooled buffers to handle panics.
			defer func() {
				releaseBodyBuffer(reqBodyBuf)
				releaseBodyBuffer(respBodyBuf)
			}()
			respWriter := &bodyCapturingWriter{
				ResponseWriter: w,
				body:           respBodyBuf,
				statusCode:     http.StatusOK,
				maxBodySize:    cfg.MaxBodySize,
			}

			// Execute handler
			next.ServeHTTP(respWriter, r)

			latency := time.Since(start)

			// Build response headers map
			var respHeaders map[string]string
			if cfg.LogResponseHeaders {
				respHeaders = extractHeaders(respWriter.Header(), sensitiveHeaders, redactRespHeaders)
			}

			var reqBodyStr string
			if reqBodyBuf != nil {
				reqBodyStr = formatBodyForLog(reqBodyBuf.Bytes(), cfg.MaxBodySize, redactReqBody, redactionPatterns)
				releaseBodyBuffer(reqBodyBuf)
				reqBodyBuf = nil // prevent double-release by defer
			}
			respBodyStr := formatRespBodyForLog(respWriter.body, cfg.MaxBodySize, redactRespBody, redactionPatterns)

			// Build log attributes dynamically based on config
			var attrsBuf [32]any
			attrs := attrsBuf[:0]
			attrs = append(attrs, "request_id", requestID, "method", r.Method, "path", path)

			if cfg.LogQueryParams && len(queryParams) > 0 {
				attrs = append(attrs, "query", queryParams)
			}
			if cfg.LogClientIP {
				attrs = append(attrs, "client_ip", clientIP(r))
			}
			if cfg.LogUserAgent {
				attrs = append(attrs, "user_agent", r.UserAgent())
			}
			if cfg.LogContentInfo {
				attrs = append(attrs, "content_type", r.Header.Get("Content-Type"), "content_length", r.ContentLength)
			}
			if cfg.LogRequestHeaders && reqHeaders != nil {
				attrs = append(attrs, "request_headers", reqHeaders)
			}
			if cfg.LogRequestBody && reqBodyStr != "" {
				attrs = append(attrs, "request_body", reqBodyStr)
			}

			// Response info
			attrs = append(attrs, "status", respWriter.statusCode, "latency", latency.String())
			if cfg.LogLatencyMS {
				attrs = append(attrs, "latency_ms", float64(latency.Microseconds())/1000.0)
			}
			if cfg.LogResponseHeaders && respHeaders != nil {
				attrs = append(attrs, "response_headers", respHeaders)
			}
			if cfg.LogResponseBody && respBodyStr != "" {
				attrs = append(attrs, "response_body", respBodyStr)
			}
			attrs = append(attrs, "bytes_out", respWriter.bytesWritten)

			// Log the request/response dump
			slog.Log(r.Context(), cfg.Level, "http_dump", attrs...)

			// Release respBodyBuf on the normal path; nil prevents
			// double-release by the deferred cleanup.
			releaseBodyBuffer(respBodyBuf)
			respBodyBuf = nil
		})
	}

	native := func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			path := c.Path()
			method := c.Method()

			if _, ok := skipPaths[path]; ok {
				return next(c)
			}

			start := time.Now()
			requestID := c.RequestID()
			userAgent := ""
			if cfg.LogUserAgent {
				userAgent = c.Header("User-Agent")
			}
			clientIPValue := ""
			if cfg.LogClientIP {
				clientIPValue = c.RealIP()
			}

			// Always get the raw request — needed for headers, body, query params, or content info.
			req := c.RawRequest()

			// Capture request body using tee reader so downstream gets the full body.
			// The tee only records bytes the handler actually reads — it never
			// drains unread bytes, so slow/streaming uploads are not stalled.
			var reqBodyBuf *bytes.Buffer
			if cfg.LogRequestBody && req.Body != nil && req.ContentLength != 0 {
				reqBodyBuf = acquireBodyBuffer()
				req.Body = &bodyTeeReader{
					reader: req.Body,
					buf:    reqBodyBuf,
					limit:  cfg.MaxBodySize + 1,
				}
			}

			var reqHeaders map[string]string
			if cfg.LogRequestHeaders {
				reqHeaders = extractHeaders(req.Header, sensitiveHeaders, redactReqHeaders)
			}

			var queryParams map[string]string
			if cfg.LogQueryParams {
				// Fast path: skip url.Query() parsing if URL has no query string
				if req.URL.RawQuery != "" {
					values := c.QueryParams()
					if len(values) > 0 {
						queryParams = extractQueryParams(values, sensitiveFields, redactQueryParams)
					}
				}
			}

			respBodyBuf := acquireBodyBuffer()

			orig := c.Response()
			respWriter := &bodyCapturingWriter{
				ResponseWriter: orig,
				body:           respBodyBuf,
				statusCode:     http.StatusOK,
				maxBodySize:    cfg.MaxBodySize,
			}

			c.SetResponse(respWriter)

			var respHeaders map[string]string

			// Defer cleanup to handle panics — ensures response writer is restored
			// and pooled body buffers are released even if next(c) or post-handler
			// code panics.
			var logged bool
			defer func() {
				c.SetResponse(orig)
				if !logged {
					releaseBodyBuffer(reqBodyBuf)
					releaseBodyBuffer(respBodyBuf)
				}
			}()

			err := next(c)
			if err != nil {
				return err
			}

			latency := time.Since(start)
			if cfg.LogResponseHeaders {
				respHeaders = extractHeaders(respWriter.Header(), sensitiveHeaders, redactRespHeaders)
			}

			var reqBodyStr string
			if reqBodyBuf != nil {
				reqBodyStr = formatBodyForLog(reqBodyBuf.Bytes(), cfg.MaxBodySize, redactReqBody, redactionPatterns)
				releaseBodyBuffer(reqBodyBuf)
				reqBodyBuf = nil
			}
			respBodyStr := formatRespBodyForLog(respWriter.body, cfg.MaxBodySize, redactRespBody, redactionPatterns)

			var attrsBuf [32]any
			attrs := attrsBuf[:0]
			attrs = append(attrs, "request_id", requestID, "method", method, "path", path)
			if cfg.LogQueryParams && len(queryParams) > 0 {
				attrs = append(attrs, "query", queryParams)
			}
			if cfg.LogClientIP {
				attrs = append(attrs, "client_ip", clientIPValue)
			}
			if cfg.LogUserAgent {
				attrs = append(attrs, "user_agent", userAgent)
			}
			if cfg.LogContentInfo {
				attrs = append(attrs, "content_type", req.Header.Get("Content-Type"), "content_length", req.ContentLength)
			}
			if cfg.LogRequestHeaders && reqHeaders != nil {
				attrs = append(attrs, "request_headers", reqHeaders)
			}
			if cfg.LogRequestBody && reqBodyStr != "" {
				attrs = append(attrs, "request_body", reqBodyStr)
			}

			attrs = append(attrs, "status", respWriter.statusCode, "latency", latency.String())
			if cfg.LogLatencyMS {
				attrs = append(attrs, "latency_ms", float64(latency.Microseconds())/1000.0)
			}
			if cfg.LogResponseHeaders && respHeaders != nil {
				attrs = append(attrs, "response_headers", respHeaders)
			}
			if cfg.LogResponseBody && respBodyStr != "" {
				attrs = append(attrs, "response_body", respBodyStr)
			}
			attrs = append(attrs, "bytes_out", respWriter.bytesWritten)

			slog.Log(c.Context(), cfg.Level, "http_dump", attrs...)

			logged = true
			// reqBodyBuf already released above after formatting
			releaseBodyBuffer(respBodyBuf)
			return nil
		}
	}

	return aarv.RegisterNativeMiddleware(m, native)
}

func buildRedactionPatterns(fields []string) []redactionPattern {
	patterns := make([]redactionPattern, 0, len(fields)*3)
	for _, field := range fields {
		lowerField := strings.ToLower(field)
		for _, pattern := range [...]string{
			`"` + lowerField + `":"`,
			`"` + lowerField + `": "`,
			`"` + lowerField + `" : "`,
		} {
			patterns = append(patterns, redactionPattern{
				match: pattern,
			})
		}
	}
	return patterns
}

func redactSensitiveBody(body string, patterns []redactionPattern) string {
	if body == "" || len(patterns) == 0 {
		return body
	}

	lowerBody := strings.ToLower(body)
	for _, pattern := range patterns {
		offset := 0
		for offset < len(lowerBody) {
			idx := strings.Index(lowerBody[offset:], pattern.match)
			if idx < 0 {
				break
			}
			absIdx := offset + idx
			start := absIdx + len(pattern.match)
			if start >= len(body) {
				break
			}
			end := strings.IndexByte(body[start:], '"')
			if end <= 0 {
				break
			}
			replacement := "[REDACTED]"
			body = body[:start] + replacement + body[start+end:]
			lowerBody = strings.ToLower(body)
			offset = start + len(replacement)
		}
	}
	return body
}

func firstOrJoin(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	return strings.Join(values, ", ")
}
