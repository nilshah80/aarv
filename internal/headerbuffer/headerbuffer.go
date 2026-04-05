package headerbuffer

import "net/http"

// Buffer owns a lazily-allocated http.Header map that can be reused across
// response-writer wrappers without exposing the real writer's header map.
type Buffer struct {
	h http.Header
}

// Header returns the buffered header map, allocating it on first use.
func (b *Buffer) Header() http.Header {
	if b.h == nil {
		b.h = make(http.Header)
	}
	return b.h
}

// Reset clears the buffered headers while retaining the allocated map.
func (b *Buffer) Reset() {
	clear(b.h)
}

// CopyTo copies the buffered headers onto the destination map.
func (b *Buffer) CopyTo(dst http.Header) {
	for k, v := range b.h {
		dst[k] = v
	}
}
