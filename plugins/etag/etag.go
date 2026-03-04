// Package etag provides ETag middleware for the aarv framework.
//
// It computes a CRC32 hash of the response body and sets it as the ETag header.
// If the client sends an If-None-Match header that matches the computed ETag,
// a 304 Not Modified response is returned instead of the full body.
package etag

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"net/http"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the ETag middleware.
type Config struct {
	// Weak generates a weak ETag (prefixed with W/) instead of a strong one.
	// Weak ETags indicate semantic equivalence rather than byte-for-byte identity.
	// Default: false.
	Weak bool
}

// DefaultConfig returns the default ETag configuration.
func DefaultConfig() Config {
	return Config{
		Weak: false,
	}
}

// captureWriter buffers the response body so the ETag can be computed before
// sending the response to the client.
type captureWriter struct {
	http.ResponseWriter
	body       bytes.Buffer
	statusCode int
	headerSent bool
}

func (cw *captureWriter) WriteHeader(code int) {
	if !cw.headerSent {
		cw.statusCode = code
	}
}

func (cw *captureWriter) Write(b []byte) (int, error) {
	return cw.body.Write(b)
}

// Unwrap returns the underlying http.ResponseWriter.
func (cw *captureWriter) Unwrap() http.ResponseWriter {
	return cw.ResponseWriter
}

// New creates an ETag middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only compute ETags for GET and HEAD requests
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}

			cw := &captureWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(cw, r)

			body := cw.body.Bytes()

			// Only set ETag for successful responses with a body
			if cw.statusCode < 200 || cw.statusCode >= 300 || len(body) == 0 {
				w.WriteHeader(cw.statusCode)
				if len(body) > 0 {
					_, _ = w.Write(body)
				}
				return
			}

			// Compute CRC32 hash of the response body
			checksum := crc32.ChecksumIEEE(body)
			var etag string
			if cfg.Weak {
				etag = fmt.Sprintf(`W/"%08x"`, checksum)
			} else {
				etag = fmt.Sprintf(`"%08x"`, checksum)
			}

			// Set the ETag header
			w.Header().Set("ETag", etag)

			// Check If-None-Match header
			ifNoneMatch := r.Header.Get("If-None-Match")
			if ifNoneMatch != "" && matchETag(ifNoneMatch, etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}

			// Send the full response
			w.WriteHeader(cw.statusCode)
			_, _ = w.Write(body)
		})
	}
}

// matchETag checks if the given If-None-Match header value matches the ETag.
// It handles the "*" wildcard, comma-separated lists, and weak/strong comparison.
func matchETag(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "*" {
		return true
	}

	// Strip W/ prefix for weak comparison (If-None-Match uses weak comparison)
	etagVal := stripWeakPrefix(etag)

	// Parse comma-separated list of ETags
	for ifNoneMatch != "" {
		var candidate string
		if i := indexOf(ifNoneMatch, ','); i >= 0 {
			candidate = trimSpaces(ifNoneMatch[:i])
			ifNoneMatch = ifNoneMatch[i+1:]
		} else {
			candidate = trimSpaces(ifNoneMatch)
			ifNoneMatch = ""
		}

		if candidate == "" {
			continue
		}

		candidateVal := stripWeakPrefix(candidate)
		if candidateVal == etagVal {
			return true
		}
	}

	return false
}

// stripWeakPrefix removes the W/ prefix from an ETag value for weak comparison.
func stripWeakPrefix(s string) string {
	if len(s) >= 2 && s[0] == 'W' && s[1] == '/' {
		return s[2:]
	}
	return s
}

// indexOf returns the index of the first occurrence of sep in s, or -1 if not found.
func indexOf(s string, sep byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return i
		}
	}
	return -1
}

// trimSpaces trims leading and trailing whitespace from a string.
func trimSpaces(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
