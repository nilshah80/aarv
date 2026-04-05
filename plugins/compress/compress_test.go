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

	"github.com/nilshah80/aarv"
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

func TestSelectEncoding(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		preferGzip bool
		want       string
	}{
		{name: "prefer gzip when both available", header: "gzip, deflate", preferGzip: true, want: "gzip"},
		{name: "prefer deflate when configured", header: "gzip, deflate", preferGzip: false, want: "deflate"},
		{name: "gzip disabled by q zero", header: "gzip;q=0, deflate", preferGzip: true, want: "deflate"},
		{name: "wildcard fallback", header: "*;q=1", preferGzip: true, want: "gzip"},
		{name: "specific zero overrides wildcard", header: "gzip;q=0, *;q=1", preferGzip: true, want: "deflate"},
		{name: "deflate q zero", header: "deflate;q=0", preferGzip: false, want: ""},
		{name: "invalid q ignored", header: "gzip;q=bogus", preferGzip: true, want: "gzip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectEncoding(tt.header, tt.preferGzip); got != tt.want {
				t.Fatalf("selectEncoding(%q, preferGzip=%v) = %q, want %q", tt.header, tt.preferGzip, got, tt.want)
			}
		})
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

func TestExcludedContentTypePassthroughAcrossMultipleWrites(t *testing.T) {
	handler := New(Config{
		MinSize:       1,
		ExcludedTypes: []string{"image/"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("first"))
		_, _ = w.Write([]byte("second"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("expected excluded response to stay uncompressed, got %q", got)
	}
	if rec.Body.String() != "firstsecond" {
		t.Fatalf("expected passthrough writes, got %q", rec.Body.String())
	}
}

func TestNewNativeMiddlewarePath(t *testing.T) {
	body := strings.Repeat("n", 1500)

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{MinSize: 1}))
	app.Get("/test", func(c *aarv.Context) error {
		c.SetHeader("Content-Type", "text/plain; charset=utf-8")
		return c.Text(http.StatusOK, body)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip content-encoding, got %q", rec.Header().Get("Content-Encoding"))
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
	if string(decoded) != body {
		t.Fatalf("unexpected decoded body length=%d", len(decoded))
	}
}

func TestNewNativeDeflateAndSkipPaths(t *testing.T) {
	body := strings.Repeat("d", 1500)

	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{MinSize: 1, PreferGzip: false}))
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, body)
	})

	// Deflate path
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Accept-Encoding", "deflate")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "deflate" {
		t.Fatalf("expected deflate encoding, got %q", rec.Header().Get("Content-Encoding"))
	}

	// No accept-encoding — skip compression
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected no encoding, got %q", rec.Header().Get("Content-Encoding"))
	}
}

func TestNewNativeSkipsAlreadyEncoded(t *testing.T) {
	// Use a native middleware that sets Content-Encoding before compress
	preEncode := aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			c.Response().Header().Set("Content-Encoding", "br")
			return next(c)
		}
	})

	app := aarv.New(aarv.WithBanner(false))
	app.Use(preEncode)
	app.Use(New(Config{MinSize: 1}))
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "already-encoded")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("expected existing encoding preserved, got %q", got)
	}
}

func TestReleaseLargeBuffer(t *testing.T) {
	grw := &gzipResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		buf:            make([]byte, 0, 128<<10),
	}
	releaseGzipResponseWriter(grw)

	drw := &deflateResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		buf:            make([]byte, 0, 128<<10),
	}
	releaseDeflateResponseWriter(drw)
}

