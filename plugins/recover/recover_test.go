package recover

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
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

func TestNewNativeRecoverWithStack(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Get("/panic", func(c *aarv.Context) error {
		panic("native-boom")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("expected error body, got %q", rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), "\"stack\":") {
		t.Fatalf("expected stack in log, got %s", logBuf.String())
	}
}

func TestNewNativeRecoverWithoutStack(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{DisablePrintStack: true}))
	app.Get("/panic", func(c *aarv.Context) error {
		panic("quiet-native")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if strings.Contains(logBuf.String(), "\"stack\":") {
		t.Fatalf("did not expect stack, got %s", logBuf.String())
	}
}

func TestNewNativePassThrough(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Get("/ok", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "safe")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/ok", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "safe" {
		t.Fatalf("expected pass-through, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}
