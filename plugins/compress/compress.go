// Package compress provides gzip and deflate compression middleware for the aarv framework.
//
// It checks the Accept-Encoding request header for gzip/deflate support and compresses
// response bodies using pooled writers. Responses smaller than the configured minimum
// size are not compressed. Content types can be excluded from compression.
package compress

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/nilshah80/aarv"
)

type gzipCompressor interface {
	Write([]byte) (int, error)
	Close() error
	Reset(io.Writer)
}

type deflateCompressor interface {
	Write([]byte) (int, error)
	Close() error
	Reset(io.Writer)
}

var (
	newGzipWriterLevel = func(w io.Writer, level int) (gzipCompressor, error) {
		return gzip.NewWriterLevel(w, level)
	}
	newDeflateWriter = func(w io.Writer, level int) (deflateCompressor, error) {
		return flate.NewWriter(w, level)
	}
)

// Config holds configuration for the compression middleware.
type Config struct {
	// Level is the compression level for both gzip and deflate.
	// Valid values range from gzip.BestSpeed (1) to gzip.BestCompression (9).
	// Use gzip.DefaultCompression (-1) for the default level.
	// Default: gzip.DefaultCompression.
	Level int

	// MinSize is the minimum response body size in bytes required before
	// compression is applied. Responses smaller than this threshold are sent
	// uncompressed. Default: 1024.
	MinSize int

	// ExcludedTypes is a list of MIME types that should not be compressed.
	// Default: image/*, video/*, audio/*, application/pdf, application/zip.
	ExcludedTypes []string

	// PreferGzip prefers gzip over deflate when both are accepted.
	// Default: true.
	PreferGzip bool
}

// DefaultConfig returns the default compression configuration.
func DefaultConfig() Config {
	return Config{
		Level:   gzip.DefaultCompression,
		MinSize: 1024,
		ExcludedTypes: []string{
			"image/",
			"video/",
			"audio/",
			"application/pdf",
			"application/zip",
			"application/gzip",
			"application/x-gzip",
		},
		PreferGzip: true,
	}
}

// gzipResponseWriter wraps http.ResponseWriter to buffer the response and
// conditionally apply gzip compression based on the body size.
type gzipResponseWriter struct {
	http.ResponseWriter
	gzWriter       gzipCompressor
	pool           *sync.Pool
	buf            []byte
	minSize        int
	statusCode     int
	headerSent     bool
	compressed     bool
	isExcludedFunc func(string) bool
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

	// Check if content type is excluded
	if grw.isExcludedFunc != nil {
		ct := grw.ResponseWriter.Header().Get("Content-Type")
		if ct != "" && grw.isExcludedFunc(ct) {
			// Skip compression for excluded types
			grw.headerSent = true
			grw.ResponseWriter.WriteHeader(grw.statusCode)
			return grw.ResponseWriter.Write(b)
		}
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
		_ = grw.gzWriter.Close()
		grw.pool.Put(grw.gzWriter)
		return
	}

	// Response was smaller than minSize — send uncompressed
	if !grw.headerSent {
		grw.headerSent = true
		grw.ResponseWriter.WriteHeader(grw.statusCode)
	}
	if len(grw.buf) > 0 {
		_, _ = grw.ResponseWriter.Write(grw.buf)
	}
}

// Unwrap returns the underlying http.ResponseWriter.
func (grw *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return grw.ResponseWriter
}

// deflateResponseWriter wraps http.ResponseWriter for deflate compression.
type deflateResponseWriter struct {
	http.ResponseWriter
	deflateWriter  deflateCompressor
	pool           *sync.Pool
	buf            []byte
	minSize        int
	statusCode     int
	headerSent     bool
	compressed     bool
	isExcludedFunc func(string) bool
}

func (drw *deflateResponseWriter) WriteHeader(code int) {
	if !drw.headerSent {
		drw.statusCode = code
	}
}

