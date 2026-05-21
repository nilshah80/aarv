package aarv

import "net/http"

// StatusRecorder wraps an http.ResponseWriter to capture the final HTTP
// status code and the total number of body bytes written. Observation
// middleware (access logging, metrics, tracing) reads these after the
// handler returns so it does not have to instrument every Write call site.
//
// StatusRecorder transparently forwards every method to the underlying
// ResponseWriter and exposes Unwrap so http.ResponseController can reach
// past the wrapper to call Flush, Hijack, push HTTP/2 streams, or any
// other interface implemented by the original writer.
//
// The public constructor NewStatusRecorder is intentionally unpooled.
// Middleware authors writing one-off observability wrappers can use it
// directly without thinking about lifecycle:
//
//	rw := aarv.NewStatusRecorder(w)
//	next.ServeHTTP(rw, r)
//	logRequest(r, rw.Status(), rw.BytesWritten())
//
// Callers that pool StatusRecorder instances themselves (typical inside a
// hot-path plugin) can re-bind a recycled recorder via Reset. Pooling is
// not exposed through this package — the lifecycle of a pool is the
// caller's contract.
type StatusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

// NewStatusRecorder returns a fresh StatusRecorder wrapping w. The
// recorded status defaults to http.StatusOK so middleware that observes
// after a handler returns without explicitly writing a header still sees
// the value the stdlib would have sent.
//
// The returned value is not pooled. Allocate one per request; the GC cost
// is minimal compared to a typical request's lifecycle.
func NewStatusRecorder(w http.ResponseWriter) *StatusRecorder {
	return &StatusRecorder{
		ResponseWriter: w,
		status:         http.StatusOK,
	}
}

// Reset re-binds r to a new underlying ResponseWriter and clears the
// recorded status (back to http.StatusOK), byte count, and header-written
// flag. Intended for callers that pool *StatusRecorder instances; the
// alternative is to allocate a new recorder per request, which is what
// NewStatusRecorder does.
//
// Reset must be called before the recorder is used for a new request.
// Calling it after Write or WriteHeader has already been issued for a
// prior request is the normal pooled path; calling it concurrently with
// an in-flight request is undefined behavior.
func (r *StatusRecorder) Reset(w http.ResponseWriter) {
	r.ResponseWriter = w
	r.status = http.StatusOK
	r.bytes = 0
	r.wroteHeader = false
}

// Status returns the HTTP status code recorded for the request. If the
// handler never explicitly called WriteHeader and only called Write, the
// recorded status is http.StatusOK — matching what the stdlib would have
// sent on the wire.
func (r *StatusRecorder) Status() int {
	return r.status
}

// BytesWritten returns the cumulative number of bytes successfully written
// to the response body across all Write calls. Excludes header bytes.
func (r *StatusRecorder) BytesWritten() int64 {
	return r.bytes
}

// WriteHeader records the first explicit status code and forwards to the
// underlying ResponseWriter. Subsequent calls forward to the underlying
// writer (which the stdlib will warn about) but do not overwrite the
// recorded status — the first status is the one observers care about.
func (r *StatusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

// Write forwards body bytes to the underlying ResponseWriter and adds the
// number of bytes the underlying writer accepted to the running total.
// Following the http.ResponseWriter contract, an implicit WriteHeader is
// considered to have fired with status http.StatusOK if Write is called
// before WriteHeader.
func (r *StatusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Unwrap returns the underlying ResponseWriter. http.ResponseController
// (and middleware that reaches for Flusher / Hijacker / Pusher / etc.)
// uses Unwrap to walk past the recording wrapper. Without it, streaming,
// hijacking, and HTTP/2 push silently break under any middleware that
// inserts a StatusRecorder.
func (r *StatusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
