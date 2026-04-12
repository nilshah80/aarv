package etag

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestDefaultConfig(t *testing.T) {
	if DefaultConfig().Weak {
		t.Fatal("expected strong etag by default")
	}
}

func TestCaptureWriterAndHelpers(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := acquireCaptureWriter(rec)
	defer releaseCaptureWriter(cw)

	cw.WriteHeader(http.StatusCreated)
	_, err := cw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if got := cw.body.String(); got != "hello" {
		t.Fatalf("expected captured body, got %q", got)
	}
	if cw.Unwrap() != rec {
		t.Fatal("unwrap should return original writer")
	}
	releaseCaptureWriter(nil)

	if !matchETag("*", `"abc"`) {
		t.Fatal("wildcard should match")
	}
	if !matchETag(` W/"abc" , "def" `, `"abc"`) {
		t.Fatal("expected weak/strong comparison to match")
	}
	if !matchETag(` , W/"abc"`, `"abc"`) {
		t.Fatal("expected parser to skip empty candidate")
	}
	if matchETag(`"zzz"`, `"abc"`) {
		t.Fatal("did not expect mismatched etags to match")
	}
	if got := stripWeakPrefix(`W/"abc"`); got != `"abc"` {
		t.Fatalf("unexpected stripped etag %q", got)
	}
	if got := indexOf("abc,def", ','); got != 3 {
		t.Fatalf("expected index 3, got %d", got)
	}
	if got := indexOf("abcdef", ','); got != -1 {
		t.Fatalf("expected missing index -1, got %d", got)
	}
	if got := trimSpaces(" \t value \t "); got != "value" {
		t.Fatalf("unexpected trimmed value %q", got)
	}
}

func TestNewPassThroughAndNoETagCases(t *testing.T) {
	nonGetCalled := false
	nonGet := New()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonGetCalled = true
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("no-etag"))
	}))
	rec := httptest.NewRecorder()
	nonGet.ServeHTTP(rec, httptest.NewRequest("POST", "/", nil))
	if !nonGetCalled || rec.Header().Get("ETag") != "" {
		t.Fatalf("expected non-get pass-through without etag, headers=%v", rec.Header())
	}

	noBody := New()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec = httptest.NewRecorder()
	noBody.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusNoContent || rec.Header().Get("ETag") != "" {
		t.Fatalf("expected 204 without etag, got status=%d headers=%v", rec.Code, rec.Header())
	}

	errorResp := New()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("missing"))
	}))
	rec = httptest.NewRecorder()
	errorResp.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusNotFound || rec.Body.String() != "missing" || rec.Header().Get("ETag") != "" {
		t.Fatalf("expected passthrough error response, got status=%d body=%q headers=%v", rec.Code, rec.Body.String(), rec.Header())
	}
}

func TestNewSetsAndMatchesETag(t *testing.T) {
	handler := New()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	etag := rec.Header().Get("ETag")
	if !strings.HasPrefix(etag, "\"") {
		t.Fatalf("expected strong etag, got %q", etag)
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("If-None-Match", "W/"+etag)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", rec.Code)
	}

	weak := New(Config{Weak: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("weak"))
	}))
	rec = httptest.NewRecorder()
	weak.ServeHTTP(rec, httptest.NewRequest("HEAD", "/", nil))
	if got := rec.Header().Get("ETag"); !strings.HasPrefix(got, `W/"`) {
		t.Fatalf("expected weak etag, got %q", got)
	}
}

func TestNewNativeMiddlewarePath(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "hello-native")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))
	etag := rec.Header().Get("ETag")
	if etag == "" || rec.Body.String() != "hello-native" {
		t.Fatalf("expected native etag response, headers=%v body=%q", rec.Header(), rec.Body.String())
	}
}

func TestNewNativeNonGetPassThrough(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Post("/test", func(c *aarv.Context) error {
		return c.Text(http.StatusAccepted, "posted")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/test", nil))
	if rec.Header().Get("ETag") != "" {
		t.Fatal("expected no etag for POST")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
}

func TestNewNativeNon2xxAndEmptyBody(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Get("/notfound", func(c *aarv.Context) error {
		return c.Text(http.StatusNotFound, "missing")
	})
	app.Get("/nocontent", func(c *aarv.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/notfound", nil))
	if rec.Header().Get("ETag") != "" {
		t.Fatal("expected no etag for 404")
	}
	if rec.Code != http.StatusNotFound || rec.Body.String() != "missing" {
		t.Fatalf("expected 404 body, got status=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nocontent", nil))
	if rec.Header().Get("ETag") != "" {
		t.Fatal("expected no etag for empty body")
	}
}

func TestNewNativeWeakETag(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Weak: true}))
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "weak-body")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))
	etag := rec.Header().Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("expected weak etag, got %q", etag)
	}
}

func TestNewNativeIfNoneMatch(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New())
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "cached-body")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))
	etag := rec.Header().Get("ETag")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", rec.Code)
	}
}

func TestNewCustomHashFunction(t *testing.T) {
	// Custom hash that always returns 0xDEADBEEF
	customHash := func(data []byte) uint32 { return 0xDEADBEEF }

	handler := New(Config{Hash: customHash})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("custom-hash-body"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	etag := rec.Header().Get("ETag")
	if etag != `"deadbeef"` {
		t.Fatalf("expected etag from custom hash, got %q", etag)
	}
}

func TestNewCustomHashFunctionWeakStdlibPath(t *testing.T) {
	customHash := func(data []byte) uint32 { return 0xCAFEBABE }

	handler := New(Config{Hash: customHash, Weak: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("weak-stdlib"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	etag := rec.Header().Get("ETag")
	if etag != `W/"cafebabe"` {
		t.Fatalf("expected weak etag from custom hash in stdlib path, got %q", etag)
	}
}

func TestNewNativeCustomHashFunction(t *testing.T) {
	customHash := func(data []byte) uint32 { return 0x12345678 }

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{Hash: customHash, Weak: true}))
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "native-custom-hash")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

	etag := rec.Header().Get("ETag")
	if etag != `W/"12345678"` {
		t.Fatalf("expected weak etag from custom hash, got %q", etag)
	}
}

func TestNewNativeErrorPropagation(t *testing.T) {
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
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/err", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestReleaseLargeCaptureBuffer(t *testing.T) {
	cw := &captureWriter{
		ResponseWriter: httptest.NewRecorder(),
	}
	cw.body.Grow(128 << 10)
	cw.body.Write(make([]byte, 128<<10))
	releaseCaptureWriter(cw)
}
