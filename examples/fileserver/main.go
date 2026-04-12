// Example: File upload, download, and static file serving.
// Demonstrates multipart form handling using aarv's UploadedFile API,
// binder integration with file struct tags, and the static plugin.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/bodylimit"
	"github.com/nilshah80/aarv/plugins/static"
)

// FileInfo represents metadata about an uploaded file
type FileInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	Hash        string `json:"hash"`
	UploadedAt  string `json:"uploaded_at"`
}

var (
	files     = make(map[string]FileInfo)
	filesMu   sync.RWMutex
	uploadDir = "/tmp/aarv-uploads"
)

// --- Binder request types ---

// SubmitReq demonstrates binder integration: form fields + optional file
// bound together via struct tags.
type SubmitReq struct {
	Title       string             `form:"title" validate:"required"`
	Description string             `form:"description"`
	Attachment  *aarv.UploadedFile `file:"attachment"`
}

// BatchUploadReq demonstrates multi-file binding via the file struct tag.
type BatchUploadReq struct {
	Docs []*aarv.UploadedFile `file:"files" validate:"required"`
}

func main() {
	_ = os.MkdirAll(uploadDir, 0755)

	app := aarv.New(
		aarv.WithBanner(true),
	)

	app.Use(
		aarv.Recovery(),
		aarv.Logger(),
		bodylimit.New(10<<20), // 10 MB limit
	)

	// Serve static files from ./public directory
	app.Use(static.New(static.Config{
		Root:   "./public",
		Index:  "index.html",
		Browse: false,
	}))

	// ---------------------------------------------------------------
	// Stdlib endpoint (no UploadedFile wrapper — plain net/http)
	// ---------------------------------------------------------------

	// Upload using the standard library multipart API directly.
	// This works without aarv's UploadedFile helper — useful when you
	// need full control or want zero framework abstractions.
	app.Post("/upload/raw", func(c *aarv.Context) error {
		if err := c.Request().ParseMultipartForm(32 << 20); err != nil {
			return aarv.ErrBadRequest("failed to parse form: " + err.Error())
		}

		file, header, err := c.Request().FormFile("file")
		if err != nil {
			return aarv.ErrBadRequest("no file provided")
		}
		defer func() { _ = file.Close() }()

		id := generateID()
		hasher := sha256.New()
		destPath := filepath.Join(uploadDir, id)
		dest, err := os.Create(destPath)
		if err != nil {
			return aarv.ErrInternal(errors.New("failed to create file"))
		}
		defer func() { _ = dest.Close() }()

		size, err := io.Copy(io.MultiWriter(dest, hasher), file)
		if err != nil {
			_ = os.Remove(destPath)
			return aarv.ErrInternal(errors.New("failed to save file"))
		}

		info := FileInfo{
			ID:          id,
			Name:        header.Filename,
			Size:        size,
			ContentType: header.Header.Get("Content-Type"),
			Hash:        hex.EncodeToString(hasher.Sum(nil)),
			UploadedAt:  time.Now().Format(time.RFC3339),
		}

		filesMu.Lock()
		files[id] = info
		filesMu.Unlock()

		return c.JSON(http.StatusCreated, info)
	})

	// ---------------------------------------------------------------
	// Context-based endpoints (using UploadedFile API directly)
	// ---------------------------------------------------------------

	// Upload a single file using c.FormFile
	app.Post("/upload", func(c *aarv.Context) error {
		f, err := c.FormFile("file")
		if errors.Is(err, http.ErrMissingFile) {
			return aarv.ErrBadRequest("no file provided")
		}
		if err != nil {
			return aarv.ErrBadRequest("failed to parse upload: " + err.Error())
		}

		id := generateID()
		info, err := saveUploadedFile(f, id)
		if err != nil {
			return aarv.ErrInternal(err)
		}

		filesMu.Lock()
		files[id] = info
		filesMu.Unlock()

		c.Logger().Info("file uploaded", "id", id, "name", f.Filename, "size", f.Size)
		return c.JSON(http.StatusCreated, info)
	})

	// Upload a single file with validation via c.FileWith
	app.Post("/upload/validated", func(c *aarv.Context) error {
		f, err := c.FileWith("file", aarv.FileConfig{
			MaxFileSize:  5 << 20, // 5MB per file
			AllowedTypes: []string{"text/plain", "application/pdf", "image/png", "image/jpeg"},
		})
		if err != nil {
			return err // 413 for size, 422 for type, propagated as-is
		}

		id := generateID()
		info, err := saveUploadedFile(f, id)
		if err != nil {
			return aarv.ErrInternal(err)
		}

		filesMu.Lock()
		files[id] = info
		filesMu.Unlock()

		return c.JSON(http.StatusCreated, info)
	})

	// Upload multiple files using c.FilesWith
	app.Post("/upload/batch", func(c *aarv.Context) error {
		uploadedFiles, err := c.FilesWith("files", aarv.FileConfig{
			MaxFiles:    10,
			MaxFileSize: 5 << 20,
		})
		if err != nil {
			return err
		}

		var uploaded []FileInfo
		for _, f := range uploadedFiles {
			id := generateID()
			info, err := saveUploadedFile(f, id)
			if err != nil {
				continue
			}

			filesMu.Lock()
			files[id] = info
			filesMu.Unlock()

			uploaded = append(uploaded, info)
		}

		return c.JSON(http.StatusCreated, map[string]any{
			"uploaded": uploaded,
			"count":    len(uploaded),
		})
	})

	// ---------------------------------------------------------------
	// Binder-based endpoints (using file struct tag)
	// ---------------------------------------------------------------

	// Form submission with typed binding: form fields + optional file
	app.Post("/submit", aarv.BindReq(func(c *aarv.Context, req SubmitReq) error {
		var fileInfo *FileInfo
		if req.Attachment != nil {
			id := generateID()
			info, err := saveUploadedFile(req.Attachment, id)
			if err == nil {
				filesMu.Lock()
				files[id] = info
				filesMu.Unlock()
				fileInfo = &info
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"title":       req.Title,
			"description": req.Description,
			"attachment":  fileInfo,
		})
	}))

	// Batch upload with typed binding
	app.Post("/submit/batch", aarv.BindReq(func(c *aarv.Context, req BatchUploadReq) error {
		var uploaded []FileInfo
		for _, f := range req.Docs {
			id := generateID()
			info, err := saveUploadedFile(f, id)
			if err != nil {
				continue
			}

			filesMu.Lock()
			files[id] = info
			filesMu.Unlock()

			uploaded = append(uploaded, info)
		}

		return c.JSON(http.StatusCreated, map[string]any{
			"uploaded": uploaded,
			"count":    len(uploaded),
		})
	}))

	// ---------------------------------------------------------------
	// File management endpoints
	// ---------------------------------------------------------------

	// List all uploaded files
	app.Get("/files", func(c *aarv.Context) error {
		filesMu.RLock()
		defer filesMu.RUnlock()

		list := make([]FileInfo, 0, len(files))
		for _, f := range files {
			list = append(list, f)
		}

		return c.JSON(http.StatusOK, map[string]any{
			"files": list,
			"total": len(list),
		})
	})

	// Get file metadata
	app.Get("/files/{id}", func(c *aarv.Context) error {
		id := c.Param("id")

		filesMu.RLock()
		info, ok := files[id]
		filesMu.RUnlock()

		if !ok {
			return aarv.ErrNotFound("file not found")
		}

		return c.JSON(http.StatusOK, info)
	})

	// Download a file
	app.Get("/files/{id}/download", func(c *aarv.Context) error {
		id := c.Param("id")

		filesMu.RLock()
		info, ok := files[id]
		filesMu.RUnlock()

		if !ok {
			return aarv.ErrNotFound("file not found")
		}

		filePath := filepath.Join(uploadDir, id)
		file, err := os.Open(filePath)
		if err != nil {
			return aarv.ErrNotFound("file data not found")
		}
		defer func() { _ = file.Close() }()

		c.SetHeader("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", info.Name))
		c.SetHeader("Content-Type", info.ContentType)
		c.SetHeader("Content-Length", fmt.Sprintf("%d", info.Size))

		return c.Stream(http.StatusOK, info.ContentType, file)
	})

	// Delete a file
	app.Delete("/files/{id}", func(c *aarv.Context) error {
		id := c.Param("id")

		filesMu.Lock()
		info, ok := files[id]
		if ok {
			delete(files, id)
		}
		filesMu.Unlock()

		if !ok {
			return aarv.ErrNotFound("file not found")
		}

		_ = os.Remove(filepath.Join(uploadDir, id))
		c.Logger().Info("file deleted", "id", id, "name", info.Name)

		return c.NoContent(http.StatusNoContent)
	})

	fmt.Println("File Server Demo on :8080")
	fmt.Println()
	fmt.Println("Stdlib endpoint (no UploadedFile wrapper):")
	fmt.Println("  POST   /upload/raw           — plain r.FormFile, no framework helpers")
	fmt.Println()
	fmt.Println("Context-based endpoints (UploadedFile API):")
	fmt.Println("  POST   /upload              — single file via c.FormFile")
	fmt.Println("  POST   /upload/validated     — single file with size+type validation via c.FileWith")
	fmt.Println("  POST   /upload/batch         — multiple files with validation via c.FilesWith")
	fmt.Println()
	fmt.Println("Binder-based endpoints (file struct tag):")
	fmt.Println("  POST   /submit              — form fields + optional file via Bind")
	fmt.Println("  POST   /submit/batch        — multi-file binding via Bind")
	fmt.Println()
	fmt.Println("File management:")
	fmt.Println("  GET    /files               — list all files")
	fmt.Println("  GET    /files/{id}          — get file metadata")
	fmt.Println("  GET    /files/{id}/download  — download file")
	fmt.Println("  DELETE /files/{id}          — delete file")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  curl -F 'file=@myfile.txt' http://localhost:8080/upload/raw")
	fmt.Println("  curl -F 'file=@myfile.txt' http://localhost:8080/upload")
	fmt.Println("  curl -F 'file=@photo.png' http://localhost:8080/upload/validated")
	fmt.Println("  curl -F 'files=@a.txt' -F 'files=@b.txt' http://localhost:8080/upload/batch")
	fmt.Println("  curl -F 'title=Hello' -F 'description=Test' -F 'attachment=@doc.pdf' http://localhost:8080/submit")
	fmt.Println("  curl -F 'files=@a.txt' -F 'files=@b.txt' http://localhost:8080/submit/batch")
	fmt.Println()
	fmt.Println("  Upload directory:", uploadDir)

	_ = app.Listen(":8080")
}

