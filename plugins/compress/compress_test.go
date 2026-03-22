package compress

import (
	"compress/flate"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeCompressor struct {
	writeErr error
}

func (c *fakeCompressor) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(p), nil
}

func (c *fakeCompressor) Close() error { return nil }

func (c *fakeCompressor) Reset(io.Writer) {}

type failingResponseWriter struct {
	header http.Header
	err    error
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingResponseWriter) WriteHeader(int) {}

func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MinSize != 1024 || !cfg.PreferGzip || len(cfg.ExcludedTypes) == 0 {
		t.Fatalf("unexpected default config: %#v", cfg)
	}
}

func TestNewGzipCompressionAndWriterHelpers(t *testing.T) {
	body := strings.Repeat("a", 1500)
	handler := New(Config{
		Level:   999,
		MinSize: 0,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(body[:1000]))
		_, _ = w.Write([]byte(body[1000:]))
		_, _ = w.Write([]byte("tail"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", rec.Header().Get("Content-Encoding"))
	}

	gr, err := gzip.NewReader(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("gzip reader failed: %v", err)
	}
	defer func() { _ = gr.Close() }()
	decoded, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("gzip decode failed: %v", err)
	}
	if string(decoded) != body+"tail" {
		t.Fatalf("unexpected decoded body length=%d", len(decoded))
	}

	base := httptest.NewRecorder()
	gz, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	grw := acquireGzipResponseWriter(base, gz, &sync.Pool{}, 0, nil)
	if grw.Unwrap() != base {
		t.Fatal("gzip unwrap should return base writer")
	}

	grw = acquireGzipResponseWriter(base, gz, &sync.Pool{}, 0, nil)
	grw.statusCode = http.StatusAccepted
	grw.finish()
	if base.Code != http.StatusAccepted {
		t.Fatalf("expected finish to send status 202, got %d", base.Code)
	}

	base = httptest.NewRecorder()
	grw = acquireGzipResponseWriter(base, &fakeCompressor{}, &sync.Pool{}, 0, nil)
	grw.buf = []byte("plain")
	grw.finish()
	if base.Body.String() != "plain" {
		t.Fatalf("expected buffered plain body, got %q", base.Body.String())
	}
}

func TestNewDeflateCompression(t *testing.T) {
	body := strings.Repeat("b", 40)
	handler := New(Config{
		MinSize:    1,
		PreferGzip: false,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
		_, _ = w.Write([]byte("tail"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "deflate")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "deflate" {
		t.Fatalf("expected deflate encoding, got %q", rec.Header().Get("Content-Encoding"))
	}

	fr := flate.NewReader(strings.NewReader(rec.Body.String()))
	defer func() { _ = fr.Close() }()
	decoded, err := io.ReadAll(fr)
	if err != nil {
		t.Fatalf("deflate decode failed: %v", err)
	}
	if string(decoded) != body+"tail" {
		t.Fatalf("unexpected deflated body %q", string(decoded))
	}

	base := httptest.NewRecorder()
	fw, _ := flate.NewWriter(io.Discard, flate.DefaultCompression)
	drw := acquireDeflateResponseWriter(base, fw, &sync.Pool{}, 0, nil)
	if drw.Unwrap() != base {
		t.Fatal("deflate unwrap should return base writer")
	}

	drw = acquireDeflateResponseWriter(base, fw, &sync.Pool{}, 0, nil)
	drw.statusCode = http.StatusAccepted
	drw.WriteHeader(http.StatusCreated)
	if drw.statusCode != http.StatusCreated {
		t.Fatalf("expected status code 201, got %d", drw.statusCode)
	}
	drw.finish()

	base = httptest.NewRecorder()
	drw = acquireDeflateResponseWriter(base, &fakeCompressor{}, &sync.Pool{}, 0, nil)
	drw.buf = []byte("plain")
	drw.finish()
	if base.Body.String() != "plain" {
		t.Fatalf("expected deflate buffered plain body, got %q", base.Body.String())
	}

	drw = acquireDeflateResponseWriter(httptest.NewRecorder(), &fakeCompressor{}, &sync.Pool{}, 10, nil)
	if n, err := drw.Write([]byte("abc")); err != nil || n != 3 {
		t.Fatalf("expected buffered deflate write, got n=%d err=%v", n, err)
	}
	releaseGzipResponseWriter(nil)
	releaseDeflateResponseWriter(nil)
}

func TestNewSkipsCompressionWhenNotEligible(t *testing.T) {
	handler := New(Config{
		MinSize:       10,
		ExcludedTypes: []string{"image/"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("small"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "small" {
		t.Fatalf("expected uncompressed excluded response, headers=%v body=%q", rec.Header(), rec.Body.String())
	}

	noAccept := New()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain"))
	}))
	rec = httptest.NewRecorder()
	noAccept.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Body.String() != "plain" || rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected plain response, headers=%v body=%q", rec.Header(), rec.Body.String())
	}

	alreadyEncoded := New()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("kept"))
	}))
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec = httptest.NewRecorder()
	rec.Header().Set("Content-Encoding", "br")
	alreadyEncoded.ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("expected existing content-encoding to survive, got %q", got)
	}
	if rec.Body.String() != "kept" {
		t.Fatalf("expected unmodified body, got %q", rec.Body.String())
	}
}

