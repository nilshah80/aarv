package aarv

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// multipartBody builds a multipart request body with the given file fields.
// Each entry in files maps field name → list of (filename, content) pairs.
func multipartBody(t *testing.T, files map[string][][2]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for field, entries := range files {
		for _, entry := range entries {
			part, err := w.CreateFormFile(field, entry[0])
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte(entry[1])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func multipartRequest(t *testing.T, files map[string][][2]string) *http.Request {
	t.Helper()
	buf, ct := multipartBody(t, files)
	req := httptest.NewRequest(http.MethodPost, "/", buf)
	req.Header.Set("Content-Type", ct)
	return req
}

// --- UploadedFile tests ---

func TestUploadedFileOpen(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"readme.txt", "hello world"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	f, err := ctx.FormFile("doc")
	if err != nil {
		t.Fatal(err)
	}
	if f.Filename != "readme.txt" {
		t.Fatalf("expected readme.txt, got %q", f.Filename)
	}
	if f.Size != 11 {
		t.Fatalf("expected size 11, got %d", f.Size)
	}

	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	data, _ := io.ReadAll(rc)
	if string(data) != "hello world" {
		t.Fatalf("expected hello world, got %q", data)
	}
}

// --- FormFile tests ---

func TestFormFileMissing(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"a.txt", "data"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FormFile("missing")
	if err != http.ErrMissingFile {
		t.Fatalf("expected http.ErrMissingFile, got %v", err)
	}
}

func TestFormFileNonMultipartRequest(t *testing.T) {
	app := New(WithBanner(false))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FormFile("anything")
	if err == nil {
		t.Fatal("expected error on non-multipart request")
	}
}

// --- FormFiles tests ---

func TestFormFilesHappyPath(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"docs": {{"a.txt", "aaa"}, {"b.txt", "bbb"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	files, err := ctx.FormFiles("docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Filename != "a.txt" || files[1].Filename != "b.txt" {
		t.Fatalf("unexpected filenames: %q, %q", files[0].Filename, files[1].Filename)
	}
}

func TestFormFilesMissing(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"a.txt", "data"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FormFiles("missing")
	if err != http.ErrMissingFile {
		t.Fatalf("expected http.ErrMissingFile, got %v", err)
	}
}

func TestFormFilesNonMultipartRequest(t *testing.T) {
	app := New(WithBanner(false))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FormFiles("anything")
	if err == nil {
		t.Fatal("expected error on non-multipart request")
	}
}

// --- FileWith tests ---

func TestFileWithHappyPath(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"avatar": {{"photo.png", "png-data"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	f, err := ctx.FileWith("avatar", FileConfig{
		MaxFileSize:  1024,
		AllowedTypes: []string{"application/octet-stream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Filename != "photo.png" {
		t.Fatalf("expected photo.png, got %q", f.Filename)
	}
}

func TestFileWithTooLarge(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"big": {{"huge.bin", "12345678901234567890"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FileWith("big", FileConfig{MaxFileSize: 5})
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	appErr, ok := err.(*AppError)
	if !ok || appErr.StatusCode() != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 AppError, got %v", err)
	}
}

func TestFileWithDisallowedType(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"script.js", "alert(1)"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FileWith("doc", FileConfig{AllowedTypes: []string{"image/png"}})
	if err == nil {
		t.Fatal("expected error for disallowed type")
	}
	appErr, ok := err.(*AppError)
	if !ok || appErr.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 AppError, got %v", err)
	}
}

// --- FilesWith tests ---

func TestFilesWithHappyPath(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"docs": {{"a.txt", "aaa"}, {"b.txt", "bbb"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	files, err := ctx.FilesWith("docs", FileConfig{MaxFiles: 5, MaxFileSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
}

func TestFilesWithTooMany(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"docs": {{"a.txt", "a"}, {"b.txt", "b"}, {"c.txt", "c"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FilesWith("docs", FileConfig{MaxFiles: 2})
	if err == nil {
		t.Fatal("expected error for too many files")
	}
	appErr, ok := err.(*AppError)
	if !ok || appErr.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 AppError, got %v", err)
	}
}

func TestFilesWithSizeViolation(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"docs": {{"small.txt", "ok"}, {"big.txt", "this is way too large"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FilesWith("docs", FileConfig{MaxFileSize: 5})
	if err == nil {
		t.Fatal("expected error for oversized file in batch")
	}
	appErr, ok := err.(*AppError)
	if !ok || appErr.StatusCode() != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 AppError, got %v", err)
	}
}

// --- SaveFile tests ---

func TestSaveFileHappyPath(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"save-me.txt", "saved content"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	f, err := ctx.FormFile("doc")
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "output.txt")
	if err := ctx.SaveFile(f, dst); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "saved content" {
		t.Fatalf("expected saved content, got %q", data)
	}
}

func TestSaveFileInvalidPath(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"file.txt", "data"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	f, err := ctx.FormFile("doc")
	if err != nil {
		t.Fatal(err)
	}

	err = ctx.SaveFile(f, "/no/such/directory/file.txt")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestSaveFileWithProgress(t *testing.T) {
	app := New(WithBanner(false))
	// Content larger than io.Copy's 32KB buffer so the copy spans multiple
	// reads and onProgress fires more than once.
	content := strings.Repeat("a", 100_000)
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"big.txt", content}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	f, err := ctx.FormFile("doc")
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	var last int64
	var lastTotal int64
	dst := filepath.Join(t.TempDir(), "big-out.txt")
	err = ctx.SaveFileWith(f, dst, func(written, total int64) {
		calls++
		if written < last {
			t.Fatalf("progress went backwards: %d after %d", written, last)
		}
		last = written
		lastTotal = total
	})
	if err != nil {
		t.Fatal(err)
	}

	if calls < 2 {
		t.Fatalf("expected multiple progress callbacks for a 100KB file, got %d", calls)
	}
	if last != int64(len(content)) {
		t.Fatalf("final written = %d, want %d", last, len(content))
	}
	if lastTotal != f.Size {
		t.Fatalf("reported total = %d, want file size %d", lastTotal, f.Size)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("saved content mismatch: got %d bytes, want %d", len(data), len(content))
	}
}

func TestSaveFileWithNilProgress(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"save-me.txt", "saved content"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	f, err := ctx.FormFile("doc")
	if err != nil {
		t.Fatal(err)
	}

	// nil callback must behave exactly like SaveFile.
	dst := filepath.Join(t.TempDir(), "output.txt")
	if err := ctx.SaveFileWith(f, dst, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "saved content" {
		t.Fatalf("expected saved content, got %q", data)
	}
}

func TestSaveFileWithInvalidPath(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"file.txt", "data"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	f, err := ctx.FormFile("doc")
	if err != nil {
		t.Fatal(err)
	}

	called := false
	err = ctx.SaveFileWith(f, "/no/such/directory/file.txt", func(_, _ int64) {
		called = true
	})
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
	if called {
		t.Fatal("onProgress must not fire when the destination cannot be created")
	}
}

// shortThenFailWriter accepts the first acceptBytes bytes (possibly across
// writes, short-writing the boundary chunk) and then fails every subsequent
// write with n == 0. It models a destination that stops accepting writes partway.
type shortThenFailWriter struct {
	accept  int
	written int
}

func (w *shortThenFailWriter) Write(b []byte) (int, error) {
	if w.written >= w.accept {
		return 0, io.ErrShortWrite
	}
	n := len(b)
	if w.written+n > w.accept {
		n = w.accept - w.written // short write at the boundary
	}
	w.written += n
	if n < len(b) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

// TestProgressWriterReportsOnlyPersistedBytes verifies the callback counts
// bytes the destination actually accepted, never bytes that failed to write.
func TestProgressWriterReportsOnlyPersistedBytes(t *testing.T) {
	const accept = 50_000
	src := bytes.NewReader([]byte(strings.Repeat("a", 100_000)))
	dst := &shortThenFailWriter{accept: accept}

	var last int64
	pw := &progressWriter{w: dst, total: 100_000, onProgress: func(written, _ int64) {
		if written < last {
			t.Fatalf("progress went backwards: %d after %d", written, last)
		}
		last = written
	}}

	_, err := io.Copy(pw, src)
	if err == nil {
		t.Fatal("expected copy to fail once the destination stops accepting")
	}
	if last != accept {
		t.Fatalf("final reported written = %d, want exactly the accepted %d", last, accept)
	}
	if pw.written != int64(accept) {
		t.Fatalf("progressWriter.written = %d, want %d", pw.written, accept)
	}
}

// TestProgressWriterNoCallbackOnZeroWrite verifies a failing write that
// writes nothing (n == 0) does not fire the callback.
func TestProgressWriterNoCallbackOnZeroWrite(t *testing.T) {
	dst := &shortThenFailWriter{accept: 0} // accepts nothing
	called := false
	pw := &progressWriter{w: dst, total: 10, onProgress: func(_, _ int64) { called = true }}

	n, err := pw.Write([]byte("hello"))
	if err == nil {
		t.Fatal("expected write error")
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes written, got %d", n)
	}
	if called {
		t.Fatal("onProgress must not fire when no bytes were written")
	}
}

// --- parseMultipartIfNeeded tests ---

func TestParseMultipartIfNeededIdempotent(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"doc": {{"a.txt", "data"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	// First call parses
	if err := ctx.parseMultipartIfNeeded(); err != nil {
		t.Fatal(err)
	}
	// Second call is a no-op (MultipartForm already set)
	if err := ctx.parseMultipartIfNeeded(); err != nil {
		t.Fatal(err)
	}
	if ctx.req.MultipartForm == nil {
		t.Fatal("expected MultipartForm to be set")
	}
}

// --- FormFiles edge case: MultipartForm nil after successful parse ---

func TestFormFilesNilMultipartForm(t *testing.T) {
	app := New(WithBanner(false))
	// Build a valid multipart body but with no file fields
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("name", "value")
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FormFiles("missing")
	if err != http.ErrMissingFile {
		t.Fatalf("expected ErrMissingFile, got %v", err)
	}
}

// --- FileWith/FilesWith error propagation ---

func TestFileWithMissing(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"other": {{"a.txt", "data"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FileWith("missing", FileConfig{MaxFileSize: 1024})
	if err != http.ErrMissingFile {
		t.Fatalf("expected ErrMissingFile, got %v", err)
	}
}

func TestFilesWithMissing(t *testing.T) {
	app := New(WithBanner(false))
	req := multipartRequest(t, map[string][][2]string{
		"other": {{"a.txt", "data"}},
	})
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_, err := ctx.FilesWith("missing", FileConfig{MaxFiles: 5})
	if err != http.ErrMissingFile {
		t.Fatalf("expected ErrMissingFile, got %v", err)
	}
}

// --- SaveFile Create() error already covered by TestSaveFileInvalidPath ---

// --- SetBody coverage ---

func TestSetBodyCoverage(t *testing.T) {
	app := New(WithBanner(false))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("original"))
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	// Read body to populate cache
	body, err := ctx.Body()
	if err != nil || string(body) != "original" {
		t.Fatalf("unexpected body: %q err=%v", body, err)
	}

	// SetBody with small cache (reusable)
	ctx.SetBody(io.NopCloser(strings.NewReader("new-body")))
	if ctx.bodyRead {
		t.Fatal("expected bodyRead reset")
	}

	// SetBody with large cache (should nil out)
	ctx.bodyCache = make([]byte, maxReusableBodyCache+1)
	ctx.SetBody(io.NopCloser(strings.NewReader("another")))
	if ctx.bodyCache != nil {
		t.Fatal("expected large bodyCache to be nil'd")
	}
}

// --- validateFile / validateFiles unit tests ---

func TestValidateFileSize(t *testing.T) {
	f := &UploadedFile{Filename: "big.bin", Size: 100}
	if err := validateFile(f, FileConfig{MaxFileSize: 200}); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
	if err := validateFile(f, FileConfig{MaxFileSize: 50}); err == nil {
		t.Fatal("expected size error")
	}
	// Zero limit means no limit
	if err := validateFile(f, FileConfig{MaxFileSize: 0}); err != nil {
		t.Fatalf("expected pass with no limit, got %v", err)
	}
}

func TestValidateFileType(t *testing.T) {
	f := &UploadedFile{Filename: "img.png", ContentType: "image/png"}
	if err := validateFile(f, FileConfig{AllowedTypes: []string{"image/png", "image/jpeg"}}); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
	if err := validateFile(f, FileConfig{AllowedTypes: []string{"application/pdf"}}); err == nil {
		t.Fatal("expected type error")
	}
	// Nil AllowedTypes means allow all
	if err := validateFile(f, FileConfig{}); err != nil {
		t.Fatalf("expected pass with no type filter, got %v", err)
	}
}

func TestValidateFilesCount(t *testing.T) {
	files := []*UploadedFile{
		{Filename: "a.txt", Size: 1},
		{Filename: "b.txt", Size: 1},
		{Filename: "c.txt", Size: 1},
	}
	if err := validateFiles(files, FileConfig{MaxFiles: 5}); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
	if err := validateFiles(files, FileConfig{MaxFiles: 2}); err == nil {
		t.Fatal("expected count error")
	}
	// Zero limit means no limit
	if err := validateFiles(files, FileConfig{MaxFiles: 0}); err != nil {
		t.Fatalf("expected pass with no limit, got %v", err)
	}
}
