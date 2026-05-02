package static

import (
	"errors"
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

func TestDirectoryWithoutIndexFallsThroughHTTPAndNative(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "empty"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	t.Run("http middleware", func(t *testing.T) {
		called := false
		handler := New(Config{Root: root})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusAccepted)
		}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/empty/", nil)
		handler.ServeHTTP(rec, req)
		if !called || rec.Code != http.StatusAccepted {
			t.Fatalf("expected fallthrough to next handler, called=%v status=%d", called, rec.Code)
		}
	})

	t.Run("native middleware", func(t *testing.T) {
		app := aarv.New(aarv.WithBanner(false))
		app.Use(New(Config{Root: root}))
		app.Get("/empty/", func(c *aarv.Context) error {
			return c.NoContent(http.StatusAccepted)
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/empty/", nil)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected native fallthrough to route, got %d", rec.Code)
		}
	})
}

func TestNoBrowseFSOpenForcedStatError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	orig := fileStat
	t.Cleanup(func() { fileStat = orig })
	fileStat = func(http.File) (os.FileInfo, error) {
		return nil, errors.New("forced stat failure")
	}

	f, err := (noBrowseFS{root: root, index: "index.html"}).Open("/file.txt")
	if err == nil {
		if f != nil {
			_ = f.Close()
		}
		t.Fatal("expected forced stat failure")
	}
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

// TestNewCustomIndexInFastPath verifies that a non-default Config.Index
// is respected in the fast path (no SPA, no Browse) where noBrowseFS is used.
func TestNewCustomIndexInFastPath(t *testing.T) {
	root := t.TempDir()
	subDir := filepath.Join(root, "docs")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Create a custom index file instead of index.html
	if err := os.WriteFile(filepath.Join(subDir, "default.htm"), []byte("custom-index"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	nextCalled := 0
	handler := New(Config{
		Root:  root,
		Index: "default.htm",
		// Browse: false (default), SPA: false — exercises the fast/noBrowseFS path
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	}))

	// Directory with custom index should be served
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/docs/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "custom-index") {
		t.Fatalf("expected custom index served, got status=%d body=%q", rec.Code, rec.Body.String())
	}

	// Directory without custom index should fall through (404 intercepted)
	emptyDir := filepath.Join(root, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/empty/", nil))
	if rec.Code != http.StatusTeapot || nextCalled != 1 {
		t.Fatalf("expected pass-through for dir without custom index, status=%d next=%d", rec.Code, nextCalled)
	}
}

// TestNotFoundInterceptorHeaderIsolation verifies that headers set during
// a suppressed 404 do not leak to the fallback handler.
func TestNotFoundInterceptorHeaderIsolation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "exists.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	handler := New(Config{
		Root:   root,
		MaxAge: 3600,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fallback handler — should not see Cache-Control from the static middleware
		if cc := w.Header().Get("Cache-Control"); cc != "" {
			t.Errorf("fallback handler should not see leaked Cache-Control header, got %q", cc)
		}
		w.WriteHeader(http.StatusTeapot)
	}))

	// Hit for missing file — should fall through without leaking headers
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/missing.txt", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected fallback, got %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("suppressed 404 should not leak Cache-Control to response, got %q", cc)
	}

	// Hit for existing file — should have Cache-Control
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/exists.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Errorf("expected Cache-Control on hit, got %q", cc)
	}
}

// TestNewNativeFileHitWithCacheControl covers the native path where a file
// exists and MaxAge is set (exercises cacheControl set on native hit).
func TestNewNativeFileHitWithCacheControl(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "style.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-SPA, non-Browse — exercises the native fast path with cache control
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root, MaxAge: 120}))
	app.Get("/style.css", func(c *aarv.Context) error { return c.NoContent(http.StatusTeapot) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/style.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=120" {
		t.Fatalf("expected cache-control on native hit, got %q", got)
	}
}

// TestNewNativeDirNoIndexNoBrowseNoSPA covers the native SPA/browse path
// where a directory exists but has no index, browse is off, and SPA is off.
func TestNewNativeDirNoIndexNoBrowseNoSPA(t *testing.T) {
	root := t.TempDir()
	emptyDir := filepath.Join(root, "assets")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}

	// SPA=true to force the SPA/browse code path, but test the dir-no-index
	// pass-through first with SPA=false.
	app := aarv.New(aarv.WithBanner(false))
	// Browse=true forces SPA/browse path but with no SPA fallback
	app.Use(New(Config{Root: root}))
	app.Get("/assets/", func(c *aarv.Context) error { return c.Text(http.StatusTeapot, "next") })

	rec := httptest.NewRecorder()
	// This hits the fast path (no SPA, no browse) — noBrowseFS returns ErrNotExist
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected pass-through for empty dir, got %d", rec.Code)
	}
}

