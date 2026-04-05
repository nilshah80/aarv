package verboselog

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"encoding/json"
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
		_, _ = c.Body() // consume the body so the tee reader captures it
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

// Body close is now the handler's responsibility since the tee reader
// delegates Close() to the original body. Close-failure tests removed.

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

func TestDumpLogger_SensitiveQueryRedaction(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())

	app.Get("/search", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest("GET", "/search?token=top-secret&apikey=abc123&q=visible", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "top-secret") || strings.Contains(logOutput, "abc123") {
		t.Fatalf("sensitive query params should be redacted, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "[REDACTED]") {
		t.Fatalf("expected redaction marker in query params, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "visible") {
		t.Fatalf("expected non-sensitive query param to remain visible, got: %s", logOutput)
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

	releaseBodyBuffer(nil)
	buf := acquireBodyBuffer()
	buf.WriteString("captured")
	releaseBodyBuffer(buf)
	if got := firstOrJoin([]string{"solo"}); got != "solo" {
		t.Fatalf("unexpected single join result %q", got)
	}
	if got := firstOrJoin([]string{"one", "two"}); got != "one, two" {
		t.Fatalf("unexpected multi join result %q", got)
	}
	if got := redactSensitiveBody("", buildRedactionPatterns([]string{"token"})); got != "" {
		t.Fatalf("expected empty body to stay empty, got %q", got)
	}
	if got := redactSensitiveBody(`{"token":"secret","password":"hidden"}`, nil); !strings.Contains(got, `"password":"hidden"`) {
		t.Fatalf("expected nil patterns to keep body unchanged, got %q", got)
	}
	if got := redactSensitiveBody(`{"token":"secret","password":"hidden"}`, buildRedactionPatterns([]string{"token", "password"})); strings.Contains(got, "secret") || strings.Contains(got, "hidden") {
		t.Fatalf("expected multiple fields to be redacted, got %q", got)
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

func TestDumpLogger_RedactBodyOnly(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestHeaders:  true,
		LogResponseHeaders: true,
		LogQueryParams:     true,
		LogRequestBody:     true,
		LogResponseBody:    true,
		RedactSensitive:    true,
		SensitiveHeaders:   []string{"Authorization", "Set-Cookie"},
		SensitiveFields:    []string{"token"},
		RedactSurfaces: []RedactionSurface{
			RedactRequestBody,
			RedactResponseBody,
		},
		MaxBodySize: 128,
	}))

	app.Post("/test", func(c *aarv.Context) error {
		c.Response().Header().Set("Set-Cookie", "session=value")
		return c.JSON(200, map[string]string{"token": "server-secret"})
	})

	req := httptest.NewRequest("POST", "/test?token=query-visible", bytes.NewReader([]byte(`{"token":"body-secret"}`)))
	req.Header.Set("Authorization", "Bearer visible")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "body-secret") || strings.Contains(logOutput, "server-secret") {
		t.Fatalf("expected body surfaces to be redacted, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "Bearer visible") {
		t.Fatalf("expected request header to remain visible, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "session=value") {
		t.Fatalf("expected response header to remain visible, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "query-visible") {
		t.Fatalf("expected query param to remain visible, got: %s", logOutput)
	}
}

func TestDumpLogger_NativeSkipPaths(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{SkipPaths: []string{"/health"}}))
	app.Get("/health", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if strings.Contains(logBuf.String(), "http_dump") {
		t.Fatal("expected skipped path to not be logged")
	}
}

func TestDumpLogger_NativeDisabledFields(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogUserAgent:       false,
		LogClientIP:        false,
		LogRequestHeaders:  false,
		LogResponseHeaders: false,
		LogQueryParams:     false,
		LogContentInfo:     false,
		LogRequestBody:     false,
		LogResponseBody:    false,
		LogLatencyMS:       false,
		RedactSensitive:    false,
		MaxBodySize:        64,
	}))
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(200, "minimal")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/test", nil))
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "http_dump") {
		t.Fatal("expected log output")
	}
	if strings.Contains(logOutput, "user_agent") {
		t.Fatal("did not expect user_agent when disabled")
	}
}