func TestWriterBranchesForErrorsAndExcludedTypes(t *testing.T) {
	failErr := errors.New("write failed")
	failing := &failingResponseWriter{err: failErr}

	grw := &gzipResponseWriter{
		ResponseWriter: failing,
		gzWriter:       &fakeCompressor{writeErr: failErr},
		pool:           &sync.Pool{},
		minSize:        1,
		statusCode:     http.StatusOK,
	}
	if _, err := grw.Write([]byte("x")); !errors.Is(err, failErr) {
		t.Fatalf("expected gzip write error, got %v", err)
	}

	rec := httptest.NewRecorder()
	grw = &gzipResponseWriter{
		ResponseWriter: rec,
		gzWriter:       &fakeCompressor{},
		pool:           &sync.Pool{},
		minSize:        10,
		statusCode:     http.StatusOK,
		isExcludedFunc: func(ct string) bool { return ct == "image/png" },
	}
	rec.Header().Set("Content-Type", "image/png")
	if _, err := grw.Write([]byte("raw")); err != nil {
		t.Fatalf("excluded gzip write failed: %v", err)
	}

	drw := &deflateResponseWriter{
		ResponseWriter: failing,
		deflateWriter:  &fakeCompressor{writeErr: failErr},
		pool:           &sync.Pool{},
		minSize:        1,
		statusCode:     http.StatusOK,
	}
	if _, err := drw.Write([]byte("x")); !errors.Is(err, failErr) {
		t.Fatalf("expected deflate write error, got %v", err)
	}

	rec = httptest.NewRecorder()
	drw = &deflateResponseWriter{
		ResponseWriter: rec,
		deflateWriter:  &fakeCompressor{},
		pool:           &sync.Pool{},
		minSize:        10,
		statusCode:     http.StatusOK,
		isExcludedFunc: func(ct string) bool { return ct == "image/png" },
	}
	rec.Header().Set("Content-Type", "image/png")
	if _, err := drw.Write([]byte("raw")); err != nil {
		t.Fatalf("excluded deflate write failed: %v", err)
	}

	exactExcluded := New(Config{
		MinSize:       1,
		ExcludedTypes: []string{"application/pdf"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf; charset=utf-8")
		_, _ = w.Write([]byte("pdf"))
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec = httptest.NewRecorder()
	exactExcluded.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "pdf" {
		t.Fatalf("expected exact excluded type to skip compression, headers=%v body=%q", rec.Header(), rec.Body.String())
	}
}