func TestTrimASCIISpace(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"  ", ""},
		{"\t\n", ""},
		{"  hello  ", "hello"},
		{"hello", "hello"},
		{" \t hi \r\n", "hi"},
	}
	for _, tt := range tests {
		if got := trimASCIISpace(tt.in); got != tt.want {
			t.Errorf("trimASCIISpace(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAsciiEqualFold(t *testing.T) {
	if !asciiEqualFold("GZIP", "gzip") {
		t.Fatal("expected case-insensitive match")
	}
	if asciiEqualFold("gzp", "gzip") {
		t.Fatal("expected length mismatch to fail")
	}
	if asciiEqualFold("bzip", "gzip") {
		t.Fatal("expected character mismatch to fail")
	}
	if !asciiEqualFold("", "") {
		t.Fatal("expected empty strings to match")
	}
}

func TestQualityAllowedEdgeCases(t *testing.T) {
	// No q param — accepted
	if !qualityAllowed("level=5") {
		t.Fatal("expected non-q param to be accepted")
	}
	// Empty params after semicolons
	if !qualityAllowed(";;") {
		t.Fatal("expected empty params to be accepted")
	}
	// q=0.000 — rejected
	if qualityAllowed("q=0.000") {
		t.Fatal("expected q=0.000 to be rejected")
	}
	// q=0.001 — accepted (contains non-zero digit after decimal)
	if !qualityAllowed("q=0.001") {
		t.Fatal("expected q=0.001 to be accepted")
	}
	// q with no value
	if !qualityAllowed("q") {
		t.Fatal("expected q without = to be accepted")
	}
}

func TestQualityValuePositiveEdgeCases(t *testing.T) {
	// Empty string — no digits seen, returns true (not a zero)
	if !qualityValuePositive("") {
		t.Fatal("expected empty string to be positive")
	}
	// Just a dot
	if !qualityValuePositive(".") {
		t.Fatal("expected dot-only to be positive")
	}
}

func TestDeflateExcludedContentTypeWithCaching(t *testing.T) {
	// Test the deflate isContentTypeExcluded caching path
	rec := httptest.NewRecorder()
	drw := &deflateResponseWriter{
		ResponseWriter: rec,
		deflateWriter:  &fakeCompressor{},
		pool:           &sync.Pool{},
		minSize:        10,
		statusCode:     http.StatusOK,
		isExcludedFunc: func(ct string) bool { return ct == "image/png" },
	}

	// First call with CT set — should check and cache
	rec.Header().Set("Content-Type", "text/plain")
	excluded, known := drw.isContentTypeExcluded()
	if excluded || !known {
		t.Fatal("expected text/plain to not be excluded")
	}

	// Second call — should use cached result
	excluded, known = drw.isContentTypeExcluded()
	if excluded || !known {
		t.Fatal("expected cached non-excluded result")
	}

	// Empty CT path
	rec2 := httptest.NewRecorder()
	drw2 := &deflateResponseWriter{
		ResponseWriter: rec2,
		deflateWriter:  &fakeCompressor{},
		pool:           &sync.Pool{},
		minSize:        10,
		statusCode:     http.StatusOK,
		isExcludedFunc: func(ct string) bool { return true },
	}
	excluded, known = drw2.isContentTypeExcluded()
	if excluded || known {
		t.Fatal("expected empty CT to return unknown")
	}
}

func TestGzipExcludedContentTypeCaching(t *testing.T) {
	rec := httptest.NewRecorder()
	grw := &gzipResponseWriter{
		ResponseWriter: rec,
		gzWriter:       &fakeCompressor{},
		pool:           &sync.Pool{},
		minSize:        10,
		statusCode:     http.StatusOK,
		isExcludedFunc: func(ct string) bool { return ct == "image/png" },
	}

	// Non-excluded type, first call — checks and caches
	rec.Header().Set("Content-Type", "application/json")
	excluded, known := grw.isContentTypeExcluded()
	if excluded || !known {
		t.Fatal("expected application/json to not be excluded")
	}

	// Second call — cached path
	excluded, known = grw.isContentTypeExcluded()
	if excluded || !known {
		t.Fatal("expected cached non-excluded result")
	}
}

func TestGzipWriteBeforeContentTypeSet(t *testing.T) {
	// When Write is called before Content-Type is set, isContentTypeExcluded
	// should return unknown (ct == ""), and data should be buffered.
	rec := httptest.NewRecorder()
	grw := &gzipResponseWriter{
		ResponseWriter: rec,
		gzWriter:       &fakeCompressor{},
		pool:           &sync.Pool{},
		minSize:        100,
		statusCode:     http.StatusOK,
		isExcludedFunc: func(ct string) bool { return false },
	}
	// No Content-Type set — should buffer, not compress
	n, err := grw.Write([]byte("partial"))
	if err != nil || n != 7 {
		t.Fatalf("expected buffered write, got n=%d err=%v", n, err)
	}
	if len(grw.buf) != 7 {
		t.Fatalf("expected 7 bytes buffered, got %d", len(grw.buf))
	}
}

func TestTrimASCIISpaceAllWhitespaceVariants(t *testing.T) {
	// These hit the return "" early path (all whitespace)
	if got := trimASCIISpace("\t\r\n "); got != "" {
		t.Fatalf("expected empty for all-whitespace, got %q", got)
	}
}

func TestDeflateExcludedTypeMultiWrite(t *testing.T) {
	// Test that deflate passthrough mode works for excluded types across writes
	handler := New(Config{
		MinSize:       1,
		PreferGzip:    false,
		ExcludedTypes: []string{"image/"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("first"))
		_, _ = w.Write([]byte("second"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "deflate")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("expected no compression for excluded type via deflate")
	}
	if rec.Body.String() != "firstsecond" {
		t.Fatalf("expected passthrough body, got %q", rec.Body.String())
	}
}

func TestSelectEncodingEmptyParts(t *testing.T) {
	// Trailing comma produces empty part
	if got := selectEncoding("gzip,", true); got != "gzip" {
		t.Fatalf("expected gzip, got %q", got)
	}
	// Only whitespace
	if got := selectEncoding("  , , ", true); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func BenchmarkSelectEncoding_Gzip(b *testing.B) {
	header := "gzip, deflate, br"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if selectEncoding(header, true) == "" {
			b.Fatal("expected encoding")
		}
	}
}

func BenchmarkSelectEncoding_QValues(b *testing.B) {
	header := "br;q=1.0, gzip;q=0.8, deflate;q=0.5, *;q=0"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if selectEncoding(header, true) == "" {
			b.Fatal("expected encoding")
		}
	}
}

func BenchmarkCompress_Stdlib_Gzip(b *testing.B) {
	body := strings.Repeat("compressible payload ", 128)
	handler := New(Config{MinSize: 256})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkCompress_Stdlib_Passthrough(b *testing.B) {
	body := strings.Repeat("compressible payload ", 128)
	handler := New(Config{MinSize: 256})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkCompress_Native_Gzip(b *testing.B) {
	body := strings.Repeat("compressible payload ", 128)
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{MinSize: 256}))
	app.Get("/", func(c *aarv.Context) error {
		c.SetHeader("Content-Type", "text/plain; charset=utf-8")
		return c.Text(http.StatusOK, body)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkCompress_Native_Passthrough(b *testing.B) {
	body := strings.Repeat("compressible payload ", 128)
	app := aarv.New(aarv.WithBanner(false))
	app.Use(New(Config{MinSize: 256}))
	app.Get("/", func(c *aarv.Context) error {
		c.SetHeader("Content-Type", "text/plain; charset=utf-8")
		return c.Text(http.StatusOK, body)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.ServeHTTP(httptest.NewRecorder(), req)
	}
}
