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
	"github.com/nilshah80/aarv/internal/headerbuffer"
)

var filepathAbs = filepath.Abs

// Config holds configuration for the static file middleware.
type Config struct {
	// Root is the directory to serve files from. Required.
	Root string

	// Prefix is the URL path prefix to strip before looking up files.
	// For example, if Prefix is "/static" and a request comes in for
	// "/static/js/app.js", the middleware will look for "js/app.js" in Root.
	// Default: "" (no prefix stripping).
	Prefix string

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

// notFoundInterceptor wraps a ResponseWriter to detect 404 responses from
// http.FileServer without pre-statting every request. When a 404 is detected,
// the intercepted flag is set and no bytes are written to the underlying writer.
// Headers are buffered locally until commit so that a suppressed 404 does not
// leak stale headers into the fallback response.
type notFoundInterceptor struct {
	http.ResponseWriter
	headers     headerbuffer.Buffer
	intercepted bool
	committed   bool
}

func (w *notFoundInterceptor) Header() http.Header {
	return w.headers.Header()
}

func (w *notFoundInterceptor) WriteHeader(code int) {
	if code == http.StatusNotFound {
		w.intercepted = true
		return
	}
	w.copyHeaders()
	w.committed = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *notFoundInterceptor) Write(b []byte) (int, error) {
	if w.intercepted {
		return len(b), nil // discard
	}
	if !w.committed {
		w.copyHeaders()
		w.committed = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *notFoundInterceptor) copyHeaders() {
	w.headers.CopyTo(w.ResponseWriter.Header())
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
	root, err := filepathAbs(config.Root)
	if err != nil {
		panic(fmt.Sprintf("static: invalid root directory: %v", err))
	}

	cacheControl := ""
	if config.MaxAge > 0 {
		cacheControl = fmt.Sprintf("public, max-age=%d", config.MaxAge)
	}

	fileServer := http.FileServer(http.Dir(root))
	// When Browse is disabled (the default), use a filesystem that hides
	// directory listings so http.FileServer returns 404 for directories
	// without an index file, instead of rendering a listing.
	if !config.Browse {
		fileServer = http.FileServer(noBrowseFS{root: root, index: config.Index})
	}

	prefix := config.Prefix
	needSPAorBrowse := config.SPA || config.Browse

	m := func(next http.Handler) http.Handler {
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

			// Strip prefix if configured
			if prefix != "" {
				if !strings.HasPrefix(upath, prefix) {
					next.ServeHTTP(w, r)
					return
				}
				upath = strings.TrimPrefix(upath, prefix)
				if upath == "" || upath[0] != '/' {
					upath = "/" + upath
				}
			}

			if !needSPAorBrowse {
				// Fast path: no SPA, no browse — let http.FileServer
				// handle everything. Use a 404 interceptor so we can
				// fall through to next handler on miss.
				nfi := &notFoundInterceptor{ResponseWriter: w}
				if cacheControl != "" {
					nfi.Header().Set("Cache-Control", cacheControl)
				}
				fileServer.ServeHTTP(nfi, r)
				if nfi.intercepted {
					// 404 was suppressed — headers were buffered and never
					// copied, so the real writer is clean for the fallback.
					next.ServeHTTP(w, r)
				}
				return
			}

			// SPA/browse path: need to check file existence for fallback decisions.
			filePath := filepath.Join(root, filepath.FromSlash(upath))
			info, statErr := os.Stat(filePath)

			if statErr != nil {
				if config.SPA {
					serveIndex(w, r, root, config.Index, cacheControl)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if info.IsDir() {
				indexPath := filepath.Join(filePath, config.Index)
				if _, err := os.Stat(indexPath); err != nil {
					if config.Browse {
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
					next.ServeHTTP(w, r)
					return
				}
			}

			if cacheControl != "" {
				w.Header().Set("Cache-Control", cacheControl)
			}
			fileServer.ServeHTTP(w, r)
		})
	}

	native := func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			req := c.Request()

			if req.Method != http.MethodGet && req.Method != http.MethodHead {
				return next(c)
			}

			upath := req.URL.Path

			if prefix != "" {
				if !strings.HasPrefix(upath, prefix) {
					return next(c)
				}
				upath = strings.TrimPrefix(upath, prefix)
				if upath == "" || upath[0] != '/' {
					upath = "/" + upath
				}
			}

			if !needSPAorBrowse {
				// Fast path: no SPA, no browse — let http.FileServer handle it.
				nfi := &notFoundInterceptor{ResponseWriter: c.Response()}
				if cacheControl != "" {
					nfi.Header().Set("Cache-Control", cacheControl)
				}
				fileServer.ServeHTTP(nfi, req)
				if nfi.intercepted {
					return next(c)
				}
				return nil
			}

			// SPA/browse path
			filePath := filepath.Join(root, filepath.FromSlash(upath))
			info, statErr := os.Stat(filePath)
			if statErr != nil {
				if config.SPA {
					serveIndex(c.Response(), req, root, config.Index, cacheControl)
					return nil
				}
				return next(c)
			}

			if info.IsDir() {
				indexPath := filepath.Join(filePath, config.Index)
				if _, err := os.Stat(indexPath); err != nil {
					if config.Browse {
						if cacheControl != "" {
							c.SetHeader("Cache-Control", cacheControl)
						}
						fileServer.ServeHTTP(c.Response(), req)
						return nil
					}
					if config.SPA {
						serveIndex(c.Response(), req, root, config.Index, cacheControl)
						return nil
					}
					return next(c)
				}
			}

			if cacheControl != "" {
				c.SetHeader("Cache-Control", cacheControl)
			}
			fileServer.ServeHTTP(c.Response(), req)
			return nil
		}
	}

	return aarv.RegisterNativeMiddleware(m, native)
}

// serveIndex serves the root index file for SPA fallback.
func serveIndex(w http.ResponseWriter, r *http.Request, root, index, cacheControl string) {
	indexPath := filepath.Join(root, index)
	if cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
	http.ServeFile(w, r, indexPath)
}

// noBrowseFS wraps http.Dir to suppress directory listings and support
// custom index filenames. http.FileServer hardcodes "index.html", so when
// a custom index is configured, this FS transparently serves the custom
// index file in place of the missing "index.html".
type noBrowseFS struct {
	root  string
	index string
}

func (fs noBrowseFS) Open(name string) (http.File, error) {
	dir := http.Dir(fs.root)

	// When http.FileServer asks for "index.html" inside a directory and the
	// configured index is different, serve the custom index file instead.
	if fs.index != "index.html" && strings.HasSuffix(name, "/index.html") {
		customName := strings.TrimSuffix(name, "index.html") + fs.index
		if f, err := dir.Open(customName); err == nil {
			return f, nil
		}
		// Custom index not found either — fall through to normal Open
		// which will fail and prevent directory listing.
	}

	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.IsDir() {
		idx, err := dir.Open(name + "/" + fs.index)
		if err != nil {
			_ = f.Close()
			return nil, os.ErrNotExist
		}
		_ = idx.Close()
	}
	return f, nil
}
