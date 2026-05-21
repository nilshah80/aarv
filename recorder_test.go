package aarv

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStatusRecorder_DefaultStatusOK locks in the documented contract:
// observation middleware that reads Status before any explicit WriteHeader
// (or after only Write) sees http.StatusOK, matching what the stdlib
// puts on the wire.
func TestStatusRecorder_DefaultStatusOK(t *testing.T) {
	w := httptest.NewRecorder()
	r := NewStatusRecorder(w)

	if got := r.Status(); got != http.StatusOK {
		t.Fatalf("Status before any write = %d, want %d", got, http.StatusOK)
	}
	if got := r.BytesWritten(); got != 0 {
		t.Fatalf("BytesWritten before any write = %d, want 0", got)
	}
}

// TestStatusRecorder_WriteHeaderOnce ensures the first explicit status is
// the one recorded, even if a buggy handler calls WriteHeader twice.
func TestStatusRecorder_WriteHeaderOnce(t *testing.T) {
	w := httptest.NewRecorder()
	r := NewStatusRecorder(w)

	r.WriteHeader(http.StatusCreated)
	r.WriteHeader(http.StatusInternalServerError)

	if got := r.Status(); got != http.StatusCreated {
		t.Fatalf("Status after duplicate WriteHeader = %d, want %d", got, http.StatusCreated)
	}
	if got := w.Code; got != http.StatusCreated {
		t.Fatalf("underlying recorder status = %d, want %d (only first WriteHeader is forwarded as status)", got, http.StatusCreated)
	}
}

// TestStatusRecorder_WriteWithoutHeader exercises the implicit-200 path:
// a handler that only calls Write should yield Status() == 200 and an
// accurate byte count.
func TestStatusRecorder_WriteWithoutHeader(t *testing.T) {
	w := httptest.NewRecorder()
	r := NewStatusRecorder(w)

	body := []byte("hello world")
	n, err := r.Write(body)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(body) {
		t.Fatalf("Write n = %d, want %d", n, len(body))
	}
	if got := r.Status(); got != http.StatusOK {
		t.Fatalf("Status after Write only = %d, want %d", got, http.StatusOK)
	}
	if got := r.BytesWritten(); got != int64(len(body)) {
		t.Fatalf("BytesWritten = %d, want %d", got, len(body))
	}
}

// TestStatusRecorder_BytesWrittenAccumulates checks the total matches
// what the underlying writer accepted across multiple Write calls. This
// is the metric "response_size" middleware will read.
func TestStatusRecorder_BytesWrittenAccumulates(t *testing.T) {
	w := httptest.NewRecorder()
	r := NewStatusRecorder(w)

	r.WriteHeader(http.StatusOK)
	chunks := [][]byte{[]byte("alpha"), []byte("-"), []byte("beta")}
	var want int64
	for _, c := range chunks {
		n, err := r.Write(c)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		want += int64(n)
	}
	if got := r.BytesWritten(); got != want {
		t.Fatalf("BytesWritten = %d, want %d", got, want)
	}
}

// TestStatusRecorder_Unwrap confirms http.ResponseController and similar
// pass-through helpers can reach the underlying writer. Without this,
// flushing, hijacking, and HTTP/2 push silently break for any middleware
// that wraps the writer with StatusRecorder.
func TestStatusRecorder_Unwrap(t *testing.T) {
	w := httptest.NewRecorder()
	r := NewStatusRecorder(w)

	if r.Unwrap() != w {
		t.Fatal("Unwrap() did not return the original ResponseWriter")
	}
}

// TestStatusRecorder_WriteErrorBytesReflectAccepted verifies the byte
// count tracks what the underlying writer accepted, not what the caller
// asked it to write. A short-write writer contributes only the accepted
// prefix.
func TestStatusRecorder_WriteErrorBytesReflectAccepted(t *testing.T) {
	short := &shortWriter{n: 3}
	r := NewStatusRecorder(short)

	n, err := r.Write([]byte("hello"))
	if !errors.Is(err, errShort) {
		t.Fatalf("Write err = %v, want errShort", err)
	}
	if n != 3 {
		t.Fatalf("Write n = %d, want 3", n)
	}
	if got := r.BytesWritten(); got != 3 {
		t.Fatalf("BytesWritten = %d, want 3", got)
	}
}

// TestStatusRecorder_Reset rebinds a recycled recorder; status, bytes,
// and the wroteHeader flag must all be cleared so the next request does
// not inherit stale state from the previous one.
func TestStatusRecorder_Reset(t *testing.T) {
	w1 := httptest.NewRecorder()
	r := NewStatusRecorder(w1)
	r.WriteHeader(http.StatusTeapot)
	if _, err := r.Write([]byte("xxx")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	w2 := httptest.NewRecorder()
	r.Reset(w2)

	if got := r.Status(); got != http.StatusOK {
		t.Fatalf("Status after Reset = %d, want %d", got, http.StatusOK)
	}
	if got := r.BytesWritten(); got != 0 {
		t.Fatalf("BytesWritten after Reset = %d, want 0", got)
	}
	if r.Unwrap() != w2 {
		t.Fatal("Unwrap after Reset did not return the new writer")
	}

	// New writer should record cleanly; the previous writer should be
	// untouched by subsequent activity.
	r.WriteHeader(http.StatusAccepted)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("post-reset WriteHeader did not reach new writer (got %d)", w2.Code)
	}
	if w1.Code != http.StatusTeapot {
		t.Fatalf("post-reset write leaked to previous writer (got %d, want untouched %d)", w1.Code, http.StatusTeapot)
	}
}

var errShort = errors.New("short write")

// shortWriter accepts only the first n bytes of any Write and returns
// errShort. Used to test BytesWritten accounting under partial-write
// errors.
type shortWriter struct {
	header http.Header
	n      int
}

func (s *shortWriter) Header() http.Header {
	if s.header == nil {
		s.header = http.Header{}
	}
	return s.header
}

func (s *shortWriter) Write(b []byte) (int, error) {
	if len(b) > s.n {
		return s.n, errShort
	}
	return len(b), nil
}

func (s *shortWriter) WriteHeader(int) {}
