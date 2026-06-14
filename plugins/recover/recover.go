// Package recover provides panic recovery middleware for the aarv framework.
//
// It catches panics in downstream handlers, logs the stack trace, and returns
// a 500 JSON error response instead of crashing the server.
package recover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"

	"github.com/nilshah80/aarv"
)

// panicGuardWriter buffers the custom handler's writes so that if the handler
// panics mid-response, nothing has been committed to the real ResponseWriter.
// Call commit() to flush buffered headers, status, and body to the real writer.
type panicGuardWriter struct {
	real        http.ResponseWriter
	header      http.Header
	buf         bytes.Buffer
	statusCode  int
	committed   bool
	wroteHeader bool
}

func (g *panicGuardWriter) Header() http.Header {
	if g.header == nil {
		g.header = make(http.Header)
	}
	return g.header
}

func (g *panicGuardWriter) WriteHeader(code int) {
	if g.committed || g.wroteHeader {
		return
	}
	g.statusCode = code
	g.wroteHeader = true
}

func (g *panicGuardWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.statusCode = http.StatusOK
		g.wroteHeader = true
	}
	return g.buf.Write(b)
}

// commit flushes buffered status and body to the real writer.
func (g *panicGuardWriter) commit() {
	if g.committed {
		return
	}
	g.committed = true
	for k, vals := range g.header {
		dst := g.real.Header()
		dst.Del(k)
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
	if g.wroteHeader {
		g.real.WriteHeader(g.statusCode)
	}
	if g.buf.Len() > 0 {
		_, _ = g.real.Write(g.buf.Bytes())
	}
}

// discard drops all buffered writes and clears any headers the handler set.
func (g *panicGuardWriter) discard() {
	g.buf.Reset()
	g.statusCode = 0
	g.wroteHeader = false
	g.header = nil
}

// PanicHandler is a callback invoked when a panic is recovered.
// It receives the response writer, request, recovered value, and stack trace.
// If set, it replaces the default 500 JSON response — the handler is
// responsible for writing the HTTP status and body.
type PanicHandler func(w http.ResponseWriter, r *http.Request, err any, stack []byte)

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

	// IncludeStackInResponse adds the panic value and stack trace to the
	// default 500 JSON response body (as "panic" and "stack" fields). It is
	// intended for local debugging only.
	//
	// SECURITY: never enable this in production — it leaks internal stack
	// traces and panic messages to clients. Default false. Has no effect when
	// a custom Handler is set (the handler owns the response body).
	IncludeStackInResponse bool

	// Handler is an optional custom panic handler. When set, it is called
	// instead of the default 500 JSON response. The handler receives the
	// response writer, request, recovered panic value, and captured stack
	// trace. Logging is still performed by the middleware unless
	// DisablePrintStack is true.
	Handler PanicHandler
}

// DefaultConfig returns the default recovery configuration.
func DefaultConfig() Config {
	return Config{
		StackSize:         4096,
		DisableStackAll:   false,
		DisablePrintStack: false,
	}
}

// defaultResponse writes the generic 500 JSON error to the given ResponseWriter.
// When cfg.IncludeStackInResponse is true, the panic value and stack trace are
// added as "panic" and "stack" fields (debug only — never enable in production).
func defaultResponse(w http.ResponseWriter, cfg Config, panicVal string, stack []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	body := map[string]string{
		"error":   "internal_error",
		"message": "Internal server error",
	}
	if cfg.IncludeStackInResponse {
		body["panic"] = panicVal
		body["stack"] = string(stack)
	}
	_ = json.NewEncoder(w).Encode(body)
}

// New creates a panic recovery middleware with optional configuration.
// If no config is provided, DefaultConfig is used.
func New(config ...Config) aarv.NativeMiddleware {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	if cfg.StackSize <= 0 {
		cfg.StackSize = 4096
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			defer func() {
				if rec := recover(); rec != nil {
					stack := make([]byte, cfg.StackSize)
					length := runtime.Stack(stack, !cfg.DisableStackAll)
					stack = stack[:length]

					err := fmt.Sprintf("%v", rec)
					path := c.Path()

					if !cfg.DisablePrintStack {
						slog.Error("panic recovered",
							"error", err,
							"method", c.Method(),
							"path", path,
							"stack", string(stack),
						)
					} else {
						slog.Error("panic recovered",
							"error", err,
							"method", c.Method(),
							"path", path,
						)
					}

					w := c.Response()
					if cfg.Handler != nil {
						guard := &panicGuardWriter{real: w}
						panicked := true
						func() {
							defer func() {
								if nested := recover(); nested != nil {
									slog.Error("panic in custom recovery handler",
										"original", err,
										"nested", fmt.Sprintf("%v", nested),
									)
								}
							}()
							cfg.Handler(guard, c.RawRequest(), rec, stack)
							panicked = false
						}()
						if panicked {
							guard.discard()
							defaultResponse(w, cfg, err, stack)
						} else {
							guard.commit()
						}
					} else {
						defaultResponse(w, cfg, err, stack)
					}
				}
			}()
			return next(c)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
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

					if cfg.Handler != nil {
						guard := &panicGuardWriter{real: w}
						panicked := true
						func() {
							defer func() {
								if nested := recover(); nested != nil {
									slog.Error("panic in custom recovery handler",
										"original", err,
										"nested", fmt.Sprintf("%v", nested),
									)
								}
							}()
							cfg.Handler(guard, r, rec, stack)
							panicked = false
						}()
						if panicked {
							guard.discard()
							defaultResponse(w, cfg, err, stack)
						} else {
							guard.commit()
						}
					} else {
						defaultResponse(w, cfg, err, stack)
					}
				}
			}()

			next.ServeHTTP(w, r)
		})
	})
	return aarv.RegisterNativeMiddleware(m, native)
}
