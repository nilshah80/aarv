package secure

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultAndRelaxedConfig(t *testing.T) {
	def := DefaultConfig()
	if def.XFrameOptions != "DENY" || def.HSTSMaxAge == 0 || def.ContentSecurityPolicy == "" {
		t.Fatalf("unexpected default config: %#v", def)
	}

	relaxed := RelaxedConfig()
	if relaxed.XFrameOptions != "SAMEORIGIN" || relaxed.HSTSMaxAge != 0 {
		t.Fatalf("unexpected relaxed config: %#v", relaxed)
	}
}

func TestNewSetsDefaultHeaders(t *testing.T) {
	handler := New()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Header().Get("X-XSS-Protection") != "1; mode=block" {
		t.Fatal("expected xss protection header")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("expected content-type nosniff header")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("expected deny frame options")
	}
	if got := rec.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "includeSubDomains") {
		t.Fatalf("expected hsts includeSubDomains, got %q", got)
	}
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Permissions-Policy") == "" {
		t.Fatal("expected CSP and Permissions-Policy headers")
	}
	if rec.Header().Get("Cross-Origin-Embedder-Policy") != "" {
		t.Fatal("did not expect COEP by default")
	}
}

func TestNewRespectsDisabledHeadersAndCustomHSTS(t *testing.T) {
	handler := New(Config{
		XSSProtection:             "",
		ContentTypeNosniff:        "",
		XFrameOptions:             "SAMEORIGIN",
		HSTSMaxAge:                100,
		HSTSIncludeSubdomains:     false,
		HSTSPreload:               true,
		ContentSecurityPolicy:     "",
		ReferrerPolicy:            "no-referrer",
		PermissionsPolicy:         "interest-cohort=()",
		CrossOriginOpenerPolicy:   "same-origin-allow-popups",
		CrossOriginEmbedderPolicy: "require-corp",
		CrossOriginResourcePolicy: "cross-origin",
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Header().Get("X-XSS-Protection") != "" || rec.Header().Get("X-Content-Type-Options") != "" {
		t.Fatal("expected disabled headers to be omitted")
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=100" {
		t.Fatalf("expected simple hsts header, got %q", got)
	}
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("expected CSP header to be omitted")
	}
	if rec.Header().Get("Cross-Origin-Embedder-Policy") != "require-corp" {
		t.Fatal("expected custom COEP header")
	}
	if rec.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Fatal("expected custom CORP header")
	}
}

func TestNewAddsPreloadWhenAllowed(t *testing.T) {
	handler := New(Config{
		HSTSMaxAge:            200,
		HSTSIncludeSubdomains: true,
		HSTSPreload:           true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=200; includeSubDomains; preload" {
		t.Fatalf("unexpected preload hsts value %q", got)
	}
}
