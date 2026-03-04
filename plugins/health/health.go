// Package health provides health check endpoint middleware for the aarv framework.
//
// It registers health, readiness, and liveness endpoints that return JSON status
// responses. Optionally, custom check functions can be provided to control the
// ready and live status.
package health

import (
	"encoding/json"
	"net/http"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the health check middleware.
type Config struct {
	// HealthPath is the path for the general health endpoint.
	// Default: "/health".
	HealthPath string

	// ReadyPath is the path for the readiness endpoint.
	// Default: "/ready".
	ReadyPath string

	// LivePath is the path for the liveness endpoint.
	// Default: "/live".
	LivePath string

	// ReadyCheck is an optional function that determines whether the service
	// is ready to accept traffic. Return true for ready, false for not ready.
	// If nil, the readiness endpoint always returns "ok".
	ReadyCheck func() bool

	// LiveCheck is an optional function that determines whether the service
	// is alive (healthy). Return true for alive, false for unhealthy.
	// If nil, the liveness endpoint always returns "ok".
	LiveCheck func() bool
}

// DefaultConfig returns the default health check configuration.
func DefaultConfig() Config {
	return Config{
		HealthPath: "/health",
		ReadyPath:  "/ready",
		LivePath:   "/live",
	}
}

// statusResponse is the JSON response body for health check endpoints.
type statusResponse struct {
	Status string `json:"status"`
}

// New creates a health check middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
//
// The middleware intercepts requests to the configured health, ready, and live
// paths and returns the appropriate status. All other requests are passed through
// to the next handler.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	if cfg.HealthPath == "" {
		cfg.HealthPath = "/health"
	}
	if cfg.ReadyPath == "" {
		cfg.ReadyPath = "/ready"
	}
	if cfg.LivePath == "" {
		cfg.LivePath = "/live"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			switch path {
			case cfg.HealthPath:
				writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
				return

			case cfg.ReadyPath:
				if cfg.ReadyCheck != nil && !cfg.ReadyCheck() {
					writeJSON(w, http.StatusServiceUnavailable, statusResponse{Status: "unavailable"})
					return
				}
				writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
				return

			case cfg.LivePath:
				if cfg.LiveCheck != nil && !cfg.LiveCheck() {
					writeJSON(w, http.StatusServiceUnavailable, statusResponse{Status: "unavailable"})
					return
				}
				writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
