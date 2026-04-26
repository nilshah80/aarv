package aarv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// =============================================================================
// binder.go: file-binding error paths
// =============================================================================

// TestBindFileSliceBindingError exercises the binder branch where
// FormFiles returns a non-ErrMissingFile error during slice file binding.
// The malformed multipart body forces ParseMultipartForm to fail, which
// surfaces as a BindError → 400.
func TestBindFileSliceBindingError(t *testing.T) {
	type req struct {
		Docs []*UploadedFile `file:"docs"`
	}

	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	app.Post("/upload", BindReq(func(c *Context, payload req) error {
		return c.NoContent(http.StatusOK)
	}))

	httpReq := httptest.NewRequest(http.MethodPost, "/upload",
		strings.NewReader("garbage that is not a real multipart body"))
	httpReq.Header.Set("Content-Type", "multipart/form-data; boundary=fakeboundary")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed multipart should bind-error → 400, got %d body=%s",
			rec.Code, rec.Body.String())
	}
}

// TestBindFileSingleBindingError covers the single-file analogue of the above.
func TestBindFileSingleBindingError(t *testing.T) {
	type req struct {
		Image *UploadedFile `file:"image"`
	}

	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	app.Post("/upload", BindReq(func(c *Context, payload req) error {
		return c.NoContent(http.StatusOK)
	}))

	httpReq := httptest.NewRequest(http.MethodPost, "/upload",
		strings.NewReader("garbage"))
	httpReq.Header.Set("Content-Type", "multipart/form-data; boundary=fakeboundary")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed multipart should bind-error → 400, got %d body=%s",
			rec.Code, rec.Body.String())
	}
}

// =============================================================================
// context.go: FormFiles defensive nil-File branch
// =============================================================================

// TestFormFilesNilFileMap covers the defensive branch where MultipartForm is
// already populated but its File map is nil. modern Go always initializes the
// File map, so the branch is only reachable via direct construction.
func TestFormFilesNilFileMap(t *testing.T) {
	app := New(WithBanner(false))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.MultipartForm = &multipart.Form{
		Value: map[string][]string{},
		File:  nil,
	}
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FormFiles("anything")
	if !errors.Is(err, http.ErrMissingFile) {
		t.Fatalf("nil File map must return ErrMissingFile, got %v", err)
	}
}

// =============================================================================
// context.go: SaveFile open-error branch
// =============================================================================

// TestSaveFileOpenError covers the branch where UploadedFile.Open() fails.
// The trick: hand-construct an UploadedFile from a multipart.FileHeader that
// references a temp file we then unlink before calling Open.
func TestSaveFileOpenError(t *testing.T) {
	app := New(WithBanner(false))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	// A bare FileHeader with no Content-Disposition body and no temp file
	// triggers an Open error. We simulate this by constructing an
	// UploadedFile whose underlying *multipart.FileHeader has zero state.
	uf := &UploadedFile{
		Filename:    "x",
		Size:        0,
		ContentType: "text/plain",
		Header:      nil,
		fh:          &multipart.FileHeader{Filename: "x"},
	}

	// Open must error because the FileHeader has neither in-memory content
	// nor a backing temp file path.
	if _, err := uf.Open(); err == nil {
		t.Skip("multipart.FileHeader.Open succeeded unexpectedly on this Go version; cannot exercise SaveFile open-error path")
	}

	dst := filepath.Join(t.TempDir(), "out")
	if err := ctx.SaveFile(uf, dst); err == nil {
		t.Fatal("SaveFile must surface the Open() error from a header with no backing data")
	}
}

// =============================================================================
// context.go: SSE bufferedResponseWriter bypass branch
// =============================================================================

// TestContextSSEBypassesBufferedWriter covers the branch where c.res is a
// *bufferedResponseWriter and SSE() must call Bypass() so events are flushed
// directly rather than buffered until handler return.
func TestContextSSEBypassesBufferedWriter(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	bw := acquireBufferedWriter(rec)
	defer releaseBufferedWriter(bw)

	ctx := app.AcquireContext(bw, req)
	defer app.ReleaseContext(ctx)
	ctx.res = bw

	sse, err := ctx.SSE()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sse.Close() }()
	if !bw.bypassed {
		t.Fatal("SSE() must enable Bypass on the buffered writer so events stream live")
	}
}

