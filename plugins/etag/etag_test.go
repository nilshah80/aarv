package etag

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	if DefaultConfig().Weak {
		t.Fatal("expected strong etag by default")
	}
}

func TestCaptureWriterAndHelpers(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := &captureWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

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