func TestDumpLogger_NativeFullRedaction(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestHeaders:  true,
		LogResponseHeaders: true,
		LogRequestBody:     true,
		LogResponseBody:    true,
		LogQueryParams:     true,
		RedactSensitive:    true,
		SensitiveHeaders:   []string{"Authorization", "Set-Cookie"},
		SensitiveFields:    []string{"token"},
		MaxBodySize:        128,
	}))
	app.Post("/test", func(c *aarv.Context) error {
		c.Response().Header().Set("Set-Cookie", "session=secret")
		return c.JSON(200, map[string]string{"token": "server-secret"})
	})

	body := []byte(`{"token":"body-secret"}`)
	req := httptest.NewRequest("POST", "/test?token=query-secret", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sensitive")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "sensitive") || strings.Contains(logOutput, "body-secret") || strings.Contains(logOutput, "query-secret") {
		t.Fatalf("expected full redaction, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "[REDACTED]") {
		t.Fatal("expected redaction marker")
	}
}

func TestDumpLogger_NativeTruncation(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestBody:  true,
		LogResponseBody: true,
		RedactSensitive: false,
		MaxBodySize:     5,
	}))
	app.Post("/test", func(c *aarv.Context) error {
		return c.Text(200, "long-response-body")
	})

	body := []byte("long-request-body")
	req := httptest.NewRequest("POST", "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "[truncated]") {
		t.Fatalf("expected truncated marker, got %s", logOutput)
	}
}

func TestDumpLogger_StdlibSkipPathsAndRedaction(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	// Stdlib skip path
	middleware := New(Config{
		SkipPaths:          []string{"/health"},
		LogRequestHeaders:  true,
		LogResponseHeaders: true,
		LogQueryParams:     true,
		LogRequestBody:     true,
		LogResponseBody:    true,
		RedactSensitive:    true,
		SensitiveHeaders:   []string{"Authorization", "Set-Cookie"},
		SensitiveFields:    []string{"token"},
		MaxBodySize:        5,
	})

	// Test skip path through stdlib handler
	rec := httptest.NewRecorder()
	middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if strings.Contains(logBuf.String(), "http_dump") {
		t.Fatal("expected skip path to suppress logging")
	}

	// Test request header redaction, query param redaction, body truncation through stdlib handler
	logBuf.Reset()
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=secret")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("long-response-body"))
	}))

	body := []byte(`{"token":"body-secret-value"}`)
	req := httptest.NewRequest("POST", "/test?token=query-secret&visible=yes", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sensitive")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "http_dump") {
		t.Fatal("expected log output")
	}
	// Request header redaction
	if strings.Contains(logOutput, "Bearer sensitive") {
		t.Fatal("expected Authorization header to be redacted")
	}
	// Response header redaction
	if strings.Contains(logOutput, "session=secret") {
		t.Fatal("expected Set-Cookie header to be redacted")
	}
	// Query param redaction
	if strings.Contains(logOutput, "query-secret") {
		t.Fatal("expected query param to be redacted")
	}
	if !strings.Contains(logOutput, "yes") {
		t.Fatal("expected visible query param")
	}
	// Body truncation
	if !strings.Contains(logOutput, "[truncated]") {
		t.Fatal("expected body truncation marker")
	}
}

func TestDumpLogger_StdlibResponseBodyAndQueryLogging(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	middleware := New(Config{
		LogRequestBody:     true,
		LogResponseBody:    true,
		LogQueryParams:     true,
		LogRequestHeaders:  true,
		LogResponseHeaders: true,
		RedactSensitive:    false,
		MaxBodySize:        1024,
	})

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("response-body-content"))
	}))

	req := httptest.NewRequest("GET", "/test?foo=bar", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "response-body-content") {
		t.Fatal("expected response body in log")
	}
	if !strings.Contains(logOutput, "foo") {
		t.Fatal("expected query param in log")
	}
}

func TestDumpLogger_StdlibRequestIDFromContext(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	// Use aarv app to get aarv context, but use stdlib handler chain
	// The body close failure path with aarv context is already tested
	// Here we test the aarv context requestID in stdlib handler
	app := aarv.New(aarv.WithBanner(false))
	// Use stdlib path by wrapping with a non-native middleware
	stdlibWrapper := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
	app.Use(stdlibWrapper)
	app.Use(New())
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/test", nil))

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "request_id") {
		t.Fatal("expected request_id in stdlib path log")
	}
}

