package health

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HealthPath != "/health" || cfg.ReadyPath != "/ready" || cfg.LivePath != "/live" {
		t.Fatalf("unexpected default config: %#v", cfg)
	}
}

func TestNewServesHealthEndpointsAndPassesThrough(t *testing.T) {
	handler := New(Config{
		HealthPath: "",
		ReadyPath:  "",
		LivePath:   "",
		ReadyCheck: func() bool { return false },
		LiveCheck:  func() bool { return false },
	}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("next"))
	}))

	tests := []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{path: "/health", wantStatus: http.StatusOK, wantBody: `{"status":"ok"}`},
		{path: "/ready", wantStatus: http.StatusServiceUnavailable, wantBody: `{"status":"unavailable"}`},
		{path: "/live", wantStatus: http.StatusServiceUnavailable, wantBody: `{"status":"unavailable"}`},
		{path: "/other", wantStatus: http.StatusTeapot, wantBody: "next"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("expected body to contain %q, got %q", tt.wantBody, rec.Body.String())
			}
			if tt.path != "/other" && rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("expected json content-type, got %q", rec.Header().Get("Content-Type"))
			}
		})
	}
}

func TestNewSupportsCustomPathsAndHealthyChecks(t *testing.T) {
	handler := New(Config{
		HealthPath: "/status",
		ReadyPath:  "/status/ready",
		LivePath:   "/status/live",
		ReadyCheck: func() bool { return true },
		LiveCheck:  func() bool { return true },
	}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	for _, path := range []string{"/status", "/status/ready", "/status/live"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `{"status":"ok"}`) {
			t.Fatalf("expected ok status for %s, got %q", path, rec.Body.String())
		}
	}
}

func TestNewInfoIncludedInResponse(t *testing.T) {
	handler := New(Config{
		Info: map[string]any{
			"version": "1.2.3",
			"commit":  "abc123",
		},
	}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("expected status ok in body, got %q", body)
	}
	if !strings.Contains(body, `"version":"1.2.3"`) {
		t.Fatalf("expected version in body, got %q", body)
	}
	if !strings.Contains(body, `"commit":"abc123"`) {
		t.Fatalf("expected commit in body, got %q", body)
	}
}

func TestNewInfoOnUnavailableStdlibPath(t *testing.T) {
	handler := New(Config{
		Info: map[string]any{
			"version": "3.0.0",
		},
		ReadyCheck: func() bool { return false },
	}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"version":"3.0.0"`) || !strings.Contains(body, `"status":"unavailable"`) {
		t.Fatalf("expected info in unavailable stdlib response, got %q", body)
	}
}

func TestNewInfoStatusCannotBeOverridden(t *testing.T) {
	handler := New(Config{
		Info: map[string]any{
			"status": "lying",
		},
		ReadyCheck: func() bool { return false },
	}).Stdlib(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/ready", nil))
	body := rec.Body.String()
	if strings.Contains(body, `"lying"`) {
		t.Fatalf("Info[\"status\"] should not override computed status, got %q", body)
	}
	if !strings.Contains(body, `"status":"unavailable"`) {
		t.Fatalf("expected computed status, got %q", body)
	}
}

func TestNewNativeInfoIncludedInResponse(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		Info: map[string]any{
			"version": "2.0.0",
		},
		ReadyCheck: func() bool { return false },
	}))
	app.Get("/other", func(c *aarv.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	// Health endpoint with info
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"version":"2.0.0"`) || !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("expected info in health response, got %q", body)
	}

	// Unavailable endpoint also includes info
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	body = rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if !strings.Contains(body, `"version":"2.0.0"`) || !strings.Contains(body, `"status":"unavailable"`) {
		t.Fatalf("expected info in unavailable response, got %q", body)
	}
}

func TestNewNativeMiddlewarePath(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Get("/users", func(c *aarv.Context) error {
		return c.Text(http.StatusTeapot, "next")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `{"status":"ok"}`) {
		t.Fatalf("expected native health response, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestNewNativeAllEndpoints(t *testing.T) {
	noop := func(c *aarv.Context) error { return c.Text(http.StatusTeapot, "next") }

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		ReadyCheck: func() bool { return false },
		LiveCheck:  func() bool { return false },
	}))
	app.Get("/health", noop)
	app.Get("/ready", noop)
	app.Get("/live", noop)
	app.Get("/other", noop)

	tests := []struct {
		path   string
		status int
		body   string
	}{
		{"/health", http.StatusOK, `"ok"`},
		{"/ready", http.StatusServiceUnavailable, `"unavailable"`},
		{"/live", http.StatusServiceUnavailable, `"unavailable"`},
		{"/other", http.StatusTeapot, "next"},
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if rec.Code != tt.status {
			t.Fatalf("%s: expected %d, got %d", tt.path, tt.status, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), tt.body) {
			t.Fatalf("%s: expected body to contain %q, got %q", tt.path, tt.body, rec.Body.String())
		}
	}
}

func TestNewNativeHealthyChecks(t *testing.T) {
	noop := func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) }

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		ReadyCheck: func() bool { return true },
		LiveCheck:  func() bool { return true },
	}))
	app.Get("/health", noop)
	app.Get("/ready", noop)
	app.Get("/live", noop)

	for _, path := range []string{"/health", "/ready", "/live"} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"ok"`) {
			t.Fatalf("%s: expected ok, got %q", path, rec.Body.String())
		}
	}
}
