package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.AllowOrigins) != 1 || cfg.AllowOrigins[0] != "*" {
		t.Fatalf("unexpected default origins: %#v", cfg.AllowOrigins)
	}
	if len(cfg.AllowMethods) == 0 || len(cfg.AllowHeaders) == 0 {
		t.Fatal("expected default methods and headers")
	}
}

func TestNewPassesThroughWithoutOriginAndDisallowedOrigin(t *testing.T) {
	nextCalled := 0
	handler := New(Config{
		AllowOrigins: []string{"https://allowed.example"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(204)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if nextCalled != 1 {
		t.Fatalf("expected next handler for non-cors request, got %d", nextCalled)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("did not expect CORS headers without Origin")
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://blocked.example")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if nextCalled != 2 {
		t.Fatalf("expected next handler for blocked origin, got %d", nextCalled)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("did not expect CORS headers for blocked origin")
	}
}

func TestNewPanicsForWildcardWithCredentials(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()

	_ = New(Config{
		AllowOrigins:     []string{"*"},
		AllowCredentials: true,
	})
}

func TestNewHandlesAllowedOriginsAndPreflight(t *testing.T) {
	handler := New(Config{
		AllowOrigins:     []string{"https://app.example"},
		AllowMethods:     nil,
		AllowHeaders:     nil,
		ExposeHeaders:    []string{"X-Trace-ID"},
		AllowCredentials: true,
		MaxAge:           60,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
	}))

	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("expected echoed origin, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected credentials header, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected allow methods header")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("expected allow headers header")
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "X-Trace-ID" {
		t.Fatalf("expected expose headers, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "60" {
		t.Fatalf("expected max-age 60, got %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("expected Vary Origin, got %q", got)
	}
}

func TestNewAllowsWildcardAndDynamicOriginFunction(t *testing.T) {
	wildcard := New()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://any.example")
	rec := httptest.NewRecorder()
	wildcard.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard allow origin, got %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Fatalf("did not expect vary header for wildcard, got %q", got)
	}

	dynamic := New(Config{
		AllowOrigins: []string{"https://ignored.example"},
		AllowOriginFunc: func(origin string) bool {
			return origin == "https://dynamic.example"
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://dynamic.example")
	rec = httptest.NewRecorder()
	dynamic.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dynamic.example" {
		t.Fatalf("expected dynamic origin echo, got %q", got)
	}
}

func TestNewNativeMiddlewarePathFull(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		AllowOrigins:     []string{"https://app.example"},
		ExposeHeaders:    []string{"X-Trace-ID"},
		AllowCredentials: true,
		MaxAge:           60,
	}))
	app.Get("/api", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	// No origin — pass through
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/api", nil))
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("expected no CORS headers without origin")
	}

	// Blocked origin
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Origin", "https://blocked.example")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("expected no CORS headers for blocked origin")
	}

	// Allowed origin — normal GET
	req = httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Origin", "https://app.example")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("expected echoed origin, got %q", got)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("expected credentials header")
	}
	if rec.Header().Get("Access-Control-Expose-Headers") != "X-Trace-ID" {
		t.Fatal("expected expose headers")
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Fatal("expected Vary Origin")
	}

	// Preflight OPTIONS — need an OPTIONS route for native chain
	app2 := aarv.New(aarv.WithBanner(false))
	app2.Use(New(Config{
		AllowOrigins:     []string{"https://app.example"},
		AllowCredentials: true,
		MaxAge:           60,
	}))
	app2.Options("/api", func(c *aarv.Context) error {
		return c.NoContent(http.StatusOK)
	})
	req = httptest.NewRequest("OPTIONS", "/api", nil)
	req.Header.Set("Origin", "https://app.example")
	rec = httptest.NewRecorder()
	app2.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("expected allow methods header")
	}
	if rec.Header().Get("Access-Control-Max-Age") != "60" {
		t.Fatalf("expected max-age 60, got %q", rec.Header().Get("Access-Control-Max-Age"))
	}
}

func TestNewNativeWildcardOrigin(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New()) // wildcard default
	app.Get("/api", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Origin", "https://any.example")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard origin, got %q", got)
	}
}