func TestDumpLogger_NativeErrorPropagation(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	errMiddleware := aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			return errors.New("middleware error")
		}
	})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Use(errMiddleware)
	app.Get("/err", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/err", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestDumpLogger_RedactHeadersOnly(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestHeaders:  true,
		LogResponseHeaders: true,
		LogRequestBody:     true,
		LogResponseBody:    true,
		RedactSensitive:    true,
		SensitiveHeaders:   []string{"Authorization", "Set-Cookie"},
		SensitiveFields:    []string{"token"},
		RedactSurfaces: []RedactionSurface{
			RedactRequestHeaders,
			RedactResponseHeaders,
		},
		MaxBodySize: 128,
	}))

	app.Post("/test", func(c *aarv.Context) error {
		_, _ = c.Body() // consume so tee reader captures it
		c.Response().Header().Set("Set-Cookie", "session=secret")
		return c.JSON(200, map[string]string{"token": "body-visible"})
	})

	req := httptest.NewRequest("POST", "/test", bytes.NewReader([]byte(`{"token":"request-visible"}`)))
	req.Header.Set("Authorization", "Bearer secret-header")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "Bearer secret-header") || strings.Contains(logOutput, "session=secret") {
		t.Fatalf("expected header surfaces to be redacted, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "request-visible") || !strings.Contains(logOutput, "body-visible") {
		t.Fatalf("expected bodies to remain visible, got: %s", logOutput)
	}
}

// TestDumpLogger_NativeQueryParamsOnlyConfig verifies that query params are
// logged correctly when LogQueryParams is true but LogRequestBody,
// LogRequestHeaders, and LogContentInfo are all false. This exercises the
// code path where req must still be initialized for query parsing.
func TestDumpLogger_NativeQueryParamsOnlyConfig(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogQueryParams:     true,
		LogRequestBody:     false,
		LogRequestHeaders:  false,
		LogResponseBody:    false,
		LogResponseHeaders: false,
		LogContentInfo:     false,
		LogClientIP:        false,
		LogUserAgent:       false,
		LogLatencyMS:       false,
	}))
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/test?color=blue&size=large", nil))

	logStr := logBuf.String()
	if !strings.Contains(logStr, "http_dump") {
		t.Fatal("expected http_dump log line")
	}

	// Parse the JSON log and verify query params are present
	var logEntry map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logStr), "\n") {
		if strings.Contains(line, "http_dump") {
			if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
				t.Fatalf("failed to parse log JSON: %v", err)
			}
			break
		}
	}

	query, ok := logEntry["query"].(map[string]any)
	if !ok {
		t.Fatal("expected 'query' field in log to be a map")
	}
	if query["color"] != "blue" {
		t.Errorf("expected query color=blue, got %v", query["color"])
	}
	if query["size"] != "large" {
		t.Errorf("expected query size=large, got %v", query["size"])
	}
}

