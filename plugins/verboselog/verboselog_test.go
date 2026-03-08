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

func TestMinimalConfigAndHelpers(t *testing.T) {
	cfg := MinimalConfig()
	if cfg.LogRequestBody || cfg.LogResponseBody || cfg.LogClientIP || cfg.MaxBodySize != 0 {
		t.Fatalf("unexpected minimal config: %#v", cfg)
	}

	rec := httptest.NewRecorder()
	writer := &bodyCapturingWriter{
		ResponseWriter: rec,
		body:           &bytes.Buffer{},
		statusCode:     200,
		maxBodySize:    4,
	}
	_, _ = writer.Write([]byte("abcdef"))
	if got := writer.body.String(); got != "abcd" {
		t.Fatalf("expected truncated captured body, got %q", got)
	}
	if writer.Unwrap() != rec {
		t.Fatal("unwrap should return base writer")
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Real-IP", "1.1.1.1")
	if got := clientIP(req); got != "1.1.1.1" {
		t.Fatalf("expected x-real-ip, got %q", got)
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "2.2.2.2, 3.3.3.3")
	if got := clientIP(req); got != "2.2.2.2" {
		t.Fatalf("expected forwarded ip, got %q", got)
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "3.3.3.3")
	if got := clientIP(req); got != "3.3.3.3" {
		t.Fatalf("expected single forwarded ip, got %q", got)
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "4.4.4.4:1234"
	if got := clientIP(req); got != "4.4.4.4" {
		t.Fatalf("expected remote addr ip, got %q", got)
	}
}

func TestDumpLogger_TruncatesBodiesAndRedactsResponseHeaders(t *testing.T) {
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		MaxBodySize:        5,
		LogRequestBody:     true,
		LogResponseBody:    true,
		LogRequestHeaders:  true,
		LogResponseHeaders: true,
		RedactSensitive:    true,
		SensitiveHeaders:   []string{"Set-Cookie"},
		SensitiveFields:    []string{"token"},
	}))

	app.Post("/test", func(c *aarv.Context) error {
		c.Response().Header().Set("Set-Cookie", "session=secret")
		return c.JSON(200, map[string]string{"token": "secret-token"})
	})

	body := []byte(`{"token":"abcdefghi"}`)
	req := httptest.NewRequest("POST", "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "[truncated]") {
		t.Fatalf("expected truncated marker, got %s", logOutput)
	}
	if !strings.Contains(logOutput, "[REDACTED]") {
		t.Fatalf("expected redaction marker, got %s", logOutput)
	}
}

func TestDumpLogger_AllowsVisibleSensitiveDataWhenRedactionDisabled(t *testing.T) {
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestHeaders:  true,
		LogResponseHeaders: true,
		LogRequestBody:     true,
		LogResponseBody:    true,
		RedactSensitive:    false,
		MaxBodySize:        64,
	}))

	app.Post("/test", func(c *aarv.Context) error {
		c.Response().Header().Set("Set-Cookie", "session=value")
		return c.JSON(200, map[string]string{"token": "secret"})
	})

	req := httptest.NewRequest("POST", "/test", bytes.NewReader([]byte(`{"token":"secret"}`)))
	req.Header.Set("Authorization", "Bearer visible")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "Bearer visible") {
		t.Fatalf("expected visible request header, got %s", logOutput)
	}
	if !strings.Contains(logOutput, "session=value") {
		t.Fatalf("expected visible response header, got %s", logOutput)
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