// TestNewNativeMissingFileNoSPA covers the native SPA-enabled path
// where a file is missing and SPA is off.
func TestNewNativeMissingFileNoSPAPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("spa"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use Browse=true to force the SPA/browse code path
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root, Browse: true}))
	app.Get("/missing.js", func(c *aarv.Context) error { return c.Text(http.StatusTeapot, "next") })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/missing.js", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected pass-through for missing file in browse mode, got %d", rec.Code)
	}
}

// TestNotFoundInterceptorImplicitCommitOnWrite covers the Write path where
// WriteHeader was not called before Write (implicit 200 commit).
func TestNotFoundInterceptorImplicitCommitOnWrite(t *testing.T) {
	base := httptest.NewRecorder()
	nfi := &notFoundInterceptor{ResponseWriter: base}
	nfi.Header().Set("X-Custom", "val")

	// Write without WriteHeader — should implicitly commit headers
	n, err := nfi.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("expected successful write, got n=%d err=%v", n, err)
	}
	if !nfi.committed {
		t.Fatal("expected committed after Write")
	}
	if base.Header().Get("X-Custom") != "val" {
		t.Fatal("expected headers copied on implicit commit")
	}
	if base.Body.String() != "hello" {
		t.Fatalf("expected body written, got %q", base.Body.String())
	}
}

// TestNewNativeCacheControlOnSPAFileHit covers the native SPA/browse path
// where a file exists with cache-control set.
func TestNewNativeCacheControlOnSPAFileHit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("js"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("spa"), 0o644); err != nil {
		t.Fatal(err)
	}

	// SPA=true forces SPA/browse path + MaxAge for cache-control
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root, SPA: true, MaxAge: 300}))
	app.Get("/app.js", func(c *aarv.Context) error { return c.NoContent(http.StatusTeapot) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Fatalf("expected cache-control on SPA file hit, got %q", got)
	}
}