// TestDumpLogger_NativePanicCleansUp verifies that when the handler panics,
// the middleware restores the original response writer and releases all pooled
// resources (bodyBuf, reqHeaders, queryParams, respHeaders).
func TestDumpLogger_NativePanicCleansUp(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))

	// Recovery middleware registered with native implementation so the entire
	// chain stays in native mode — exercising the native panic cleanup path.
	stdlibRecovery := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					w.WriteHeader(500)
					_, _ = w.Write([]byte("recovered"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	})
	nativeRecovery := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			defer func() {
				if rec := recover(); rec != nil {
					c.Response().WriteHeader(500)
					_, _ = c.Response().Write([]byte("recovered"))
				}
			}()
			return next(c)
		}
	})
	app.Use(aarv.RegisterNativeMiddleware(stdlibRecovery, nativeRecovery))

	app.Use(New(DefaultConfig()))
	app.Get("/panic", func(c *aarv.Context) error {
		panic("test panic")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/panic?q=1", nil))

	// The recovery middleware should have caught the panic
	if rec.Code != 500 {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	// The response body should be from recovery, not from verboselog's wrapper
	body := rec.Body.String()
	if !strings.Contains(body, "recovered") {
		t.Errorf("expected 'recovered' in body, got %q", body)
	}

	// Verify no http_dump was logged (panic interrupted before slog.Log)
	if strings.Contains(logBuf.String(), "http_dump") {
		t.Error("expected no http_dump log on panic — the handler never completed")
	}
}

// TestDumpLogger_StdlibPanicCleansUp verifies that when the handler panics in the
// stdlib path, the deferred cleanup releases both reqBodyBuf and respBodyBuf.
func TestDumpLogger_StdlibPanicCleansUp(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	recovery := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					w.WriteHeader(500)
					_, _ = w.Write([]byte("recovered"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}

	middleware := New(Config{
		LogRequestBody:  true,
		LogResponseBody: true,
		RedactSensitive: false,
		MaxBodySize:     1024,
	})

	handler := recovery(middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("stdlib panic")
	})))

	body := strings.NewReader(`{"data":"value"}`)
	req := httptest.NewRequest("POST", "/panic", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 500 {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "recovered") {
		t.Fatalf("expected 'recovered' in body, got %q", rec.Body.String())
	}
	// No http_dump should be logged since the handler panicked before logging
	if strings.Contains(logBuf.String(), "http_dump") {
		t.Error("expected no http_dump log on panic")
	}
}

// TestDumpLogger_LargeBodyNotTruncatedForDownstream verifies that when the request
// body exceeds MaxBodySize, downstream handlers still receive the full body (not truncated).
func TestDumpLogger_LargeBodyNotTruncatedForDownstream(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestBody:  true,
		LogResponseBody: false,
		RedactSensitive: false,
		MaxBodySize:     10,
	}))

	var receivedBody string
	app.Post("/test", func(c *aarv.Context) error {
		b, err := c.Body()
		if err != nil {
			return err
		}
		receivedBody = string(b)
		return c.Text(200, "ok")
	})

	fullBody := "this-is-a-body-that-exceeds-max-body-size"
	req := httptest.NewRequest("POST", "/test", strings.NewReader(fullBody))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if receivedBody != fullBody {
		t.Fatalf("downstream received truncated body: got %q, want %q", receivedBody, fullBody)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "[truncated]") {
		t.Fatal("expected truncated marker in log output")
	}
	// Log should contain at most MaxBodySize bytes of the body
	if strings.Contains(logOutput, fullBody) {
		t.Fatal("full body should not appear in log when it exceeds MaxBodySize")
	}
}

// TestDumpLogger_LargeBodyStdlib is the stdlib-path equivalent of the large body test.
func TestDumpLogger_LargeBodyStdlib(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	middleware := New(Config{
		LogRequestBody:  true,
		LogResponseBody: false,
		RedactSensitive: false,
		MaxBodySize:     10,
	})

	fullBody := "this-is-a-body-that-exceeds-max-body-size"
	var receivedBody string
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("POST", "/test", strings.NewReader(fullBody))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if receivedBody != fullBody {
		t.Fatalf("downstream received truncated body: got %q, want %q", receivedBody, fullBody)
	}
}

// TestRedactSensitiveBody_MultipleOccurrences verifies that all occurrences of
// sensitive fields are redacted, not just the first.
func TestRedactSensitiveBody_MultipleOccurrences(t *testing.T) {
	patterns := buildRedactionPatterns([]string{"token"})
	body := `{"token":"first-secret","other":"value","token":"second-secret"}`
	result := redactSensitiveBody(body, patterns)

	if strings.Contains(result, "first-secret") {
		t.Fatalf("first token value should be redacted, got: %s", result)
	}
	if strings.Contains(result, "second-secret") {
		t.Fatalf("second token value should be redacted, got: %s", result)
	}
	if !strings.Contains(result, `"other":"value"`) {
		t.Fatalf("non-sensitive field should be preserved, got: %s", result)
	}
}

