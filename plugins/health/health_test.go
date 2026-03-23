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
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
