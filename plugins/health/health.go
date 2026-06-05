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

	// Info is optional additional data included in every health-check JSON
	// response (e.g. version, build commit, uptime). Each key/value pair is
	// added as a top-level field alongside "status".
	// Default: nil (no extra fields).
	Info map[string]any
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
func New(config ...Config) aarv.NativeMiddleware {
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

	// buildBody returns the response payload, merging Info when configured.
	// The computed "status" field always wins — Info cannot override it.
	buildBody := func(status string) any {
		if len(cfg.Info) == 0 {
			return statusResponse{Status: status}
		}
		body := make(map[string]any, len(cfg.Info)+1)
		for k, v := range cfg.Info {
			body[k] = v
		}
		body["status"] = status // set last so Info cannot overwrite
		return body
	}

	m := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			switch path {
			case cfg.HealthPath:
				writeJSON(w, http.StatusOK, buildBody("ok"))
				return

			case cfg.ReadyPath:
				if cfg.ReadyCheck != nil && !cfg.ReadyCheck() {
					writeJSON(w, http.StatusServiceUnavailable, buildBody("unavailable"))
					return
				}
				writeJSON(w, http.StatusOK, buildBody("ok"))
				return

			case cfg.LivePath:
				if cfg.LiveCheck != nil && !cfg.LiveCheck() {
					writeJSON(w, http.StatusServiceUnavailable, buildBody("unavailable"))
					return
				}
				writeJSON(w, http.StatusOK, buildBody("ok"))
				return
			}

			next.ServeHTTP(w, r)
		})
	}

	native := func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			switch c.Path() {
			case cfg.HealthPath:
				return c.JSON(http.StatusOK, buildBody("ok"))
			case cfg.ReadyPath:
				if cfg.ReadyCheck != nil && !cfg.ReadyCheck() {
					return c.JSON(http.StatusServiceUnavailable, buildBody("unavailable"))
				}
				return c.JSON(http.StatusOK, buildBody("ok"))
			case cfg.LivePath:
				if cfg.LiveCheck != nil && !cfg.LiveCheck() {
					return c.JSON(http.StatusServiceUnavailable, buildBody("unavailable"))
				}
				return c.JSON(http.StatusOK, buildBody("ok"))
			default:
				return next(c)
			}
		}
	}

	return aarv.RegisterNativeMiddleware(m, native)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
