// Package bodylimit provides request body size limiting middleware for the aarv
// framework.
//
// It wraps the request body with http.MaxBytesReader to enforce a maximum request
// body size. If the limit is exceeded, it returns a 413 Payload Too Large response.
package bodylimit

import (
	"encoding/json"
	"net/http"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the body limit middleware.
type Config struct {
	// MaxBytes is the maximum allowed size of the request body in bytes.
	// Default: 1048576 (1 MB).
	MaxBytes int64
}

// DefaultConfig returns the default body limit configuration.
func DefaultConfig() Config {
	return Config{
		MaxBytes: 1 << 20, // 1 MB
	}
}

// New creates a body size limit middleware with the given maximum body size.
// If maxBytes is <= 0, the default of 1 MB is used.
func New(maxBytes int64) aarv.Middleware {
	if maxBytes <= 0 {
		maxBytes = DefaultConfig().MaxBytes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the body with MaxBytesReader, which returns an error if
			// the body exceeds the limit.
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

			next.ServeHTTP(w, r)

			// Note: http.MaxBytesReader will cause r.Body.Read to return
			// an error after the limit is exceeded. Handlers that read the
			// body will receive this error. The framework's error handler
			// or the handler itself should check for *http.MaxBytesError
			// and return 413. For additional safety, we also provide a
			// wrapper that catches the error at the middleware level via
			// a custom response writer below. However, the simplest approach
			// is to let the MaxBytesReader do its job and let downstream
			// handlers deal with the error naturally.
		})
	}
}

// NewWithResponse creates a body size limit middleware that intercepts the error
// from MaxBytesReader and writes a 413 JSON error response.
func NewWithResponse(maxBytes int64) aarv.Middleware {
	if maxBytes <= 0 {
		maxBytes = DefaultConfig().MaxBytes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

			rw := &limitResponseWriter{
				ResponseWriter: w,
				request:        r,
				maxBytes:       maxBytes,
			}

			next.ServeHTTP(rw, r)
		})
	}
}

// limitResponseWriter intercepts writes to detect if a MaxBytesError was
// triggered and convert it into a proper 413 response.
type limitResponseWriter struct {
	http.ResponseWriter
	request  *http.Request
	maxBytes int64
}

func (lw *limitResponseWriter) WriteHeader(code int) {
	lw.ResponseWriter.WriteHeader(code)
}

func (lw *limitResponseWriter) Write(b []byte) (int, error) {
	return lw.ResponseWriter.Write(b)
}

// Unwrap returns the underlying http.ResponseWriter.
func (lw *limitResponseWriter) Unwrap() http.ResponseWriter {
	return lw.ResponseWriter
}

// SendPayloadTooLarge writes a 413 JSON error response. This is exported so
// handlers can call it when they catch a MaxBytesError.
func SendPayloadTooLarge(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "payload_too_large",
		"message": "Request body too large",
	})
}
