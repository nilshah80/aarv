// Package recover provides panic recovery middleware for the aarv framework.
//
// It catches panics in downstream handlers, logs the stack trace, and returns
// a 500 JSON error response instead of crashing the server.
package recover

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the recovery middleware.
type Config struct {
	// StackSize is the maximum size of the stack trace buffer in bytes.
	// Default: 4096.
	StackSize int

	// DisableStackAll disables capturing the stack of all goroutines.
	// When false (default), runtime.Stack is called with all=true.
	DisableStackAll bool

	// DisablePrintStack disables logging the stack trace.
	// When true, the panic value is still logged but the stack trace is omitted.
	DisablePrintStack bool
}

// DefaultConfig returns the default recovery configuration.
func DefaultConfig() Config {
	return Config{
		StackSize:         4096,
		DisableStackAll:   false,
		DisablePrintStack: false,
	}
}

// New creates a panic recovery middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
func New(config ...Config) aarv.Middleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	if cfg.StackSize <= 0 {
		cfg.StackSize = 4096
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// Capture stack trace
					stack := make([]byte, cfg.StackSize)
					length := runtime.Stack(stack, !cfg.DisableStackAll)
					stack = stack[:length]

					err := fmt.Sprintf("%v", rec)

					if !cfg.DisablePrintStack {
						slog.Error("panic recovered",
							"error", err,
							"method", r.Method,
							"path", r.URL.Path,
							"stack", string(stack),
						)
					} else {
						slog.Error("panic recovered",
							"error", err,
							"method", r.Method,
							"path", r.URL.Path,
						)
					}

					// Write 500 JSON error response
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error":   "internal_error",
						"message": "Internal server error",
					})
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
