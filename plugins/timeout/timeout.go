// Package timeout provides request timeout middleware for the aarv framework.
//
// Two middleware variants are provided:
//
//   - [New] creates an enforced timeout that runs the handler in a separate
//     goroutine, blocks late writes, and returns 504 Gateway Timeout when
//     the deadline is exceeded. This is the heavy, strict option.
//
//   - [Context] creates a lightweight deadline-propagation middleware that
//     sets a timeout on the request context and lets context-aware handlers
//     return early. No goroutine, no response interception. This variant
//     supports the native fast chain.
package timeout

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the timeout middleware.
type Config struct {
	// Timeout is the maximum duration allowed for the handler to complete.
	// Default: 30 seconds.
	Timeout time.Duration
}

// DefaultConfig returns the default timeout configuration.
func DefaultConfig() Config {
	return Config{
		Timeout: 30 * time.Second,
	}
}

// timeoutWriter is a response writer that guards against writes after timeout.
type timeoutWriter struct {
	http.ResponseWriter
	mu         sync.Mutex
	timedOut   bool
	written    bool
	statusCode int
}

var timeoutWriterPool = sync.Pool{
	New: func() any { return &timeoutWriter{} },
}

func acquireTimeoutWriter(w http.ResponseWriter) *timeoutWriter {
	tw := timeoutWriterPool.Get().(*timeoutWriter)
	tw.ResponseWriter = w
	tw.timedOut = false
	tw.written = false
	tw.statusCode = http.StatusOK
	return tw
}

func releaseTimeoutWriter(tw *timeoutWriter) {
	if tw == nil {
		return
	}
	tw.ResponseWriter = nil
	tw.timedOut = false
	tw.written = false
	tw.statusCode = 0
	timeoutWriterPool.Put(tw)
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut || tw.written {
		return
	}
	tw.statusCode = code
	tw.written = true
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return 0, context.DeadlineExceeded
	}
	if !tw.written {
		tw.written = true
		tw.ResponseWriter.WriteHeader(tw.statusCode)
	}
	return tw.ResponseWriter.Write(b)
}

// Unwrap returns the underlying http.ResponseWriter.
func (tw *timeoutWriter) Unwrap() http.ResponseWriter {
	return tw.ResponseWriter
}

// New creates a request timeout middleware with the given duration.
// If d is <= 0, the default of 30 seconds is used.
func New(d time.Duration) aarv.Middleware {
	if d <= 0 {
		d = DefaultConfig().Timeout
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			if c, ok := aarv.FromRequest(r); ok {
				c.SetContext(ctx)
				r = c.RawRequest()
			} else {
				r = r.WithContext(ctx)
			}

			tw := acquireTimeoutWriter(w)

			done := make(chan struct{})
			panicCh := make(chan any, 1)
			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicCh <- p
					}
					releaseTimeoutWriter(tw)
					close(done)
				}()
				next.ServeHTTP(tw, r)
			}()

			select {
			case <-done:
				// Handler completed (or panicked) within timeout
				select {
				case p := <-panicCh:
					// Re-panic on the original goroutine so Recovery middleware can catch it
					panic(p)
				default:
				}
				return
			case <-ctx.Done():
				// Timeout exceeded
				tw.mu.Lock()
				tw.timedOut = true
				written := tw.written
				tw.mu.Unlock()

				if !written {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusGatewayTimeout)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error":   "gateway_timeout",
						"message": "Request timed out",
					})
				}
			}
		})
	}
}

// Context creates a lightweight deadline-propagation middleware.
// It sets a timeout on the request context via context.WithTimeout but does
// not run the handler in a separate goroutine and does not intercept the
// response. Context-aware code (database drivers, HTTP clients, etc.) will
// observe the deadline and return early. Handlers that ignore the context
// will run to completion regardless of the deadline.
//
// This variant supports the native fast chain and is significantly cheaper
// than [New].
//
// If d is <= 0, the default of 30 seconds is used.
func Context(d time.Duration) aarv.Middleware {
	if d <= 0 {
		d = DefaultConfig().Timeout
	}

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			if c, ok := aarv.FromRequest(r); ok {
				c.SetContext(ctx)
				r = c.RawRequest()
			} else {
				r = r.WithContext(ctx)
			}

			next.ServeHTTP(w, r)
		})
	})

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			ctx, cancel := context.WithTimeout(c.Context(), d)
			defer cancel()
			c.SetContext(ctx)
			return next(c)
		}
	})

	return aarv.RegisterNativeMiddleware(m, native)
}