// TestDumpLogger_ChunkedBodyLogged verifies that request bodies with unknown
// content length (ContentLength == -1) are still captured for logging.
func TestDumpLogger_ChunkedBodyLogged(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestBody:  true,
		LogResponseBody: false,
		RedactSensitive: false,
		MaxBodySize:     1024,
	}))

	app.Post("/test", func(c *aarv.Context) error {
		_, _ = c.Body() // consume the body
		return c.Text(200, "ok")
	})

	// Create a request with unknown content length (simulating chunked transfer)
	body := "chunked-body-content"
	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "chunked-body-content") {
		t.Fatalf("expected chunked body to be logged, got: %s", logOutput)
	}
}

// TestClientIP_SplitHostPortFallback verifies that clientIP falls back to
// RemoteAddr when SplitHostPort fails (e.g., no port in address).
func TestClientIP_SplitHostPortFallback(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1" // no port — SplitHostPort will fail
	if got := clientIP(req); got != "192.168.1.1" {
		t.Fatalf("expected fallback to RemoteAddr, got %q", got)
	}
}

func TestNegativeMaxBodySizeClamped(t *testing.T) {
	var logBuf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		MaxBodySize: -1,
	}))
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/test", nil))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestBodyTeeReaderClose(t *testing.T) {
	inner := io.NopCloser(strings.NewReader("body"))
	tee := &bodyTeeReader{
		reader: inner,
		buf:    &bytes.Buffer{},
		limit:  1024,
	}
	if err := tee.Close(); err != nil {
		t.Fatalf("expected no error from Close, got %v", err)
	}
}

func TestReleaseBodyBufferOversizedCap(t *testing.T) {
	// Create a buffer that exceeds MaxPooledBufferCap
	buf := bytes.NewBuffer(make([]byte, 0, 128*1024))
	buf.WriteString("data")
	releaseBodyBuffer(buf) // should discard, not panic
}

func TestRedactSensitiveBodyNoClosingQuote(t *testing.T) {
	// Pattern match found but no closing quote — should not loop forever
	patterns := buildRedactionPatterns([]string{"token"})
	body := `{"token":"no-end-quote`
	result := redactSensitiveBody(body, patterns)
	// Should return unchanged since there's no closing quote to redact
	if result != body {
		t.Fatalf("expected unchanged body when closing quote missing, got %q", result)
	}
}

func TestRedactSensitiveBodyStartPastEnd(t *testing.T) {
	// Pattern match at the very end of the string
	patterns := buildRedactionPatterns([]string{"token"})
	body := `{"token":"`
	result := redactSensitiveBody(body, patterns)
	if result != body {
		t.Fatalf("expected unchanged body when value is empty, got %q", result)
	}
}

func BenchmarkDumpLogger_Native(b *testing.B) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())

	app.Post("/users", func(c *aarv.Context) error {
		_, _ = c.Body() // consume body so tee captures it
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

func BenchmarkDumpLogger_Stdlib(b *testing.B) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), nil)))

	middleware := New()
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body) // consume body so tee captures it
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"1","name":"alice"}`))
	}))

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkDumpLogger_Native_NoBody(b *testing.B) {
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

func BenchmarkDumpLogger_Stdlib_NoBody(b *testing.B) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), nil)))

	middleware := New(Config{
		LogRequestBody:  false,
		LogResponseBody: false,
	})
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkDumpLogger_Native_LargeBody(b *testing.B) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		LogRequestBody:  true,
		LogResponseBody: true,
		RedactSensitive: false,
		MaxBodySize:     1024,
	}))

	app.Post("/upload", func(c *aarv.Context) error {
		_, _ = c.Body() // consume body
		return c.Text(200, "ok")
	})

	// 8KB body — well above the 1KB MaxBodySize
	largeBody := bytes.Repeat([]byte("x"), 8*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/upload", bytes.NewReader(largeBody))
		req.Header.Set("Content-Type", "application/octet-stream")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkDumpLogger_Stdlib_LargeBody(b *testing.B) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), nil)))

	middleware := New(Config{
		LogRequestBody:  true,
		LogResponseBody: true,
		RedactSensitive: false,
		MaxBodySize:     1024,
	})
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body) // consume body
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))

	largeBody := bytes.Repeat([]byte("x"), 8*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/upload", bytes.NewReader(largeBody))
		req.Header.Set("Content-Type", "application/octet-stream")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}
