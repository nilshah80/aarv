// Package compress provides gzip compression middleware for the aarv framework.
//
// It checks the Accept-Encoding request header for gzip support and compresses
// response bodies using a pooled gzip.Writer. Responses smaller than the
// configured minimum size are not compressed.
package compress

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the compression middleware.
type Config struct {
	// Level is the gzip compression level.
	// Valid values range from gzip.BestSpeed (1) to gzip.BestCompression (9).
	// Use gzip.DefaultCompression (-1) for the default level.
	// Default: gzip.DefaultCompression.
	Level int

	// MinSize is the minimum response body size in bytes required before
	// compression is applied. Responses smaller than this threshold are sent
	// uncompressed. Default: 1024.
	MinSize int
}

// DefaultConfig returns the default compression configuration.
func DefaultConfig() Config {
	return Config{
		Level:   gzip.DefaultCompression,
		MinSize: 1024,
	}
}

// gzipResponseWriter wraps http.ResponseWriter to buffer the response and
// conditionally apply gzip compression based on the body size.
type gzipResponseWriter struct {
	http.ResponseWriter
	gzWriter   *gzip.Writer
	pool       *sync.Pool
	buf        []byte
	minSize    int
	statusCode int
	headerSent bool
	compressed bool
}

func (grw *gzipResponseWriter) WriteHeader(code int) {
	if !grw.headerSent {
		grw.statusCode = code
	}
}

func (grw *gzipResponseWriter) Write(b []byte) (int, error) {
	// If we already decided to compress, write through the gzip writer
	if grw.compressed {
		return grw.gzWriter.Write(b)
	}

	// Buffer data until we have enough to decide
	grw.buf = append(grw.buf, b...)

	if len(grw.buf) >= grw.minSize {
		// Threshold reached: enable compression
		grw.compressed = true
		grw.ResponseWriter.Header().Set("Content-Encoding", "gzip")
		grw.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
		grw.ResponseWriter.Header().Del("Content-Length")
		grw.headerSent = true
		grw.ResponseWriter.WriteHeader(grw.statusCode)

		// Write buffered data through gzip
		n, err := grw.gzWriter.Write(grw.buf)
		grw.buf = nil
		// Return the length of b as written, since all of b was accepted
		if err != nil {
			return 0, err
		}
		_ = n
		return len(b), nil
	}

	return len(b), nil
}

// finish flushes any remaining data. Called after the downstream handler returns.
func (grw *gzipResponseWriter) finish() {
	if grw.compressed {
		// Flush and close the gzip writer
		grw.gzWriter.Close()
		grw.pool.Put(grw.gzWriter)
		return
	}

	// Response was smaller than minSize — send uncompressed
	if !grw.headerSent {
		grw.headerSent = true
		grw.ResponseWriter.WriteHeader(grw.statusCode)
	}
	if len(grw.buf) > 0 {
		grw.ResponseWriter.Write(grw.buf)
	}
}

// Unwrap returns the underlying http.ResponseWriter.
func (grw *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return grw.ResponseWriter
}

// New creates a gzip compression middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	if cfg.MinSize <= 0 {
		cfg.MinSize = 1024
	}

	level := cfg.Level
	if level < gzip.HuffmanOnly || level > gzip.BestCompression {
		level = gzip.DefaultCompression
	}

	pool := &sync.Pool{
		New: func() any {
			gz, _ := gzip.NewWriterLevel(io.Discard, level)
			return gz
		},
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if the client accepts gzip encoding
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}

			// Skip if the response is already encoded
			if w.Header().Get("Content-Encoding") != "" {
				next.ServeHTTP(w, r)
				return
			}

			gz := pool.Get().(*gzip.Writer)
			gz.Reset(w)

			grw := &gzipResponseWriter{
				ResponseWriter: w,
				gzWriter:       gz,
				pool:           pool,
				minSize:        cfg.MinSize,
				statusCode:     http.StatusOK,
			}

			defer grw.finish()

			next.ServeHTTP(grw, r)
		})
	}
}
