package verboselog

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestDumpLogger_Basic(t *testing.T) {
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())

	app.Get("/test", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"message": "hello"})
	})

	req := httptest.NewRequest("GET", "/test?foo=bar&baz=qux", nil)
	req.Header.Set("User-Agent", "TestClient/1.0")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "http_dump") {
		t.Errorf("expected log to contain 'http_dump', got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "/test") {
		t.Errorf("expected log to contain '/test', got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "foo") {
		t.Errorf("expected log to contain query param 'foo', got: %s", logOutput)
	}
}

func TestDumpLogger_RequestBody(t *testing.T) {
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())

	app.Post("/users", func(c *aarv.Context) error {
		return c.JSON(201, map[string]string{"id": "1"})
	})

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "alice") {
		t.Errorf("expected log to contain request body 'alice', got: %s", logOutput)
	}
}

func TestDumpLogger_SensitiveHeaderRedaction(t *testing.T) {
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())

	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer secret-token-12345")
	req.Header.Set("X-Custom", "visible-value")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "secret-token-12345") {
		t.Errorf("sensitive header should be redacted, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "visible-value") {
		t.Errorf("non-sensitive header should be visible, got: %s", logOutput)
	}
}

func TestDumpLogger_SensitiveBodyRedaction(t *testing.T) {
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())

	app.Post("/login", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	body := []byte(`{"username":"alice","password":"super-secret-123"}`)
	req := httptest.NewRequest("POST", "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "super-secret-123") {
		t.Errorf("password should be redacted, got: %s", logOutput)
	}
}

func TestDumpLogger_SkipPaths(t *testing.T) {
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		SkipPaths: []string{"/health"},
	}))

	app.Get("/health", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "http_dump") {
		t.Errorf("skipped path should not be logged, got: %s", logOutput)
	}
}

func TestDumpLogger_DisableBody(t *testing.T) {
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestBody:  false,
		LogResponseBody: false,
	}))

	app.Post("/test", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"secret": "data"})
	})

	body := []byte(`{"name":"alice"}`)
	req := httptest.NewRequest("POST", "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	// When body logging is disabled, the body fields should be empty
	if strings.Contains(logOutput, "alice") {
		t.Errorf("request body should not be logged when disabled, got: %s", logOutput)
	}
}

func TestDumpLogger_ResponseBody(t *testing.T) {
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())

	app.Get("/test", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"result": "success123"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "success123") {
		t.Errorf("response body should be logged, got: %s", logOutput)
	}
}

func BenchmarkDumpLogger(b *testing.B) {
	// Discard logs during benchmark
	slog.SetDefault(slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())

	app.Post("/users", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": "1", "name": "alice"})
	})

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkDumpLogger_NoBody(b *testing.B) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestBody:  false,
		LogResponseBody: false,
	}))

	app.Get("/test", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": "1"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}
