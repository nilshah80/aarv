package aarv

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"slices"
)

// UploadedFile wraps a multipart file header with convenience accessors.
// It is returned by Context.FormFile and Context.FormFiles.
type UploadedFile struct {
	// Filename is the original filename from the client.
	Filename string

	// Size is the file size in bytes.
	Size int64

	// ContentType is the MIME type from the Content-Type part header.
	// This is client-supplied and should not be treated as a trusted
	// security boundary. Use http.DetectContentType on the file bytes
	// for trusted MIME sniffing.
	ContentType string

	// Header contains the raw MIME headers of the multipart part.
	Header textproto.MIMEHeader

	fh *multipart.FileHeader
}

// Open returns the associated file for reading.
// The caller must close the returned file when done.
func (f *UploadedFile) Open() (multipart.File, error) {
	return f.fh.Open()
}

// newUploadedFile constructs an UploadedFile from a stdlib FileHeader.
func newUploadedFile(fh *multipart.FileHeader) *UploadedFile {
	return &UploadedFile{
		Filename:    fh.Filename,
		Size:        fh.Size,
		ContentType: fh.Header.Get("Content-Type"),
		Header:      fh.Header,
		fh:          fh,
	}
}

// FileConfig holds validation constraints for file upload helpers.
type FileConfig struct {
	// MaxFileSize is the per-file byte limit. 0 means no limit.
	MaxFileSize int64

	// AllowedTypes is a whitelist of permitted MIME types (e.g. "image/png").
	// Validation is based on the client-supplied Content-Type multipart
	// header, not MIME sniffing. Nil or empty means allow all types.
	AllowedTypes []string

	// MaxFiles is the maximum number of files for a multi-file field.
	// 0 means no limit.
	MaxFiles int
}

// validateFile checks a single file against the given constraints.
func validateFile(f *UploadedFile, cfg FileConfig) error {
	if cfg.MaxFileSize > 0 && f.Size > cfg.MaxFileSize {
		return ErrPayloadTooLarge(fmt.Sprintf("file %q exceeds maximum size of %d bytes", f.Filename, cfg.MaxFileSize))
	}
	if len(cfg.AllowedTypes) > 0 && !slices.Contains(cfg.AllowedTypes, f.ContentType) {
		return ErrUnprocessable(fmt.Sprintf("file %q has disallowed content type %q", f.Filename, f.ContentType))
	}
	return nil
}

// progressWriter wraps a writer and invokes onProgress after each non-empty
// Write with the cumulative bytes successfully written so far and the known
// total size. It is used by Context.SaveFileWith to report copy progress
// against the destination — counting writes (not source reads) so a short
// write or write error never reports bytes the destination did not accept.
// "written" means accepted by Write, not fsync-durable. onProgress
// must be non-nil; the callback runs synchronously on the copying goroutine.
type progressWriter struct {
	w          io.Writer
	written    int64
	total      int64
	onProgress func(written, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	if n > 0 {
		p.written += int64(n)
		p.onProgress(p.written, p.total)
	}
	return n, err
}

// validateFiles checks a slice of files against the given constraints.
func validateFiles(files []*UploadedFile, cfg FileConfig) error {
	if cfg.MaxFiles > 0 && len(files) > cfg.MaxFiles {
		return ErrUnprocessable(fmt.Sprintf("too many files: got %d, maximum is %d", len(files), cfg.MaxFiles))
	}
	for _, f := range files {
		if err := validateFile(f, cfg); err != nil {
			return err
		}
	}
	return nil
}