func (drw *deflateResponseWriter) Write(b []byte) (int, error) {
	if drw.compressed {
		return drw.deflateWriter.Write(b)
	}

	// Check if content type is excluded
	if drw.isExcludedFunc != nil {
		ct := drw.ResponseWriter.Header().Get("Content-Type")
		if ct != "" && drw.isExcludedFunc(ct) {
			drw.headerSent = true
			drw.ResponseWriter.WriteHeader(drw.statusCode)
			return drw.ResponseWriter.Write(b)
		}
	}

	drw.buf = append(drw.buf, b...)

	if len(drw.buf) >= drw.minSize {
		drw.compressed = true
		drw.ResponseWriter.Header().Set("Content-Encoding", "deflate")
		drw.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
		drw.ResponseWriter.Header().Del("Content-Length")
		drw.headerSent = true
		drw.ResponseWriter.WriteHeader(drw.statusCode)

		n, err := drw.deflateWriter.Write(drw.buf)
		drw.buf = nil
		if err != nil {
			return 0, err
		}
		_ = n
		return len(b), nil
	}

	return len(b), nil
}

func (drw *deflateResponseWriter) finish() {
	if drw.compressed {
		_ = drw.deflateWriter.Close()
		drw.pool.Put(drw.deflateWriter)
		return
	}

	if !drw.headerSent {
		drw.headerSent = true
		drw.ResponseWriter.WriteHeader(drw.statusCode)
	}
	if len(drw.buf) > 0 {
		_, _ = drw.ResponseWriter.Write(drw.buf)
	}
}

func (drw *deflateResponseWriter) Unwrap() http.ResponseWriter {
	return drw.ResponseWriter
}

// New creates a compression middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
// Supports both gzip and deflate compression.
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

	gzipPool := &sync.Pool{
		New: func() any {
			gz, _ := newGzipWriterLevel(io.Discard, level)
			return gz
		},
	}

	deflatePool := &sync.Pool{
		New: func() any {
			fw, _ := newDeflateWriter(io.Discard, level)
			return fw
		},
	}

	// Build excluded types set for fast lookup
	excludedTypes := make(map[string]struct{}, len(cfg.ExcludedTypes))
	excludedPrefixes := make([]string, 0)
	for _, t := range cfg.ExcludedTypes {
		if strings.HasSuffix(t, "/") {
			excludedPrefixes = append(excludedPrefixes, t)
		} else {
			excludedTypes[t] = struct{}{}
		}
	}

	isExcluded := func(contentType string) bool {
		// Extract MIME type without parameters
		if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
			contentType = strings.TrimSpace(contentType[:idx])
		}
		if _, ok := excludedTypes[contentType]; ok {
			return true
		}
		for _, prefix := range excludedPrefixes {
			if strings.HasPrefix(contentType, prefix) {
				return true
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			acceptEncoding := r.Header.Get("Accept-Encoding")

			// Determine which encoding to use
			acceptsGzip := strings.Contains(acceptEncoding, "gzip")
			acceptsDeflate := strings.Contains(acceptEncoding, "deflate")

			if !acceptsGzip && !acceptsDeflate {
				next.ServeHTTP(w, r)
				return
			}

			// Skip if the response is already encoded
			if w.Header().Get("Content-Encoding") != "" {
				next.ServeHTTP(w, r)
				return
			}

			// Choose encoding: prefer gzip if configured and both are accepted
			useGzip := acceptsGzip && (cfg.PreferGzip || !acceptsDeflate)

			if useGzip {
				gz := gzipPool.Get().(gzipCompressor)
				gz.Reset(w)

				grw := &gzipResponseWriter{
					ResponseWriter:   w,
					gzWriter:         gz,
					pool:             gzipPool,
					minSize:          cfg.MinSize,
					statusCode:       http.StatusOK,
					isExcludedFunc:   isExcluded,
				}

				defer grw.finish()
				next.ServeHTTP(grw, r)
			} else {
				fw := deflatePool.Get().(deflateCompressor)
				fw.Reset(w)

				drw := &deflateResponseWriter{
					ResponseWriter:   w,
					deflateWriter:    fw,
					pool:             deflatePool,
					minSize:          cfg.MinSize,
					statusCode:       http.StatusOK,
					isExcludedFunc:   isExcluded,
				}

				defer drw.finish()
				next.ServeHTTP(drw, r)
			}
		})
	}
}
