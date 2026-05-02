package openapiui

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func newApp(opts ...aarv.Option) *aarv.App {
	base := []aarv.Option{
		aarv.WithBanner(false),
		aarv.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	return aarv.New(append(base, opts...)...)
}

// ---- Mount + nil-arg --------------------------------------------------------

func TestMountRejectsNilApp(t *testing.T) {
	if err := Mount(nil, Config{}); !errors.Is(err, ErrNilApp) {
		t.Fatalf("expected ErrNilApp, got %v", err)
	}
}

func TestMountRegistersDefaultPaths(t *testing.T) {
	app := newApp()
	if err := Mount(app, Config{}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	for _, path := range []string{DefaultSwaggerPath, DefaultReDocPath} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("%s: Content-Type %q", path, ct)
		}
	}
}

func TestMountSkipsViewersWithDashSentinel(t *testing.T) {
	t.Run("swagger off", func(t *testing.T) {
		app := newApp()
		if err := Mount(app, Config{SwaggerPath: "-"}); err != nil {
			t.Fatalf("Mount: %v", err)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, DefaultSwaggerPath, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for skipped Swagger, got %d", rec.Code)
		}
		// ReDoc should still be mounted.
		rec2 := httptest.NewRecorder()
		app.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, DefaultReDocPath, nil))
		if rec2.Code != http.StatusOK {
			t.Fatalf("ReDoc default: %d", rec2.Code)
		}
	})
	t.Run("redoc off", func(t *testing.T) {
		app := newApp()
		if err := Mount(app, Config{ReDocPath: "-"}); err != nil {
			t.Fatalf("Mount: %v", err)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, DefaultReDocPath, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for skipped ReDoc, got %d", rec.Code)
		}
	})
}

func TestMountCustomPathsAndSpecURL(t *testing.T) {
	app := newApp()
	cfg := Config{
		SwaggerPath: "/api-docs/swagger",
		ReDocPath:   "/api-docs/redoc",
		SpecURL:     "/v1/openapi.json",
		Title:       "My API",
	}
	if err := Mount(app, cfg); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, cfg.SwaggerPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("custom Swagger path: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, cfg.SpecURL) {
		t.Errorf("Swagger HTML missing spec URL: body=%s", body)
	}
	if !strings.Contains(body, "My API") {
		t.Errorf("Swagger HTML missing title: body=%s", body)
	}
	if !strings.Contains(body, cfg.SwaggerPath+"/static/swagger-ui.css") {
		t.Errorf("Swagger HTML missing CSS link to custom static base: body=%s", body)
	}
}

// ---- Static asset serving ---------------------------------------------------

func TestStaticAssetRoutes(t *testing.T) {
	app := newApp()
	if err := Mount(app, Config{}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Stable substrings drawn from the real upstream bundles pinned in
	// ASSETS.md. Update these alongside any asset version bump.
	cases := []struct {
		path        string
		wantSubstr  string
		contentType string // optional; empty = don't check
	}{
		{DefaultSwaggerPath + "/static/swagger-ui.css", ".swagger-ui", "text/css"},
		{DefaultSwaggerPath + "/static/swagger-ui-bundle.js", "SwaggerUIBundle", ""},
		{DefaultSwaggerPath + "/static/swagger-initializer.js", "data-spec-url", ""},
		{DefaultReDocPath + "/static/redoc.standalone.js", "redoc", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.wantSubstr) {
				t.Errorf("body missing %q", tc.wantSubstr)
			}
			if tc.contentType != "" && !strings.Contains(rec.Header().Get("Content-Type"), tc.contentType) {
				t.Errorf("Content-Type %q does not contain %q", rec.Header().Get("Content-Type"), tc.contentType)
			}
		})
	}
}

func TestStaticAssetUnknownPath404(t *testing.T) {
	app := newApp()
	if err := Mount(app, Config{}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, DefaultSwaggerPath+"/static/does-not-exist.css", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing asset, got %d", rec.Code)
	}
}

// ---- Standalone handlers ----------------------------------------------------

func TestSwaggerHandlerStandalone(t *testing.T) {
	h := SwaggerHandler("/spec.yaml", "Standalone API", "/custom/static")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/whatever", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type: %q", rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, want := range []string{"/spec.yaml", "Standalone API", "/custom/static/swagger-ui.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestReDocHandlerStandalone(t *testing.T) {
	h := ReDocHandler("/spec.yaml", "Standalone API", "/custom/static")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/whatever", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"/spec.yaml", "Standalone API", "/custom/static/redoc.standalone.js", "<redoc"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestStaticFSAccessors(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"swagger": SwaggerStaticFS(),
		"redoc":   ReDocStaticFS(),
	} {
		t.Run(name, func(t *testing.T) {
			if fsys == nil {
				t.Fatal("nil FS")
			}
			f, err := fsys.Open("LICENSE")
			if err != nil {
				t.Fatalf("open LICENSE: %v", err)
			}
			_ = f.Close()
		})
	}
}

// ---- HTML escaping ----------------------------------------------------------

func TestHTMLEscapeNeutralizesHazardousChars(t *testing.T) {
	got := htmlEscape(`</script><img src=x onerror=alert(1)>`)
	for _, banned := range []string{"</script>", "<img"} {
		if strings.Contains(got, banned) {
			t.Errorf("escape did not neutralize %q: %q", banned, got)
		}
	}
	for _, want := range []string{"&lt;", "&gt;"} {
		if !strings.Contains(got, want) {
			t.Errorf("escape missing %q: %q", want, got)
		}
	}
}

func TestMountEscapesUserSuppliedTitle(t *testing.T) {
	app := newApp()
	if err := Mount(app, Config{Title: `<script>x</script>`}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, DefaultSwaggerPath, nil))
	if strings.Contains(rec.Body.String(), "<script>x</script>") {
		t.Fatalf("user-supplied title was not HTML-escaped: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "&lt;script&gt;") {
		t.Fatalf("expected escaped title in body: %s", rec.Body.String())
	}
}

// ---- License files ----------------------------------------------------------

func TestLicenseFilesEmbedded(t *testing.T) {
	cases := []struct {
		name string
		fsys fs.FS
	}{
		{"swagger-ui", SwaggerStaticFS()},
		{"redoc", ReDocStaticFS()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := fs.ReadFile(tc.fsys, "LICENSE")
			if err != nil {
				t.Fatalf("LICENSE missing: %v", err)
			}
			if len(data) == 0 {
				t.Fatal("LICENSE is empty — assets must keep their license text")
			}
		})
	}
}

// ---- Defaults ---------------------------------------------------------------

func TestApplyDefaultsFillsAllFields(t *testing.T) {
	cfg := applyDefaults(Config{})
	if cfg.SpecURL != DefaultSpecURL ||
		cfg.Title != DefaultTitle ||
		cfg.SwaggerPath != DefaultSwaggerPath ||
		cfg.ReDocPath != DefaultReDocPath {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestApplyDefaultsHonorsExplicitValues(t *testing.T) {
	in := Config{SpecURL: "/x", Title: "Y", SwaggerPath: "/s", ReDocPath: "/r"}
	cfg := applyDefaults(in)
	if cfg != in {
		t.Errorf("explicit values mutated: in=%+v out=%+v", in, cfg)
	}
}
