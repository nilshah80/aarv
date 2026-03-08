package recover

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.StackSize != 4096 || cfg.DisableStackAll || cfg.DisablePrintStack {
		t.Fatalf("unexpected default config: %#v", cfg)
	}
}

func TestNewPassThroughWithoutPanic(t *testing.T) {
	handler := New()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestNewRecoversAndLogsStack(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	handler := New(Config{StackSize: 0})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content-type %q", got)
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "panic recovered") || !strings.Contains(logOutput, "\"stack\":") {
		t.Fatalf("expected panic log with stack, got %s", logOutput)
	}
}

func TestNewRecoversWithoutStackLogging(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	handler := New(Config{
		DisablePrintStack: true,
		DisableStackAll:   true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("quiet-boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if strings.Contains(logBuf.String(), "\"stack\":") {
		t.Fatalf("did not expect stack field, got %s", logBuf.String())
	}
}
