// Package static provides static file serving middleware for the aarv framework.
//
// It wraps http.FileServer with additional features such as Cache-Control headers,
// SPA (Single Page Application) fallback, and directory browsing control.
package static

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nilshah80/aarv"
)

// Config holds configuration for the static file middleware.
type Config struct {
	// Root is the directory to serve files from. Required.
	Root string

	// Index is the name of the index file to serve for directory requests.
	// Default: "index.html".
	Index string

	// Browse enables directory listing when true.
	// When false (default), requests for directories without an index file
	// return 404.
	Browse bool

	// MaxAge sets the Cache-Control max-age directive in seconds.
	// Default: 0 (no caching).
	MaxAge int

	// SPA enables Single Page Application mode. When true, requests that would
	// result in a 404 (file not found) are served the root index file instead.
	// This supports client-side routing in SPAs.
	SPA bool
}

// DefaultConfig returns the default static file configuration.
func DefaultConfig() Config {
	return Config{
		Index: "index.html",
	}
}

// notFoundWriter is a response writer that captures 404 responses so the SPA
// fallback can serve the index file instead.
type notFoundWriter struct {
	http.ResponseWriter
	statusCode int
	is404      bool
}

func (nfw *notFoundWriter) WriteHeader(code int) {
	nfw.statusCode = code
	if code == http.StatusNotFound {
		nfw.is404 = true
		// Don't forward the 404 — we'll serve the SPA fallback
		return
	}
	nfw.ResponseWriter.WriteHeader(code)
}

func (nfw *notFoundWriter) Write(b []byte) (int, error) {
	if nfw.is404 {
		// Swallow the body of the 404 response
		return len(b), nil
	}
	return nfw.ResponseWriter.Write(b)
}

// Unwrap returns the underlying http.ResponseWriter.
func (nfw *notFoundWriter) Unwrap() http.ResponseWriter {
	return nfw.ResponseWriter
}

// New creates a static file serving middleware with the given configuration.
// The Config.Root field is required.
func New(config Config) aarv.Middleware {
	if config.Root == "" {
		panic("static: Root directory is required")
	}
	if config.Index == "" {
		config.Index = "index.html"
	}

	// Resolve the root to an absolute path
	root, err := filepath.Abs(config.Root)
	if err != nil {
		panic(fmt.Sprintf("static: invalid root directory: %v", err))
	}

	cacheControl := ""
	if config.MaxAge > 0 {
		cacheControl = fmt.Sprintf("public, max-age=%d", config.MaxAge)
	}

	fileServer := http.FileServer(http.Dir(root))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only serve GET and HEAD requests
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}

			// Clean the path to prevent directory traversal
			upath := r.URL.Path
			if !strings.HasPrefix(upath, "/") {
				upath = "/" + upath
			}

			// Check if the file exists
			filePath := filepath.Join(root, filepath.FromSlash(upath))
			info, err := os.Stat(filePath)

			if err != nil {
				if config.SPA {
					// SPA fallback: serve the index file
					serveIndex(w, r, root, config.Index, cacheControl)
					return
				}
				// File not found, pass to next handler
				next.ServeHTTP(w, r)
				return
			}

			// If it's a directory, check for an index file
			if info.IsDir() {
				indexPath := filepath.Join(filePath, config.Index)
				if _, err := os.Stat(indexPath); err != nil {
					if config.Browse {
						// Directory listing enabled
						if cacheControl != "" {
							w.Header().Set("Cache-Control", cacheControl)
						}
						fileServer.ServeHTTP(w, r)
						return
					}
					if config.SPA {
						serveIndex(w, r, root, config.Index, cacheControl)
						return
					}
					// No index, no browse, no SPA — pass to next handler
					next.ServeHTTP(w, r)
					return
				}
			}

			// Set Cache-Control header
			if cacheControl != "" {
				w.Header().Set("Cache-Control", cacheControl)
			}

			// Serve the file
			fileServer.ServeHTTP(w, r)
		})
	}
}

// serveIndex serves the root index file for SPA fallback.
func serveIndex(w http.ResponseWriter, r *http.Request, root, index, cacheControl string) {
	indexPath := filepath.Join(root, index)
	if cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
	http.ServeFile(w, r, indexPath)
}