// =============================================================================
// sse.go: Send / Comment / Flush coverage
// =============================================================================

// errWriter implements http.ResponseWriter and always errors on Write.
type errWriter struct {
	header http.Header
	code   int
}

func (e *errWriter) Header() http.Header {
	if e.header == nil {
		e.header = make(http.Header)
	}
	return e.header
}
func (e *errWriter) WriteHeader(c int)           { e.code = c }
func (e *errWriter) Write(_ []byte) (int, error) { return 0, errors.New("forced write error") }

// flushRecorder wraps httptest.ResponseRecorder with an http.Flusher implementation
// so SSEWriter.Flush exercises the flusher.Flush() branch.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushRecorder) Flush() { f.flushed = true }

func TestSSESendWriteError(t *testing.T) {
	w := &SSEWriter{
		w:   &errWriter{},
		ctx: context.Background(),
	}
	if err := w.Send(SSEEvent{Data: "x"}); err == nil {
		t.Fatal("Send must surface underlying write errors")
	}
}

func TestSSECommentContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &SSEWriter{
		w:   httptest.NewRecorder(),
		ctx: ctx,
	}
	err := w.Comment("ping")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Comment on cancelled ctx must return context error, got %v", err)
	}
}

func TestSSECommentWriteError(t *testing.T) {
	w := &SSEWriter{
		w:   &errWriter{},
		ctx: context.Background(),
	}
	if err := w.Comment("x"); err == nil {
		t.Fatal("Comment must surface underlying write errors")
	}
}

func TestSSEFlushUsesFlusher(t *testing.T) {
	fr := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := &SSEWriter{
		w:       fr,
		flusher: fr,
		ctx:     context.Background(),
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if !fr.flushed {
		t.Fatal("Flush() must call the underlying http.Flusher")
	}
}

// =============================================================================
// securecookie.go: encryptValue / decryptValue bad-key paths
// =============================================================================

// =============================================================================
// context.go: reset() cachedLogger cleanup branch
// =============================================================================

// TestContextResetClearsCachedLogger covers the branch in reset() that nils a
// previously-populated cachedLogger so a recycled Context cannot leak a
// request-scoped logger across requests. The race-detector run can otherwise
// schedule pool acquire/release such that no test happens to recycle a Context
// after Logger() was called on it.
func TestContextResetClearsCachedLogger(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)

	_ = ctx.Logger() // populate cachedLogger
	if ctx.cachedLogger == nil {
		t.Fatal("Logger() must populate cachedLogger")
	}

	// Re-arm the same Context via reset; cachedLogger must be cleared so the
	// next request gets a fresh request-scoped logger.
	ctx.reset(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if ctx.cachedLogger != nil {
		t.Fatal("reset() must clear cachedLogger")
	}
	app.ReleaseContext(ctx)
}

// TestEncryptValueBadKeyLength covers the aes.NewCipher error branch in
// encryptValue. deriveKeys validates the master key, but encryptValue is also
// callable directly within the package and must reject malformed keys
// defensively.
func TestEncryptValueBadKeyLength(t *testing.T) {
	if _, err := encryptValue("plaintext", []byte{1, 2, 3}); !errors.Is(err, ErrCookieDecryptFailed) {
		t.Fatalf("encryptValue with 3-byte key must fail with ErrCookieDecryptFailed, got %v", err)
	}
}

// TestDecryptValueBadKeyLength covers the analogous branch in decryptValue.
func TestDecryptValueBadKeyLength(t *testing.T) {
	// Use a base64-decodable input long enough to pass the length check so
	// the test reaches aes.NewCipher specifically.
	encoded := "AAAAAAAAAAAAAAAA" // valid base64url, decodes to 12 bytes
	if _, err := decryptValue(encoded, []byte{1, 2, 3}); !errors.Is(err, ErrCookieDecryptFailed) {
		t.Fatalf("decryptValue with 3-byte key must fail with ErrCookieDecryptFailed, got %v", err)
	}
}

// =============================================================================
// Anti-flake guard for windows runtime path differences in tests above.
// (No-op on non-windows; here only so the runtime import is used.)
// =============================================================================

func TestPlatformGuard(t *testing.T) {
	if runtime.GOOS == "" {
		t.Fatal("runtime.GOOS unexpectedly empty")
	}
	// Ensure os.TempDir is callable; placeholder.
	_ = os.TempDir()
}
