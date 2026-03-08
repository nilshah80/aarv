package timeout

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	if got := DefaultConfig().Timeout; got != 30*time.Second {
		t.Fatalf("expected 30s default timeout, got %v", got)
	}
}

func TestTimeoutWriterHelpers(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := &timeoutWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusAccepted,
	}

	tw.WriteHeader(http.StatusCreated)
	tw.WriteHeader(http.StatusNoContent)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected first status to win, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	tw = &timeoutWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusAccepted,
	}
	n, err := tw.Write([]byte("ok"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 2 || rec.Code != http.StatusAccepted || rec.Body.String() != "ok" {
		t.Fatalf("unexpected write result code=%d body=%q n=%d", rec.Code, rec.Body.String(), n)
	}

	tw.timedOut = true
	n, err = tw.Write([]byte("late"))
	if err != context.DeadlineExceeded || n != 0 {
		t.Fatalf("expected deadline exceeded after timeout, got n=%d err=%v", n, err)
	}
	if tw.Unwrap() != rec {
		t.Fatal("unwrap should return underlying writer")
	}
}

func TestNewUsesDefaultTimeoutAndPassesThrough(t *testing.T) {
	handler := New(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("unexpected response status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestNewReturnsGatewayTimeout(t *testing.T) {
	handler := New(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content-type %q", got)
	}
	if !strings.Contains(rec.Body.String(), "gateway_timeout") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
}

func TestNewRePanicsOnHandlerPanic(t *testing.T) {
	defer func() {
		if got := recover(); got != "boom" {
			t.Fatalf("expected boom panic, got %v", got)
		}
	}()

	handler := New(100 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}
