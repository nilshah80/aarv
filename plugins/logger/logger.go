// Package logger provides request logging middleware for the aarv framework.
//
// It logs each HTTP request's method, path, status code, latency, and client IP
// using the slog structured logging package. Paths can be skipped via configuration.
package logger

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the logger middleware.
type Config struct {
	// SkipPaths is a list of URL paths to exclude from logging.
	// Exact match is used.
	SkipPaths []string

	// Level is the slog level used for request logging.
	// Default: slog.LevelInfo.
	Level slog.Level
}

// DefaultConfig returns the default logger configuration.
func DefaultConfig() Config {
	return Config{
		Level: slog.LevelInfo,
	}
}

// responseWriter wraps http.ResponseWriter to capture the status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
	written      bool
}

var responseWriterPool = sync.Pool{
	New: func() any {
		return &responseWriter{}
	},
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	rw := responseWriterPool.Get().(*responseWriter)
	rw.ResponseWriter = w
	rw.statusCode = http.StatusOK
	rw.bytesWritten = 0
	rw.written = false
	return rw
}

func releaseResponseWriter(rw *responseWriter) {
	if rw == nil {
		return
	}
	rw.ResponseWriter = nil
	rw.statusCode = http.StatusOK
	rw.bytesWritten = 0
	rw.written = false
	responseWriterPool.Put(rw)
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.written = true
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// Unwrap returns the underlying http.ResponseWriter for interface checks.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// clientIP extracts the client IP from the request, respecting proxy headers.
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
	addr := r.RemoteAddr
	if addr == "" {
		return ""
	}
	if i := strings.LastIndexByte(addr, ':'); i > 0 && strings.IndexByte(addr, ']') == -1 {
		return addr[:i]
	}
	return addr
}

// New creates a request logging middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	skipPaths := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skipPaths[p] = struct{}{}
	}
	hasSkipPaths := len(skipPaths) > 0
	baseLogger := slog.Default()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Skip logging for configured paths
			if hasSkipPaths {
				if _, ok := skipPaths[path]; ok {
					next.ServeHTTP(w, r)
					return
				}
			}

			start := time.Now()
			rw := newResponseWriter(w)
			defer releaseResponseWriter(rw)

			next.ServeHTTP(rw, r)

			latency := time.Since(start)
			userAgent := r.Header.Get("User-Agent")

			// Get request ID if available
			requestID := ""
			if c, ok := aarv.FromRequest(r); ok {
				requestID = c.RequestID()
			}

			// Log request completion with all fields.
			baseLogger.LogAttrs(r.Context(), cfg.Level, "request",
				slog.String("method", r.Method),
				slog.String("path", path),
				slog.Int("status", rw.statusCode),
				slog.Duration("latency", latency),
				slog.String("client_ip", clientIP(r)),
				slog.String("user_agent", userAgent),
				slog.Int64("bytes_out", rw.bytesWritten),
				slog.String("request_id", requestID),
			)
		})
	}
}