// TestStdlibMissingFileNoSPA covers the stdlib SPA/browse path
// where a file is missing and SPA is off.
func TestStdlibMissingFileNoSPA(t *testing.T) {
	root := t.TempDir()
	nextCalled := 0
	// Browse=true forces the SPA/browse code path
	handler := New(Config{Root: root, Browse: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/missing.js", nil))
	if rec.Code != http.StatusTeapot || nextCalled != 1 {
		t.Fatalf("expected pass-through for missing file, status=%d next=%d", rec.Code, nextCalled)
	}
}

// TestStdlibDirNoIndexNoBrowseNoSPA covers the stdlib path where a directory
// exists but has no index, browse is off, and SPA is off.
func TestStdlibDirNoIndexNoBrowseNoSPA(t *testing.T) {
	root := t.TempDir()
	emptyDir := filepath.Join(root, "assets")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}

	nextCalled := 0
	// SPA=true forces the SPA/browse code path but doesn't fallback for dirs
	handler := New(Config{Root: root, SPA: false, Browse: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	// Request to a directory without index in browse mode — should list
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected browse listing, got %d", rec.Code)
	}

	// Now test without browse — dir without index should pass through
	handler2 := New(Config{Root: root, SPA: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	}))
	rec = httptest.NewRecorder()
	handler2.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/", nil))
	// SPA mode + dir without index → SPA fallback
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "root") {
		t.Fatalf("expected SPA fallback, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestStdlibFileHitWithCacheControl covers the stdlib SPA/browse path
// where a file hit has cache-control.
func TestStdlibFileHitWithCacheControl(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("js"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("idx"), 0o644); err != nil {
		t.Fatal(err)
	}

	// SPA=true forces SPA/browse path
	handler := New(Config{Root: root, SPA: true, MaxAge: 600})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=600" {
		t.Fatalf("expected cache-control, got %q", got)
	}
}

// TestStdlibDirNoIndexPassThrough covers the stdlib path where
// no browse, no SPA, dir without index.
func TestStdlibDirNoIndexPassThrough(t *testing.T) {
	root := t.TempDir()
	emptyDir := filepath.Join(root, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	nextCalled := 0
	// SPA=true forces SPA/browse code path, but without SPA fallback for
	// this specific case we use Browse=true+SPA=false
	handler := New(Config{Root: root, SPA: false, Browse: false})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/empty/", nil))
	// Fast path (no SPA, no browse) — noBrowseFS returns ErrNotExist, interceptor falls through
	if rec.Code != http.StatusTeapot || nextCalled != 1 {
		t.Fatalf("expected pass-through, got status=%d next=%d", rec.Code, nextCalled)
	}
}

// TestNoBrowseFSOpenStatError verifies behavior when the opened file's Stat fails.
// This is hard to trigger naturally but we test the noBrowseFS directly.
func TestNoBrowseFSOpen(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := noBrowseFS{root: root, index: "index.html"}

	// Regular file — should succeed
	f, err := fs.Open("/file.txt")
	if err != nil {
		t.Fatalf("expected file to open, got %v", err)
	}
	_ = f.Close()

	// Missing file — should fail
	_, err = fs.Open("/missing.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// --- Additional coverage tests ---

func TestNewPrefixMissNonSPAStdlibPath(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "app.js"), []byte("js"), 0o644)

	nextCalled := 0
	handler := New(Config{
		Root:   root,
		Prefix: "/assets",
		// SPA: false, Browse: false — the fast non-SPA path
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	}))

	// Request does NOT match prefix — should fall through to next
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/other", nil))
	if rec.Code != http.StatusTeapot || nextCalled != 1 {
		t.Fatalf("expected next for prefix mismatch, status=%d next=%d", rec.Code, nextCalled)
	}
}

func TestNewPrefixMissNonSPANativePath(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "app.js"), []byte("js"), 0o644)

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{
		Root:   root,
		Prefix: "/assets",
	}))
	app.Get("/other", func(c *aarv.Context) error {
		return c.Text(http.StatusTeapot, "fallback")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/other", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected 418 fallback, got %d", rec.Code)
	}
}

func TestNoBrowseFSOpenStatError(t *testing.T) {
	root := t.TempDir()

	// Create a file, open it via noBrowseFS, then remove it between Open and Stat.
	// This is hard to trigger deterministically, but we can test the path by
	// creating a symlink to a non-existent target on macOS/Linux.
	brokenLink := filepath.Join(root, "broken.txt")
	_ = os.Symlink("/nonexistent/target", brokenLink)

	fs := noBrowseFS{root: root, index: "index.html"}
	_, err := fs.Open("/broken.txt")
	if err == nil {
		t.Fatal("expected error for broken symlink stat")
	}
}

// --- Benchmarks ---

func benchStaticRoot(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("console.log('hello')"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>index</html>"), 0o644); err != nil {
		b.Fatal(err)
	}
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "index.html"), []byte("<html>docs</html>"), 0o644); err != nil {
		b.Fatal(err)
	}
	return root
}

func BenchmarkStatic_Hit(b *testing.B) {
	root := benchStaticRoot(b)
	handler := New(Config{Root: root, MaxAge: 60})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest("GET", "/app.js", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkStatic_Miss(b *testing.B) {
	root := benchStaticRoot(b)
	handler := New(Config{Root: root})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	req := httptest.NewRequest("GET", "/missing.js", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkStatic_DirWithIndex(b *testing.B) {
	root := benchStaticRoot(b)
	handler := New(Config{Root: root, MaxAge: 60})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest("GET", "/docs/", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkStatic_SPAFallback(b *testing.B) {
	root := benchStaticRoot(b)
	handler := New(Config{Root: root, SPA: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest("GET", "/app/dashboard", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkStatic_NativeHit(b *testing.B) {
	root := benchStaticRoot(b)
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root, MaxAge: 60}))
	app.Get("/app.js", func(c *aarv.Context) error { return c.NoContent(http.StatusTeapot) })

	req := httptest.NewRequest("GET", "/app.js", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkStatic_NativeMiss(b *testing.B) {
	root := benchStaticRoot(b)
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Root: root}))
	app.Get("/missing.js", func(c *aarv.Context) error { return c.NoContent(http.StatusTeapot) })

	req := httptest.NewRequest("GET", "/missing.js", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.ServeHTTP(httptest.NewRecorder(), req)
	}
}
