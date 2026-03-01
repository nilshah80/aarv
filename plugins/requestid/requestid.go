// Package requestid provides request ID middleware for the aarv framework.
//
// It generates or propagates a unique request identifier for each HTTP request.
// The ID is read from an incoming header (default X-Request-ID) or generated
// using crypto/rand, then set on the response header and stored in the request
// context for downstream handlers.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/nilshah80/aarv"
)

// contextKey is the type used for storing the request ID in context.Context.
type contextKey struct{}

// Config holds configuration for the request ID middleware.
type Config struct {
	// Header is the HTTP header name to read/write the request ID.
	// Default: "X-Request-ID".
	Header string

	// Generator is a function that returns a new unique request ID.
	// Default: crypto/rand hex-encoded 16 bytes.
	Generator func() string
}

// DefaultConfig returns the default request ID configuration.
func DefaultConfig() Config {
	return Config{
		Header:    "X-Request-ID",
		Generator: defaultGenerator,
	}
}

// defaultGenerator generates a random 16-byte hex-encoded string.
func defaultGenerator() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// FromContext extracts the request ID from the given context.
// Returns an empty string if no request ID is present.
func FromContext(ctx context.Context) string {
	if id, ok := ctx.Value(contextKey{}).(string); ok {
		return id
	}
	return ""
}

// New creates a request ID middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	if cfg.Header == "" {
		cfg.Header = "X-Request-ID"
	}
	if cfg.Generator == nil {
		cfg.Generator = defaultGenerator
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read existing request ID from header or generate a new one
			id := r.Header.Get(cfg.Header)
			if id == "" {
				id = cfg.Generator()
			}

			// Set the request ID on the response header
			w.Header().Set(cfg.Header, id)

			// Store the request ID in the request context
			ctx := context.WithValue(r.Context(), contextKey{}, id)
			r = r.WithContext(ctx)

			// Also store in aarv Context if available (so c.RequestID() works)
			if c, ok := aarv.FromRequest(r); ok {
				c.Set("requestId", id)
			}

			next.ServeHTTP(w, r)
		})
	}
}
