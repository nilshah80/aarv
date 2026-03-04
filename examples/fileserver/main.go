// Example: File upload, download, and static file serving.
// Demonstrates multipart form handling, file storage, and the static plugin.
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
	files   = make(map[string]FileInfo)
	filesMu sync.RWMutex
	uploadDir = "/tmp/aarv-uploads"
)

func main() {
	// Ensure upload directory exists
	os.MkdirAll(uploadDir, 0755)

	app := aarv.New(
		aarv.WithBanner(true),
	)

	app.Use(
		aarv.Recovery(),
		aarv.Logger(),
		bodylimit.New(10<<20), // 10 MB limit
	)

	// Serve static files from ./public directory
	// In production, this would be your frontend assets
	app.Use(static.New(static.Config{
		Root:   "./public",
		Index:  "index.html",
		Browse: false,
	}))

	// Upload a single file
	app.Post("/upload", func(c *aarv.Context) error {
		// Parse multipart form (max 10MB in memory)
		if err := c.Request().ParseMultipartForm(10 << 20); err != nil {
			return aarv.ErrBadRequest("failed to parse form: " + err.Error())
		}

		file, header, err := c.Request().FormFile("file")
		if err != nil {
			return aarv.ErrBadRequest("no file provided")
		}
		defer file.Close()

		// Generate unique ID
		id := generateID()

		// Calculate hash while copying to disk
		hasher := sha256.New()
		destPath := filepath.Join(uploadDir, id)
		dest, err := os.Create(destPath)
		if err != nil {
			return aarv.ErrInternal(errors.New("failed to create file"))
		}
		defer dest.Close()

		size, err := io.Copy(io.MultiWriter(dest, hasher), file)
		if err != nil {
			os.Remove(destPath)
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

		c.Logger().Info("file uploaded", "id", id, "name", header.Filename, "size", size)

		return c.JSON(http.StatusCreated, info)
	})

	// Upload multiple files
	app.Post("/upload/batch", func(c *aarv.Context) error {
		if err := c.Request().ParseMultipartForm(32 << 20); err != nil {
			return aarv.ErrBadRequest("failed to parse form")
		}

		multipartFiles := c.Request().MultipartForm.File["files"]
		if len(multipartFiles) == 0 {
			return aarv.ErrBadRequest("no files provided")
		}

		var uploaded []FileInfo
		for _, header := range multipartFiles {
			file, err := header.Open()
			if err != nil {
				continue
			}

			id := generateID()
			hasher := sha256.New()
			destPath := filepath.Join(uploadDir, id)
			dest, err := os.Create(destPath)
			if err != nil {
				file.Close()
				continue
			}

			size, err := io.Copy(io.MultiWriter(dest, hasher), file)
			file.Close()
			dest.Close()

			if err != nil {
				os.Remove(destPath)
				continue
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

			uploaded = append(uploaded, info)
		}

		return c.JSON(http.StatusCreated, map[string]any{
			"uploaded": uploaded,
			"count":    len(uploaded),
		})
	})

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
		defer file.Close()

		// Set download headers
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

		// Remove from disk
		os.Remove(filepath.Join(uploadDir, id))

		c.Logger().Info("file deleted", "id", id, "name", info.Name)

		return c.NoContent(http.StatusNoContent)
	})

	// Form with additional fields
	app.Post("/submit", func(c *aarv.Context) error {
		if err := c.Request().ParseMultipartForm(10 << 20); err != nil {
			return aarv.ErrBadRequest("failed to parse form")
		}

		// Get form fields
		title := c.Request().FormValue("title")
		description := c.Request().FormValue("description")

		// Get file (optional)
		var fileInfo *FileInfo
		file, header, err := c.Request().FormFile("attachment")
		if err == nil {
			defer file.Close()

			id := generateID()
			destPath := filepath.Join(uploadDir, id)
			dest, err := os.Create(destPath)
			if err == nil {
				size, _ := io.Copy(dest, file)
				dest.Close()

				info := FileInfo{
					ID:          id,
					Name:        header.Filename,
					Size:        size,
					ContentType: header.Header.Get("Content-Type"),
					UploadedAt:  time.Now().Format(time.RFC3339),
				}

				filesMu.Lock()
				files[id] = info
				filesMu.Unlock()

				fileInfo = &info
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"title":       title,
			"description": description,
			"attachment":  fileInfo,
		})
	})

	fmt.Println("File Server Demo on :8080")
	fmt.Println("  POST   /upload         — upload single file")
	fmt.Println("  POST   /upload/batch   — upload multiple files")
	fmt.Println("  GET    /files          — list all files")
	fmt.Println("  GET    /files/{id}     — get file metadata")
	fmt.Println("  GET    /files/{id}/download — download file")
	fmt.Println("  DELETE /files/{id}     — delete file")
	fmt.Println("  POST   /submit         — form with fields + file")
	fmt.Println()
	fmt.Println("  Upload: curl -F 'file=@myfile.txt' http://localhost:8080/upload")
	fmt.Println("  Batch:  curl -F 'files=@a.txt' -F 'files=@b.txt' http://localhost:8080/upload/batch")
	fmt.Println()
	fmt.Println("  Upload directory:", uploadDir)

	app.Listen(":8080")
}

func generateID() string {
	// Simple ID generation - in production use UUID or similar
	h := sha256.New()
	h.Write([]byte(time.Now().String()))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

