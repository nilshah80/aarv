// Package cors provides Cross-Origin Resource Sharing (CORS) middleware for the
// aarv framework.
//
// It handles preflight OPTIONS requests and sets the appropriate CORS headers on
// responses. It supports configurable origins, methods, headers, and credentials.
package cors

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the CORS middleware.
type Config struct {
	// AllowOrigins is a list of origins that are allowed to make cross-origin
	// requests. Use ["*"] to allow all origins. Default: ["*"].
	AllowOrigins []string

	// AllowMethods is a list of HTTP methods allowed for cross-origin requests.
	// Default: ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"].
	AllowMethods []string

	// AllowHeaders is a list of request headers allowed in cross-origin requests.
	// Default: ["Origin", "Content-Type", "Accept", "Authorization"].
	AllowHeaders []string

	// ExposeHeaders is a list of response headers that browsers are allowed to access.
	ExposeHeaders []string

	// AllowCredentials indicates whether the response to the request can include
	// credentials (cookies, HTTP authentication, or client-side TLS certificates).
	// Cannot be true when AllowOrigins contains "*".
	AllowCredentials bool

	// MaxAge indicates how long (in seconds) the results of a preflight request
	// can be cached. Default: 0 (no caching).
	MaxAge int

	// AllowOriginFunc is an optional function to dynamically determine whether an
	// origin is allowed. If set, it takes precedence over AllowOrigins for
	// non-wildcard checks.
	AllowOriginFunc func(origin string) bool
}

// DefaultConfig returns the default CORS configuration.
func DefaultConfig() Config {
	return Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Type", "Accept", "Authorization"},
	}
}

// New creates a CORS middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	if len(cfg.AllowMethods) == 0 {
		cfg.AllowMethods = DefaultConfig().AllowMethods
	}
	if len(cfg.AllowHeaders) == 0 {
		cfg.AllowHeaders = DefaultConfig().AllowHeaders
	}

	// Reject wildcard origin combined with credentials
	allowAll := false
	for _, o := range cfg.AllowOrigins {
		if o == "*" {
			allowAll = true
			break
		}
	}
	if allowAll && cfg.AllowCredentials {
		panic("cors: wildcard origin '*' cannot be used with AllowCredentials")
	}

	// Pre-join header values for performance
	allowMethodsStr := strings.Join(cfg.AllowMethods, ", ")
	allowHeadersStr := strings.Join(cfg.AllowHeaders, ", ")
	exposeHeadersStr := strings.Join(cfg.ExposeHeaders, ", ")
	maxAgeStr := ""
	if cfg.MaxAge > 0 {
		maxAgeStr = strconv.Itoa(cfg.MaxAge)
	}

	// Build a set of allowed origins for fast lookup
	originsMap := make(map[string]struct{}, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		originsMap[o] = struct{}{}
	}

	isOriginAllowed := func(origin string) bool {
		if allowAll {
			return true
		}
		if cfg.AllowOriginFunc != nil {
			return cfg.AllowOriginFunc(origin)
		}
		_, ok := originsMap[origin]
		return ok
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// If no Origin header, this is not a CORS request
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if !isOriginAllowed(origin) {
				// Origin not allowed; proceed without CORS headers
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()

			// Set the allowed origin. When credentials are allowed, we must
			// echo the specific origin rather than using "*".
			if allowAll && !cfg.AllowCredentials {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}

			if cfg.AllowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}

			if exposeHeadersStr != "" {
				h.Set("Access-Control-Expose-Headers", exposeHeadersStr)
			}

			// Handle preflight request
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", allowMethodsStr)
				h.Set("Access-Control-Allow-Headers", allowHeadersStr)
				if maxAgeStr != "" {
					h.Set("Access-Control-Max-Age", maxAgeStr)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
