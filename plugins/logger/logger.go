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

// responseWriterPool keeps the per-request status/bytes recorders off the
// allocator on the hot path. Each pooled recorder is the public
// aarv.StatusRecorder; Reset re-binds it to the next request's writer.
var responseWriterPool = sync.Pool{
	New: func() any {
		return aarv.NewStatusRecorder(nil)
	},
}

func acquireRecorder(w http.ResponseWriter) *aarv.StatusRecorder {
	rw := responseWriterPool.Get().(*aarv.StatusRecorder)
	rw.Reset(w)
	return rw
}

func releaseRecorder(rw *aarv.StatusRecorder) {
	if rw == nil {
		return
	}
	rw.Reset(nil)
	responseWriterPool.Put(rw)
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
	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			path := c.Path()
			if hasSkipPaths {
				if _, ok := skipPaths[path]; ok {
					return next(c)
				}
			}

			start := time.Now()
			rw := acquireRecorder(c.Response())
			defer releaseRecorder(rw)

			orig := c.Response()
			c.SetResponse(rw)
			defer c.SetResponse(orig)
			err := next(c)

			latency := time.Since(start)
			baseLogger.LogAttrs(c.Context(), cfg.Level, "request",
				slog.String("method", c.Method()),
				slog.String("path", path),
				slog.Int("status", rw.Status()),
				slog.Duration("latency", latency),
				slog.String("client_ip", c.RealIP()),
				slog.String("user_agent", c.Header("User-Agent")),
				slog.Int64("bytes_out", rw.BytesWritten()),
				slog.String("request_id", c.RequestID()),
			)
			return err
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
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
			rw := acquireRecorder(w)
			defer releaseRecorder(rw)

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
				slog.Int("status", rw.Status()),
				slog.Duration("latency", latency),
				slog.String("client_ip", clientIP(r)),
				slog.String("user_agent", userAgent),
				slog.Int64("bytes_out", rw.BytesWritten()),
				slog.String("request_id", requestID),
			)
		})
	})
	return aarv.RegisterNativeMiddleware(m, native)
}
