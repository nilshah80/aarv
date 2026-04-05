// Package bodylimit provides request body size limiting middleware for the aarv
// framework.
//
// It wraps the request body with http.MaxBytesReader to enforce a maximum request
// body size. If the limit is exceeded, it returns a 413 Payload Too Large response.
package bodylimit

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/internal/headerbuffer"
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

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			c.SetBody(http.MaxBytesReader(c.Response(), c.BodyReader(), maxBytes))
			return next(c)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
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
	})
	return aarv.RegisterNativeMiddleware(m, native)
}

// NewWithResponse creates a body size limit middleware that detects when the
// request body exceeds the limit and writes a 413 JSON error response on a
// best-effort basis.
//
// The middleware buffers the downstream response so it can replace an
// error response with a proper 413. If the handler writes more than 8KB
// before the limit violation is detected, the buffered response is committed
// and can no longer be replaced — the handler's own response stands.
// In practice this rarely matters because body-limit errors surface during
// request parsing, before significant response output.
//
// In the native path, it intercepts the framework's auto-generated 500.
// In the stdlib path, it wraps the request body to detect MaxBytesError
// and intercepts the response if no headers have been sent yet.
func NewWithResponse(maxBytes int64) aarv.Middleware {
	if maxBytes <= 0 {
		maxBytes = DefaultConfig().MaxBytes
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			tracker := &bodyLimitTracker{
				ReadCloser: http.MaxBytesReader(c.Response(), c.BodyReader(), maxBytes),
			}
			c.SetBody(tracker)

			lw := &limitInterceptWriter{ResponseWriter: c.Response()}
			orig := c.Response()
			c.SetResponse(lw)

			err := next(c)

			c.SetResponse(orig)

			// If the body limit was exceeded and the downstream wrote an
			// error response (e.g. the framework's default 500), replace
			// it with a proper 413. If nothing was written yet, also send 413.
			if tracker.limitExceeded && !lw.flushed {
				SendPayloadTooLarge(orig)
				return nil
			}
			// Flush any buffered response from downstream.
			lw.flushTo(orig)

			return err
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tracker := &bodyLimitTracker{ReadCloser: http.MaxBytesReader(w, r.Body, maxBytes)}
			r.Body = tracker

			lw := &limitResponseWriter{
				ResponseWriter: w,
			}

			next.ServeHTTP(lw, r)

			// If the body limit was exceeded and the handler hasn't written
			// a response yet, send a 413 automatically.
			if tracker.limitExceeded && !lw.wroteHeader {
				SendPayloadTooLarge(w)
			}
		})
	})
	return aarv.RegisterNativeMiddleware(m, native)
}

// bodyLimitTracker wraps a MaxBytesReader body and tracks whether the
// limit was exceeded.
type bodyLimitTracker struct {
	io.ReadCloser
	limitExceeded bool
}

func (t *bodyLimitTracker) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			t.limitExceeded = true
		}
	}
	return n, err
}

// limitResponseWriter tracks whether headers have been written so we can
// detect if the handler already sent a response before we try to send 413.
// Used in the stdlib path where we can write directly.
type limitResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (lw *limitResponseWriter) WriteHeader(code int) {
	lw.wroteHeader = true
	lw.ResponseWriter.WriteHeader(code)
}

func (lw *limitResponseWriter) Write(b []byte) (int, error) {
	if !lw.wroteHeader {
		lw.wroteHeader = true
	}
	return lw.ResponseWriter.Write(b)
}

// Unwrap returns the underlying http.ResponseWriter.
func (lw *limitResponseWriter) Unwrap() http.ResponseWriter {
	return lw.ResponseWriter
}

// limitInterceptWriter buffers the downstream response so the native-path
// middleware can discard it when a body-limit violation is detected. The
// framework's error handler may have already written a 500 through this
// writer; buffering lets us replace it with a proper 413.
// Headers are buffered locally so a suppressed 500 does not leak stale
// headers into the replacement 413 response.
type limitInterceptWriter struct {
	http.ResponseWriter
	headers    headerbuffer.Buffer
	statusCode int
	buf        []byte
	flushed    bool
}

func (w *limitInterceptWriter) Header() http.Header {
	return w.headers.Header()
}

func (w *limitInterceptWriter) WriteHeader(code int) {
	if !w.flushed {
		w.statusCode = code
	}
}

// maxInterceptBufSize is the maximum bytes we buffer before marking the
// response as non-replaceable. Error responses from the framework are small
// (~100 bytes), so 8KB is generous. If a handler writes more than this before
// the body limit triggers, we stop buffering and commit what we have.
const maxInterceptBufSize = 8 * 1024

func (w *limitInterceptWriter) Write(b []byte) (int, error) {
	if w.flushed {
		return w.ResponseWriter.Write(b)
	}
	if len(w.buf)+len(b) > maxInterceptBufSize {
		// Buffer would exceed cap — commit now and switch to direct writes.
		w.flushTo(w.ResponseWriter)
		return w.ResponseWriter.Write(b)
	}
	w.buf = append(w.buf, b...)
	return len(b), nil
}

func (w *limitInterceptWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// flushTo copies buffered headers, status, and body to the underlying writer.
func (w *limitInterceptWriter) flushTo(dst http.ResponseWriter) {
	if w.flushed {
		return
	}
	w.flushed = true
	w.headers.CopyTo(dst.Header())
	if w.statusCode != 0 {
		dst.WriteHeader(w.statusCode)
	}
	if len(w.buf) > 0 {
		_, _ = dst.Write(w.buf)
	}
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
