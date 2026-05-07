package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/bodylimit"
	"github.com/nilshah80/aarv/plugins/requestid"
)

// UploadReq is the multipart form bound by the upload route.
type UploadReq struct {
	Title string             `form:"title" validate:"required"`
	File  *aarv.UploadedFile `file:"file" validate:"required"`
}

// FileInfo is the persisted record returned to clients after upload.
type FileInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
}

var (
	uploadDir = filepath.Join(os.TempDir(), "aarv-file-upload-example")
	filesMu   sync.RWMutex
	files     = map[string]FileInfo{}
)

func main() {
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		panic(err)
	}

	app := aarv.New(aarv.WithBanner(true))
	app.Use(aarv.Recovery(), requestid.New(), bodylimit.New(20<<20))

	app.Post("/files", aarv.BindReq(func(c *aarv.Context, req UploadReq) error {
		id := fmt.Sprintf("file_%d", time.Now().UnixNano())
		info, err := save(req.Title, req.File, id)
		if err != nil {
			return aarv.ErrInternal(err)
		}

		filesMu.Lock()
		files[id] = info
		filesMu.Unlock()

		return c.JSON(http.StatusCreated, info)
	}), aarv.WithRouteMaxBodySize(20<<20))

	app.Get("/files", func(c *aarv.Context) error {
		filesMu.RLock()
		defer filesMu.RUnlock()

		out := make([]FileInfo, 0, len(files))
		for _, f := range files {
			out = append(out, f)
		}
		return c.JSON(http.StatusOK, map[string]any{"files": out})
	})

	app.Get("/files/{id}/download", func(c *aarv.Context) error {
		id := c.Param("id")

		filesMu.RLock()
		info, ok := files[id]
		filesMu.RUnlock()
		if !ok {
			return aarv.ErrNotFound("file not found")
		}

		return c.Attachment(filepath.Join(uploadDir, id), info.Name)
	})

	fmt.Println("File upload example on :8080")
	fmt.Println(`  curl -F 'title=demo' -F 'file=@README.md' http://localhost:8080/files`)
	fmt.Println("  GET /files")
	fmt.Println("  GET /files/{id}/download")

	_ = app.Listen(":8080")
}

func save(title string, file *aarv.UploadedFile, id string) (FileInfo, error) {
	src, err := file.Open()
	if err != nil {
		return FileInfo{}, err
	}
	defer func() { _ = src.Close() }()

	dst, err := os.Create(filepath.Join(uploadDir, id))
	if err != nil {
		return FileInfo{}, err
	}
	defer func() { _ = dst.Close() }()

	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(dst, h), src)
	if err != nil {
		return FileInfo{}, err
	}

	return FileInfo{
		ID:          id,
		Title:       title,
		Name:        filepath.Base(file.Filename),
		Size:        size,
		ContentType: file.ContentType,
		SHA256:      hex.EncodeToString(h.Sum(nil)),
	}, nil
}
