package idempotency

import (
	"bytes"
	"net/http"
	"sync"
)

// captureWriter buffers a downstream response so the middleware can
// persist it for later replay. Operates as a state machine:
//
//   - BUFFERING (initial): Write appends to the body buffer; the
//     underlying writer is untouched until either request completion
//     or overflow.
//   - PASSTHROUGH (after overflow): Write forwards directly to the
//     underlying writer. The captured headers and any buffered prefix
//     have already been flushed via flushOverflow.
//
// The state machine guarantees:
//
//   - Under-cap responses never write to the underlying writer until
//     the middleware decides to commit them after Save (FlushUnderCap).
//   - Over-cap responses reach the client unchanged with an
//     Idempotency-Cached: false; reason=size header, and no buffer
//     grows beyond `cap`.
//
// The writer implements Unwrap() http.ResponseWriter so
// http.ResponseController works.
type captureWriter struct {
	http.ResponseWriter
	cap        int64
	written    int64
	body       *bytes.Buffer
	headers    http.Header
	statusCode int
	overflowed bool
	headerSent bool // tracks whether the underlying writer has been written to
	committed  bool // FlushUnderCap has run
}

var captureWriterPool = sync.Pool{
	New: func() any { return &captureWriter{} },
}

func acquireCaptureWriter(w http.ResponseWriter, cap int64) *captureWriter {
	cw := captureWriterPool.Get().(*captureWriter)
	cw.ResponseWriter = w
	cw.cap = cap
	cw.written = 0
	if cw.body == nil {
		cw.body = &bytes.Buffer{}
	} else {
		cw.body.Reset()
	}
	cw.headers = http.Header{}
	cw.statusCode = 0
	cw.overflowed = false
	cw.headerSent = false
	cw.committed = false
	return cw
}

func releaseCaptureWriter(cw *captureWriter) {
	if cw == nil {
		return
	}
	cw.ResponseWriter = nil
	if cw.body != nil && cw.body.Cap() > 1<<20 {
		cw.body = nil // let GC reclaim large buffers
	}
	cw.headers = nil
	captureWriterPool.Put(cw)
}

// Header returns the buffered header map until the writer has flushed
// (either via FlushUnderCap or via overflow). After flush, headers must
// be set directly on the underlying writer.
func (cw *captureWriter) Header() http.Header {
	if cw.headerSent {
		return cw.ResponseWriter.Header()
	}
	return cw.headers
}

// WriteHeader records the status code without touching the underlying
// writer (until flush).
func (cw *captureWriter) WriteHeader(code int) {
	if cw.headerSent {
		return
	}
	if cw.statusCode == 0 {
		cw.statusCode = code
	}
}

// Write appends to the buffer until the cap is exceeded, at which point
// the writer transitions to passthrough.
func (cw *captureWriter) Write(p []byte) (int, error) {
	if cw.overflowed {
		// Already flushed; forward directly.
		return cw.ResponseWriter.Write(p)
	}
	if cw.cap > 0 && cw.written+int64(len(p)) > cw.cap {
		cw.flushOverflow()
		return cw.ResponseWriter.Write(p)
	}
	n, err := cw.body.Write(p)
	cw.written += int64(n)
	return n, err
}

// Unwrap exposes the underlying writer so http.ResponseController can
// reach it for streaming, hijacking, or HTTP/2 push.
func (cw *captureWriter) Unwrap() http.ResponseWriter { return cw.ResponseWriter }

// flushOverflow transitions BUFFERING → PASSTHROUGH:
//  1. Annotate the response with the explanatory header.
//  2. Write the captured statusCode to the underlying writer.
//  3. Forward any already-buffered prefix.
//
// Subsequent Writes go directly through the underlying writer.
func (cw *captureWriter) flushOverflow() {
	if cw.overflowed {
		return
	}
	cw.overflowed = true

	dst := cw.ResponseWriter.Header()
	for k, vs := range cw.headers {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	dst.Set("Idempotency-Cached", "false; reason=size")

	status := cw.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	cw.ResponseWriter.WriteHeader(status)
	cw.headerSent = true
	if cw.body.Len() > 0 {
		_, _ = cw.ResponseWriter.Write(cw.body.Bytes())
		cw.body.Reset()
	}
}

// FlushUnderCap commits a buffered (under-cap) response to the underlying
// writer after the middleware has persisted it. Idempotent: subsequent
// calls are no-ops.
func (cw *captureWriter) FlushUnderCap() {
	if cw.overflowed || cw.committed {
		return
	}
	cw.committed = true
	dst := cw.ResponseWriter.Header()
	for k, vs := range cw.headers {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	status := cw.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	cw.ResponseWriter.WriteHeader(status)
	cw.headerSent = true
	if cw.body.Len() > 0 {
		_, _ = cw.ResponseWriter.Write(cw.body.Bytes())
	}
}

// Snapshot copies the captured response into a stored shape (for under-cap
// responses only — overflowed responses must not be cached). Returns nil
// when the writer overflowed.
func (cw *captureWriter) Snapshot() *Response {
	if cw.overflowed {
		return nil
	}
	hdrs := http.Header{}
	for k, vs := range cw.headers {
		if isHopByHop(k) {
			continue
		}
		hdrs[k] = append([]string(nil), vs...)
	}
	body := append([]byte(nil), cw.body.Bytes()...)
	status := cw.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	return &Response{
		StatusCode: status,
		Headers:    hdrs,
		Body:       body,
	}
}

// Status returns the captured status code (200 if WriteHeader was never
// called).
func (cw *captureWriter) Status() int {
	if cw.statusCode == 0 {
		return http.StatusOK
	}
	return cw.statusCode
}

// Overflowed reports whether the response exceeded the cap and was
// streamed through unchanged.
func (cw *captureWriter) Overflowed() bool { return cw.overflowed }

// hopByHopHeaders is the set of headers that must not be forwarded
// across HTTP intermediaries (RFC 7230 §6.1). We strip them from the
// snapshot so a replayed response doesn't carry stale connection
// metadata.
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func isHopByHop(name string) bool {
	_, ok := hopByHopHeaders[http.CanonicalHeaderKey(name)]
	return ok
}
