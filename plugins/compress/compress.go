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

var gzipResponseWriterPool = sync.Pool{
	New: func() any { return &gzipResponseWriter{} },
}

var deflateResponseWriterPool = sync.Pool{
	New: func() any { return &deflateResponseWriter{} },
}

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
	passthrough    bool
	ctChecked      bool
	ctExcluded     bool
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

	if grw.passthrough {
		return grw.ResponseWriter.Write(b)
	}

	// Check if content type is excluded
	if grw.isExcludedFunc != nil {
		ctExcluded, known := grw.isContentTypeExcluded()
		if known && ctExcluded {
			// Skip compression for excluded types
			grw.passthrough = true
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
	}
	if !grw.compressed {
		// Response was smaller than minSize — send uncompressed
		if !grw.headerSent && !grw.passthrough {
			grw.headerSent = true
			grw.ResponseWriter.WriteHeader(grw.statusCode)
		}
		if len(grw.buf) > 0 {
			_, _ = grw.ResponseWriter.Write(grw.buf)
		}
	}
	releaseGzipResponseWriter(grw)
}

// Unwrap returns the underlying http.ResponseWriter.
func (grw *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return grw.ResponseWriter
}

func (grw *gzipResponseWriter) isContentTypeExcluded() (excluded, known bool) {
	if grw.ctChecked {
		return grw.ctExcluded, true
	}

	ct := grw.ResponseWriter.Header().Get("Content-Type")
	if ct == "" {
		return false, false
	}

	grw.ctChecked = true
	grw.ctExcluded = grw.isExcludedFunc(ct)
	return grw.ctExcluded, true
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
	passthrough    bool
	ctChecked      bool
	ctExcluded     bool
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

	if drw.passthrough {
		return drw.ResponseWriter.Write(b)
	}

	// Check if content type is excluded
	if drw.isExcludedFunc != nil {
		ctExcluded, known := drw.isContentTypeExcluded()
		if known && ctExcluded {
			drw.passthrough = true
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
	}
	if !drw.compressed {
		if !drw.headerSent && !drw.passthrough {
			drw.headerSent = true
			drw.ResponseWriter.WriteHeader(drw.statusCode)
		}
		if len(drw.buf) > 0 {
			_, _ = drw.ResponseWriter.Write(drw.buf)
		}
	}
	releaseDeflateResponseWriter(drw)
}

func (drw *deflateResponseWriter) Unwrap() http.ResponseWriter {
	return drw.ResponseWriter
}

func (drw *deflateResponseWriter) isContentTypeExcluded() (excluded, known bool) {
	if drw.ctChecked {
		return drw.ctExcluded, true
	}

	ct := drw.ResponseWriter.Header().Get("Content-Type")
	if ct == "" {
		return false, false
	}

	drw.ctChecked = true
	drw.ctExcluded = drw.isExcludedFunc(ct)
	return drw.ctExcluded, true
}

func acquireGzipResponseWriter(w http.ResponseWriter, gz gzipCompressor, pool *sync.Pool, minSize int, isExcludedFunc func(string) bool) *gzipResponseWriter {
	grw := gzipResponseWriterPool.Get().(*gzipResponseWriter)
	grw.ResponseWriter = w
	grw.gzWriter = gz
	grw.pool = pool
	grw.buf = grw.buf[:0]
	grw.minSize = minSize
	grw.statusCode = http.StatusOK
	grw.headerSent = false
	grw.compressed = false
	grw.passthrough = false
	grw.ctChecked = false
	grw.ctExcluded = false
	grw.isExcludedFunc = isExcludedFunc
	return grw
}

func releaseGzipResponseWriter(grw *gzipResponseWriter) {
	if grw == nil {
		return
	}
	grw.ResponseWriter = nil
	grw.gzWriter = nil
	grw.pool = nil
	if cap(grw.buf) > aarv.MaxPooledBufferCap {
		grw.buf = nil
	} else {
		grw.buf = grw.buf[:0]
	}
	grw.minSize = 0
	grw.statusCode = http.StatusOK
	grw.headerSent = false
	grw.compressed = false
	grw.passthrough = false
	grw.ctChecked = false
	grw.ctExcluded = false
	grw.isExcludedFunc = nil
	gzipResponseWriterPool.Put(grw)
}

func acquireDeflateResponseWriter(w http.ResponseWriter, fw deflateCompressor, pool *sync.Pool, minSize int, isExcludedFunc func(string) bool) *deflateResponseWriter {
	drw := deflateResponseWriterPool.Get().(*deflateResponseWriter)
	drw.ResponseWriter = w
	drw.deflateWriter = fw
	drw.pool = pool
	drw.buf = drw.buf[:0]
	drw.minSize = minSize
	drw.statusCode = http.StatusOK
	drw.headerSent = false
	drw.compressed = false
	drw.passthrough = false
	drw.ctChecked = false
	drw.ctExcluded = false
	drw.isExcludedFunc = isExcludedFunc
	return drw
}

func releaseDeflateResponseWriter(drw *deflateResponseWriter) {
	if drw == nil {
		return
	}
	drw.ResponseWriter = nil
	drw.deflateWriter = nil
	drw.pool = nil
	if cap(drw.buf) > aarv.MaxPooledBufferCap {
		drw.buf = nil
	} else {
		drw.buf = drw.buf[:0]
	}
	drw.minSize = 0
	drw.statusCode = http.StatusOK
	drw.headerSent = false
	drw.compressed = false
	drw.passthrough = false
	drw.ctChecked = false
	drw.ctExcluded = false
	drw.isExcludedFunc = nil
	deflateResponseWriterPool.Put(drw)
}

func selectEncoding(acceptEncoding string, preferGzip bool) string {
	var gzipAccepted bool
	var gzipSpecified bool
	var deflateAccepted bool
	var deflateSpecified bool
	var wildcardAccepted bool

	for len(acceptEncoding) > 0 {
		part, rest, _ := strings.Cut(acceptEncoding, ",")
		acceptEncoding = rest
		part = trimASCIISpace(part)
		if part == "" {
			continue
		}

		token, params, _ := strings.Cut(part, ";")
		token = trimASCIISpace(token)
		accepted := qualityAllowed(params)

		switch {
		case asciiEqualFold(token, "gzip"):
			gzipSpecified = true
			gzipAccepted = accepted
		case asciiEqualFold(token, "deflate"):
			deflateSpecified = true
			deflateAccepted = accepted
		case token == "*":
			wildcardAccepted = accepted
		}
	}

	if wildcardAccepted {
		if !gzipSpecified {
			gzipAccepted = true
		}
		if !deflateSpecified {
			deflateAccepted = true
		}
	}

	if preferGzip {
		if gzipAccepted {
			return "gzip"
		}
		if deflateAccepted {
			return "deflate"
		}
		return ""
	}

	if deflateAccepted {
		return "deflate"
	}
	if gzipAccepted {
		return "gzip"
	}
	return ""
}

func qualityAllowed(params string) bool {
	if params == "" {
		return true
	}

	for len(params) > 0 {
		param, rest, _ := strings.Cut(params, ";")
		params = rest
		param = trimASCIISpace(param)
		if param == "" {
			continue
		}
		name, value, ok := strings.Cut(param, "=")
		if !ok {
			continue
		}
		if !asciiEqualFold(trimASCIISpace(name), "q") {
			continue
		}
		return qualityValuePositive(value)
	}

	return true
}

func qualityValuePositive(value string) bool {
	sawDigit := false
	for _, ch := range trimASCIISpace(value) {
		switch {
		case ch >= '1' && ch <= '9':
			return true
		case ch == '0':
			sawDigit = true
		case ch == '.':
			continue
		default:
			return true
		}
	}
	return !sawDigit
}

func trimASCIISpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func asciiEqualFold(s, target string) bool {
	if len(s) != len(target) {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		if ch != target[i] {
			return false
		}
	}
	return true
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

	m := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			encoding := selectEncoding(r.Header.Get("Accept-Encoding"), cfg.PreferGzip)
			if encoding == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip if the response is already encoded.
			if w.Header().Get("Content-Encoding") != "" {
				next.ServeHTTP(w, r)
				return
			}

			if encoding == "gzip" {
				gz := gzipPool.Get().(gzipCompressor)
				gz.Reset(w)

				grw := acquireGzipResponseWriter(w, gz, gzipPool, cfg.MinSize, isExcluded)
				defer grw.finish()
				next.ServeHTTP(grw, r)
				return
			}

			fw := deflatePool.Get().(deflateCompressor)
			fw.Reset(w)

			drw := acquireDeflateResponseWriter(w, fw, deflatePool, cfg.MinSize, isExcluded)
			defer drw.finish()
			next.ServeHTTP(drw, r)
		})
	}

	native := func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			encoding := selectEncoding(c.Header("Accept-Encoding"), cfg.PreferGzip)
			if encoding == "" {
				return next(c)
			}

			orig := c.Response()
			if orig.Header().Get("Content-Encoding") != "" {
				return next(c)
			}

			if encoding == "gzip" {
				gz := gzipPool.Get().(gzipCompressor)
				gz.Reset(orig)
				grw := acquireGzipResponseWriter(orig, gz, gzipPool, cfg.MinSize, isExcluded)
				defer grw.finish()
				c.SetResponse(grw)
				defer c.SetResponse(orig)
				return next(c)
			}

			fw := deflatePool.Get().(deflateCompressor)
			fw.Reset(orig)
			drw := acquireDeflateResponseWriter(orig, fw, deflatePool, cfg.MinSize, isExcluded)
			defer drw.finish()
			c.SetResponse(drw)
			defer c.SetResponse(orig)
			return next(c)
		}
	}

	return aarv.RegisterNativeMiddleware(m, native)
}
