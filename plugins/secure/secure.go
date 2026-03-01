// Package secure provides security headers middleware for the aarv framework.
//
// It sets common HTTP security headers on every response to help protect against
// XSS, clickjacking, MIME sniffing, and other web vulnerabilities.
package secure

import (
	"fmt"
	"net/http"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the security headers middleware.
type Config struct {
	// XSSProtection sets the X-XSS-Protection header.
	// Default: "1; mode=block".
	XSSProtection string

	// ContentTypeNosniff sets the X-Content-Type-Options header.
	// Default: "nosniff".
	ContentTypeNosniff string

	// XFrameOptions sets the X-Frame-Options header.
	// Default: "SAMEORIGIN".
	XFrameOptions string

	// HSTSMaxAge sets the max-age directive of the Strict-Transport-Security header
	// in seconds. Set to 0 to disable HSTS. Default: 0.
	HSTSMaxAge int

	// HSTSIncludeSubdomains adds the includeSubDomains directive to the HSTS header.
	// Only effective when HSTSMaxAge > 0.
	HSTSIncludeSubdomains bool

	// ContentSecurityPolicy sets the Content-Security-Policy header.
	// Default: "" (not set).
	ContentSecurityPolicy string

	// ReferrerPolicy sets the Referrer-Policy header.
	// Default: "" (not set).
	ReferrerPolicy string
}

// DefaultConfig returns the default security headers configuration.
func DefaultConfig() Config {
	return Config{
		XSSProtection:      "1; mode=block",
		ContentTypeNosniff: "nosniff",
		XFrameOptions:      "SAMEORIGIN",
	}
}

// New creates a security headers middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	// Pre-compute HSTS header value
	var hstsValue string
	if cfg.HSTSMaxAge > 0 {
		hstsValue = fmt.Sprintf("max-age=%d", cfg.HSTSMaxAge)
		if cfg.HSTSIncludeSubdomains {
			hstsValue += "; includeSubDomains"
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()

			if cfg.XSSProtection != "" {
				h.Set("X-XSS-Protection", cfg.XSSProtection)
			}

			if cfg.ContentTypeNosniff != "" {
				h.Set("X-Content-Type-Options", cfg.ContentTypeNosniff)
			}

			if cfg.XFrameOptions != "" {
				h.Set("X-Frame-Options", cfg.XFrameOptions)
			}

			if hstsValue != "" {
				h.Set("Strict-Transport-Security", hstsValue)
			}

			if cfg.ContentSecurityPolicy != "" {
				h.Set("Content-Security-Policy", cfg.ContentSecurityPolicy)
			}

			if cfg.ReferrerPolicy != "" {
				h.Set("Referrer-Policy", cfg.ReferrerPolicy)
			}

			next.ServeHTTP(w, r)
		})
	}
}
