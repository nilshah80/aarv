package bodylimit

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	if got := DefaultConfig().MaxBytes; got != 1<<20 {
		t.Fatalf("expected 1MB default, got %d", got)
	}
}

func TestNewUsesDefaultLimit(t *testing.T) {
	handler := New(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			SendPayloadTooLarge(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	body := bytes.Repeat([]byte("a"), int(DefaultConfig().MaxBytes)+1)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/", bytes.NewReader(body)))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "payload_too_large") {
		t.Fatalf("expected payload_too_large response, got %s", rec.Body.String())
	}
}

func TestNewWithResponseAndWriterHelpers(t *testing.T) {
	base := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	lw := &limitResponseWriter{
		ResponseWriter: base,
		request:        req,
		maxBytes:       16,
	}

	lw.WriteHeader(http.StatusCreated)
	if base.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", base.Code)
	}
	if lw.Unwrap() != base {
		t.Fatal("unwrap should return underlying writer")
	}

	base = httptest.NewRecorder()
	lw = &limitResponseWriter{ResponseWriter: base}
	n, err := lw.Write([]byte("ok"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 2 || base.Body.String() != "ok" {
		t.Fatalf("unexpected write result %d %q", n, base.Body.String())
	}

	called := false
	handler := NewWithResponse(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte("pass"))
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/", bytes.NewReader([]byte("ok"))))
	if !called {
		t.Fatal("expected next handler to run")
	}
	if rec.Body.String() != "pass" {
		t.Fatalf("expected pass-through body, got %q", rec.Body.String())
	}
}

func TestSendPayloadTooLarge(t *testing.T) {
	rec := httptest.NewRecorder()
	SendPayloadTooLarge(rec)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content-type %q", got)
	}
	if !strings.Contains(rec.Body.String(), "Request body too large") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
}