// saveUploadedFile stores an UploadedFile to disk and returns its metadata.
func saveUploadedFile(f *aarv.UploadedFile, id string) (FileInfo, error) {
	src, err := f.Open()
	if err != nil {
		return FileInfo{}, err
	}
	defer func() { _ = src.Close() }()

	hasher := sha256.New()
	destPath := filepath.Join(uploadDir, id)
	dest, err := os.Create(destPath)
	if err != nil {
		return FileInfo{}, errors.New("failed to create file")
	}
	defer func() { _ = dest.Close() }()

	size, err := io.Copy(io.MultiWriter(dest, hasher), src)
	if err != nil {
		_ = os.Remove(destPath)
		return FileInfo{}, errors.New("failed to save file")
	}

	return FileInfo{
		ID:          id,
		Name:        f.Filename,
		Size:        size,
		ContentType: f.ContentType,
		Hash:        hex.EncodeToString(hasher.Sum(nil)),
		UploadedAt:  time.Now().Format(time.RFC3339),
	}, nil
}

// generateID returns a short hex ID for demo purposes.
// Not collision-safe — production code should use UUID or ULID.
func generateID() string {
	h := sha256.New()
	h.Write([]byte(time.Now().String()))
	return hex.EncodeToString(h.Sum(nil))[:16]
}
