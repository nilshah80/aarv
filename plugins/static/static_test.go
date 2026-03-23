package static

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestDefaultConfig(t *testing.T) {
	if got := DefaultConfig().Index; got != "index.html" {
		t.Fatalf("expected index.html default, got %q", got)
	}
}

func TestNewPanicsWithoutRoot(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty root")
		}
	}()
	_ = New(Config{})
}

func TestNewPanicsWhenAbsolutePathResolutionFails(t *testing.T) {
	oldAbs := filepathAbs
	filepathAbs = func(string) (string, error) {
		return "", os.ErrInvalid
	}
	defer func() {
		filepathAbs = oldAbs
	}()

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when filepath.Abs fails")
		}
	}()

	_ = New(Config{Root: "relative"})
}

func TestNewServesFilesAndPassesThroughUnsupportedMethods(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	nextCalled := 0
	handler := New(Config{
		Root:   root,
		MaxAge: 60,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/file.txt", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("expected static file response, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("unexpected cache-control %q", got)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/file.txt", nil))
	if rec.Code != http.StatusTeapot || nextCalled != 1 {
		t.Fatalf("expected next handler for unsupported method, status=%d next=%d", rec.Code, nextCalled)
	}
}

func TestNewHandlesPrefixSPAAndPassThroughCases(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("spa-index"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	nextCalled := 0
	handler := New(Config{
		Root:   root,
		Prefix: "/static",
		SPA:    true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/other", nil))
	if rec.Code != http.StatusTeapot || nextCalled != 1 {
		t.Fatalf("expected next handler for prefix mismatch, status=%d next=%d", rec.Code, nextCalled)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/static/missing", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "spa-index") {
		t.Fatalf("expected SPA fallback, got status=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/static", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected current file server behavior for prefix root, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestNewHandlesDirectoryBrowseAndNoIndexPassThrough(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docs, "guide.txt"), []byte("guide"), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}

	browse := New(Config{
		Root:   root,
		Browse: true,
		MaxAge: 30,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	browse.ServeHTTP(rec, httptest.NewRequest("GET", "/docs/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "guide.txt") {
		t.Fatalf("expected browse listing, got status=%d body=%q", rec.Code, rec.Body.String())
	}

	noBrowse := New(Config{Root: root})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec = httptest.NewRecorder()
	noBrowse.ServeHTTP(rec, httptest.NewRequest("GET", "/docs/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected pass-through without browse or spa, got %d", rec.Code)
	}

	spaDir := New(Config{Root: root, SPA: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("dir-spa"), 0o644); err != nil {
		t.Fatalf("write root index: %v", err)
	}
	rec = httptest.NewRecorder()
	spaDir.ServeHTTP(rec, httptest.NewRequest("GET", "/docs/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "dir-spa") {
		t.Fatalf("expected SPA fallback for directory, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestServeIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	rec := httptest.NewRecorder()
	serveIndex(rec, httptest.NewRequest("GET", "/", nil), root, "index.html", "public, max-age=10")

	if rec.Code != http.StatusOK || rec.Body.String() != "index" {
		t.Fatalf("unexpected response status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=10" {
		t.Fatalf("unexpected cache-control %q", got)
	}
}

func TestNewHandlesMissingFilesWithoutSPAAndPathsWithoutLeadingSlash(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("plain"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	nextCalled := 0
	handler := New(Config{Root: root})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/missing", nil))
	if rec.Code != http.StatusTeapot || nextCalled != 1 {
		t.Fatalf("expected pass-through for missing file, status=%d next=%d", rec.Code, nextCalled)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.URL.Path = "file.txt"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "plain" {
		t.Fatalf("expected file served for path without leading slash, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestNewNativeMiddlewarePath(t *testing.T) {
	noop := func(c *aarv.Context) error { return c.Text(http.StatusTeapot, "next") }
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello-native"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root, MaxAge: 60}))
	app.Get("/file.txt", noop) // register route so native chain fires
	app.Get("/users", noop)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/file.txt", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "hello-native" {
		t.Fatalf("expected native static response, got status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("expected cache-control, got %q", got)
	}
}

func TestNewNativePostPassThrough(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("static"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root}))
	app.Post("/file.txt", func(c *aarv.Context) error {
		return c.Text(http.StatusCreated, "posted")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/file.txt", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected POST pass-through, got %d", rec.Code)
	}
}

func TestNewNativePrefixAndSPA(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("spa-native"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	noop := func(c *aarv.Context) error { return c.Text(http.StatusTeapot, "next") }

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root, Prefix: "/static", SPA: true}))
	app.Get("/other", noop)
	app.Get("/static/missing", noop) // register for native chain

	// Prefix mismatch — pass to next
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected pass-through for prefix mismatch, got %d", rec.Code)
	}

	// SPA fallback for missing file under prefix
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/missing", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "spa-native") {
		t.Fatalf("expected SPA fallback, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestNewNativeDirectoryBrowseAndSPAFallback(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docs, "guide.txt"), []byte("guide"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	noop := func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) }

	// Browse mode
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root, Browse: true, MaxAge: 30}))
	app.Get("/docs/", noop) // register for native chain

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "guide.txt") {
		t.Fatalf("expected browse listing, got status=%d body=%q", rec.Code, rec.Body.String())
	}

	// SPA dir fallback
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("dir-spa-native"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	app2 := aarv.New(aarv.WithBanner(false))
	app2.Use(New(Config{Root: root, SPA: true}))
	app2.Get("/docs/", noop)

	rec = httptest.NewRecorder()
	app2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "dir-spa-native") {
		t.Fatalf("expected SPA dir fallback, got status=%d body=%q", rec.Code, rec.Body.String())
	}

	// No browse, no SPA — pass through
	app3 := aarv.New(aarv.WithBanner(false))
	app3.Use(New(Config{Root: root}))
	app3.Get("/docs/", func(c *aarv.Context) error {
		return c.Text(http.StatusTeapot, "next")
	})

	rec = httptest.NewRecorder()
	app3.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected pass-through for dir without browse/SPA, got %d", rec.Code)
	}
}

func TestNewNativeMissingFileNoSPA(t *testing.T) {
	root := t.TempDir()

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root}))
	app.Get("/missing", func(c *aarv.Context) error {
		return c.Text(http.StatusTeapot, "next")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected pass-through for missing file, got %d", rec.Code)
	}
}

func TestNewNativePrefixExactMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("root-index"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root, Prefix: "/static", SPA: true}))
	noop := func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) }
	app.Get("/static", noop) // register exact prefix path

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static", nil))
	// After TrimPrefix("/static", "/static") → "", which gets "/" prepended
	// The path becomes "/" which is a directory — file server may redirect or serve index
	if rec.Code != http.StatusOK && rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusNotFound {
		t.Fatalf("expected prefix exact match to be handled, got %d", rec.Code)
	}
}

func TestNewNativePathWithoutLeadingSlash(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("native-no-slash"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root}))
	noop := func(c *aarv.Context) error { return c.NoContent(http.StatusNoContent) }
	app.Get("/file.txt", noop)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = "file.txt"
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "native-no-slash" {
		t.Fatalf("expected file without leading slash, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}
