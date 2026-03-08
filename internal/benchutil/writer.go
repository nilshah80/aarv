package benchutil

import "net/http"

// DiscardResponseWriter is a minimal http.ResponseWriter for benchmarks.
// It avoids recorder/body allocations while still tracking headers and status.
type DiscardResponseWriter struct {
	header http.Header
	Status int
}

func (w *DiscardResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *DiscardResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *DiscardResponseWriter) WriteHeader(status int) {
	w.Status = status
}

func (w *DiscardResponseWriter) Reset() {
	for k := range w.header {
		delete(w.header, k)
	}
	w.Status = 0
}
