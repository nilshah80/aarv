// Package verboselog provides full request/response logging middleware for the aarv framework.
//
// Unlike the standard logger plugin which logs metadata only, verboselog captures and logs:
//   - Request: method, path, headers, query params, and body
//   - Response: status, headers, and body
//
// This is useful for debugging, auditing, and API monitoring. Use with caution in production
// as it may log sensitive data and has performance overhead from body buffering.
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
	"strings"
	"time"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the dump logger middleware.
type Config struct {
	// SkipPaths is a list of URL paths to exclude from logging.
	SkipPaths []string

	// Level is the slog level used for logging.
	// Default: slog.LevelInfo.
	Level slog.Level

	// LogRequestBody enables logging of request body.
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
}

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
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

// New creates a full request/response dump logger middleware.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	// Build skip paths set
	skipPaths := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skipPaths[p] = struct{}{}
	}

	// Build sensitive headers set (lowercase for case-insensitive matching)
	sensitiveHeaders := make(map[string]struct{}, len(cfg.SensitiveHeaders))
	for _, h := range cfg.SensitiveHeaders {
		sensitiveHeaders[strings.ToLower(h)] = struct{}{}
	}

	// Build sensitive field set for body and query redaction.
	sensitiveFields := make(map[string]struct{}, len(cfg.SensitiveFields))
	for _, field := range cfg.SensitiveFields {
		sensitiveFields[strings.ToLower(field)] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
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

			// Capture request body
			var reqBody []byte
			if cfg.LogRequestBody && r.Body != nil && r.ContentLength > 0 {
				reqBody, _ = io.ReadAll(io.LimitReader(r.Body, int64(cfg.MaxBodySize)+1))
				if closeErr := r.Body.Close(); closeErr != nil {
					if c, ok := aarv.FromRequest(r); ok {
						c.Logger().Warn("verboselog request body close failed", "error", closeErr)
					} else {
						slog.Warn("verboselog request body close failed", "error", closeErr, "path", path)
					}
				}
				// Restore the body for downstream handlers
				r.Body = io.NopCloser(bytes.NewReader(reqBody))
			}

			// Build request headers map
			var reqHeaders map[string]string
			if cfg.LogRequestHeaders {
				reqHeaders = make(map[string]string, len(r.Header))
				for k, v := range r.Header {
					if cfg.RedactSensitive {
						if _, sensitive := sensitiveHeaders[strings.ToLower(k)]; sensitive {
							reqHeaders[k] = "[REDACTED]"
							continue
						}
					}
					reqHeaders[k] = strings.Join(v, ", ")
				}
			}

			// Build query params map (only if enabled)
			var queryParams map[string]string
			if cfg.LogQueryParams {
				queryParams = make(map[string]string, len(r.URL.Query()))
				for k, v := range r.URL.Query() {
					if cfg.RedactSensitive {
						if _, sensitive := sensitiveFields[strings.ToLower(k)]; sensitive {
							queryParams[k] = "[REDACTED]"
							continue
						}
					}
					queryParams[k] = strings.Join(v, ", ")
				}
			}

			// Create response body capturing writer
			respWriter := &bodyCapturingWriter{
				ResponseWriter: w,
				body:           &bytes.Buffer{},
				statusCode:     http.StatusOK,
				maxBodySize:    cfg.MaxBodySize,
			}

			// Execute handler
			next.ServeHTTP(respWriter, r)

			latency := time.Since(start)

			// Build response headers map
			var respHeaders map[string]string
			if cfg.LogResponseHeaders {
				respHeaders = make(map[string]string, len(respWriter.Header()))
				for k, v := range respWriter.Header() {
					if cfg.RedactSensitive {
						if _, sensitive := sensitiveHeaders[strings.ToLower(k)]; sensitive {
							respHeaders[k] = "[REDACTED]"
							continue
						}
					}
					respHeaders[k] = strings.Join(v, ", ")
				}
			}

			// Prepare request body for logging
			reqBodyStr := ""
			if cfg.LogRequestBody && len(reqBody) > 0 {
				if len(reqBody) > cfg.MaxBodySize {
					reqBodyStr = string(reqBody[:cfg.MaxBodySize]) + "[truncated]"
				} else {
					reqBodyStr = string(reqBody)
				}
				if cfg.RedactSensitive {
					reqBodyStr = redactSensitiveFields(reqBodyStr, cfg.SensitiveFields)
				}
			}

			// Prepare response body for logging
			respBodyStr := ""
			if cfg.LogResponseBody && respWriter.body.Len() > 0 {
				if respWriter.body.Len() >= cfg.MaxBodySize {
					respBodyStr = respWriter.body.String() + "[truncated]"
				} else {
					respBodyStr = respWriter.body.String()
				}
				if cfg.RedactSensitive {
					respBodyStr = redactSensitiveFields(respBodyStr, cfg.SensitiveFields)
				}
			}

			// Build log attributes dynamically based on config
			attrs := make([]any, 0, 32)
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
		})
	}
}

// redactSensitiveFields replaces sensitive field values in JSON-like strings.
// This is a simple string-based approach, not full JSON parsing.
func redactSensitiveFields(body string, fields []string) string {
	for _, field := range fields {
		// Match patterns like "password":"value" or "password": "value"
		patterns := []string{
			`"` + field + `":"`,
			`"` + field + `": "`,
			`"` + field + `" : "`,
		}
		for _, pattern := range patterns {
			if idx := strings.Index(strings.ToLower(body), strings.ToLower(pattern)); idx >= 0 {
				// Find the closing quote
				start := idx + len(pattern)
				end := strings.Index(body[start:], `"`)
				if end > 0 {
					body = body[:start] + "[REDACTED]" + body[start+end:]
				}
			}
		}
	}
	return body
}
