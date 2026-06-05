package bodylimit

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestDefaultConfig(t *testing.T) {
	if got := DefaultConfig().MaxBytes; got != 1<<20 {
		t.Fatalf("expected 1MB default, got %d", got)
	}
}

func TestNewUsesDefaultLimit(t *testing.T) {
	handler := New(0).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	lw := &limitResponseWriter{
		ResponseWriter: base,
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
	handler := NewWithResponse(0).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestNewNativeMiddlewarePath(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(16))
	app.Post("/upload", func(c *aarv.Context) error {
		_, err := io.ReadAll(c.Request().Body)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "too large"})
		}
		return c.NoContent(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/upload", bytes.NewReader([]byte("ok"))))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/upload", bytes.NewReader(bytes.Repeat([]byte("x"), 32))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}

func TestNewWithResponseNativeMiddlewarePath(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(NewWithResponse(16))
	app.Post("/upload", func(c *aarv.Context) error {
		_, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return err
		}
		return c.Text(http.StatusOK, "done")
	})

	// Under limit — should succeed
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/upload", bytes.NewReader([]byte("ok"))))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Over limit — should auto-respond 413
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/upload", bytes.NewReader(bytes.Repeat([]byte("x"), 32))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 from NewWithResponse native path, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "payload_too_large") {
		t.Fatalf("expected payload_too_large body, got %q", rec.Body.String())
	}
}

func TestNewWithResponseStdlibAutoResponds413(t *testing.T) {
	middleware := NewWithResponse(16)
	// Handler reads the body but does NOT check for MaxBytesError —
	// the middleware should auto-send 413.
	handler := middleware.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			// Handler sees the error but doesn't handle it
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Over limit
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/upload", bytes.NewReader(bytes.Repeat([]byte("x"), 32))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 from NewWithResponse stdlib path, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "payload_too_large") {
		t.Fatalf("expected payload_too_large body, got %q", rec.Body.String())
	}

	// Under limit — should pass through
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/upload", bytes.NewReader([]byte("ok"))))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestBodyLimitTrackerRead(t *testing.T) {
	tracker := &bodyLimitTracker{
		ReadCloser: http.MaxBytesReader(httptest.NewRecorder(), io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 32))), 16),
	}
	buf := make([]byte, 64)
	// Read until error — MaxBytesReader will eventually return MaxBytesError
	var err error
	for err == nil {
		_, err = tracker.Read(buf)
	}
	if !tracker.limitExceeded {
		t.Fatal("expected limitExceeded to be true after exceeding MaxBytesReader limit")
	}
}

func TestNewWithResponseNativeBufferCapCommitsDownstreamResponse(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(NewWithResponse(16))
	app.Post("/upload", func(c *aarv.Context) error {
		if _, err := c.Response().Write(bytes.Repeat([]byte("a"), maxInterceptBufSize+32)); err != nil {
			return err
		}
		_, err := io.ReadAll(c.Request().Body)
		return err
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/upload", bytes.NewReader(bytes.Repeat([]byte("x"), 64))))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected downstream response to stand after cap flush, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "payload_too_large") {
		t.Fatalf("expected best-effort path to preserve committed downstream response, got %q", rec.Body.String())
	}
	if rec.Body.Len() <= maxInterceptBufSize {
		t.Fatalf("expected large downstream body to be committed, got len=%d", rec.Body.Len())
	}
}

func TestLimitInterceptWriterUnwrap(t *testing.T) {
	base := httptest.NewRecorder()
	w := &limitInterceptWriter{ResponseWriter: base}
	if w.Unwrap() != base {
		t.Fatal("Unwrap should return underlying writer")
	}
}

func TestLimitInterceptWriterFlushToAlreadyFlushed(t *testing.T) {
	base := httptest.NewRecorder()
	w := &limitInterceptWriter{ResponseWriter: base}
	w.Header().Set("X-Test", "val")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("buffered"))

	// First flush should work
	w.flushTo(base)
	if base.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", base.Code)
	}
	if base.Body.String() != "buffered" {
		t.Fatalf("expected buffered body, got %q", base.Body.String())
	}
	if base.Header().Get("X-Test") != "val" {
		t.Fatal("expected header to be copied on flush")
	}

	// Second flush should be a no-op
	base2 := httptest.NewRecorder()
	w.flushTo(base2)
	if base2.Code != http.StatusOK {
		t.Fatal("expected second flushTo to be a no-op")
	}
}

func TestLimitInterceptWriterDirectWriteAfterFlush(t *testing.T) {
	base := httptest.NewRecorder()
	w := &limitInterceptWriter{ResponseWriter: base}
	w.flushed = true // simulate already-flushed state
	n, err := w.Write([]byte("direct"))
	if err != nil || n != 6 {
		t.Fatalf("expected direct write, got n=%d err=%v", n, err)
	}
	if base.Body.String() != "direct" {
		t.Fatalf("expected direct write body, got %q", base.Body.String())
	}
}
