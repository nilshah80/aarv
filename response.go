package aarv

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
	"sync"
)

// bufferedResponseWriter buffers the response body until flush.
// This allows OnSend hooks to inspect/modify the response before it's sent.
type bufferedResponseWriter struct {
	http.ResponseWriter
	buf        bytes.Buffer
	statusCode int
	written    bool
	bypassed   bool // true for streaming responses
}

var bufferedWriterPool = sync.Pool{
	New: func() any {
		return &bufferedResponseWriter{}
	},
}

func acquireBufferedWriter(w http.ResponseWriter) *bufferedResponseWriter {
	bw := bufferedWriterPool.Get().(*bufferedResponseWriter)
	bw.ResponseWriter = w
	bw.buf.Reset()
	bw.statusCode = http.StatusOK
	bw.written = false
	bw.bypassed = false
	return bw
}

func releaseBufferedWriter(bw *bufferedResponseWriter) {
	bw.ResponseWriter = nil
	bw.buf.Reset()
	bufferedWriterPool.Put(bw)
}

func (bw *bufferedResponseWriter) WriteHeader(code int) {
	if !bw.written {
		bw.statusCode = code
		bw.written = true
	}
	// If bypassed, write directly
	if bw.bypassed {
		bw.ResponseWriter.WriteHeader(code)
	}
}

func (bw *bufferedResponseWriter) Write(b []byte) (int, error) {
	if !bw.written {
		bw.written = true
	}
	// If bypassed (streaming), write directly
	if bw.bypassed {
		return bw.ResponseWriter.Write(b)
	}
	// Otherwise buffer the response
	return bw.buf.Write(b)
}

// Bypass switches to direct writing mode for streaming responses.
func (bw *bufferedResponseWriter) Bypass() {
	bw.bypassed = true
	// Flush any buffered content first
	if bw.buf.Len() > 0 {
		bw.ResponseWriter.WriteHeader(bw.statusCode)
		bw.ResponseWriter.Write(bw.buf.Bytes())
		bw.buf.Reset()
	}
}

// Body returns the buffered response body.
func (bw *bufferedResponseWriter) Body() []byte {
	return bw.buf.Bytes()
}

// SetBody replaces the buffered response body (for OnSend hooks).
func (bw *bufferedResponseWriter) SetBody(data []byte) {
	bw.buf.Reset()
	bw.buf.Write(data)
}

// StatusCode returns the pending status code.
func (bw *bufferedResponseWriter) StatusCode() int {
	return bw.statusCode
}

// Flush implements http.Flusher. It flushes buffered data to the client.
func (bw *bufferedResponseWriter) Flush() {
	if bw.bypassed {
		// If bypassed, delegate to underlying flusher if available
		if f, ok := bw.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	// Send buffered content
	body := bw.buf.Bytes()
	if len(body) > 0 {
		bw.ResponseWriter.Header().Set("Content-Length", itoa(len(body)))
	}
	bw.ResponseWriter.WriteHeader(bw.statusCode)
	if len(body) > 0 {
		bw.ResponseWriter.Write(body)
	}
	bw.buf.Reset()
	bw.bypassed = true // After flush, further writes go direct
}

// flush is an internal method for use in ServeHTTP.
func (bw *bufferedResponseWriter) flush() {
	if bw.bypassed {
		return
	}
	body := bw.buf.Bytes()
	if len(body) > 0 {
		bw.ResponseWriter.Header().Set("Content-Length", itoa(len(body)))
	}
	bw.ResponseWriter.WriteHeader(bw.statusCode)
	if len(body) > 0 {
		bw.ResponseWriter.Write(body)
	}
}

// Unwrap returns the underlying http.ResponseWriter.
func (bw *bufferedResponseWriter) Unwrap() http.ResponseWriter {
	return bw.ResponseWriter
}

// Hijack implements http.Hijacker.
func (bw *bufferedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := bw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Push implements http.Pusher.
func (bw *bufferedResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := bw.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

// itoa converts int to string without importing strconv.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
