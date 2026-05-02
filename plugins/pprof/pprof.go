// Package pprof exposes Go's standard net/http/pprof debugging endpoints
// through aarv. The package is stdlib-only and lives in the root module.
//
// # Security
//
// pprof exposes process internals — heap and CPU profiles, goroutine stacks,
// memory allocator state. It must not be reachable by untrusted clients in
// production. The Config.AuthMiddleware field gates the entire endpoint
// surface; callers running pprof in shared environments should always set
// it to an authenticator (api-key, basic-auth, JWT, or a custom IP allow-list).
//
// Two entry points are provided:
//
//   - Handler(cfg) returns an http.Handler suitable for App.Mount, e.g.
//     app.Mount("/debug/pprof/", pprof.Handler(cfg)). This is the canonical
//     path: pprof is route-shaped, not request-flow-shaped.
//   - New(cfg) returns an aarv.Middleware that intercepts requests whose
//     path begins with cfg.Prefix and serves them out of pprof. Useful in
//     middleware-stack designs.
//
// New registers the matching native middleware via
// aarv.RegisterNativeMiddleware so the framework's fast path stays intact.
// Handler returns a plain http.Handler with no native counterpart — it is
// intended for App.Mount-style mounting and the per-request overhead of
// stdlib dispatch is acceptable for debugging endpoints.
package pprof

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"strings"

	"github.com/nilshah80/aarv"
)

// DefaultPrefix is the conventional Go pprof mount path.
const DefaultPrefix = "/debug/pprof"

// Config tunes the plugin.
type Config struct {
	// Prefix is the URL prefix at which pprof is mounted. Defaults to
	// DefaultPrefix. Must start with "/" and must not end with "/" — the
	// trailing slash is added automatically by the canonical sub-route paths
	// served from pprof.Index.
	Prefix string

	// AuthMiddleware, when non-nil, is applied to every pprof endpoint
	// before the pprof handler runs. Strongly recommended in any environment
	// where pprof is reachable from outside the operator's machine.
	AuthMiddleware aarv.Middleware

	// SkipPaths excludes specific request paths from the pprof handler even
	// when they match Prefix. Rarely needed; useful for carving out a
	// liveness endpoint that lives under the same prefix.
	SkipPaths []string
}

// Handler returns an http.Handler that serves the canonical pprof endpoints
// rooted at cfg.Prefix. Mount it via App.Mount:
//
//	app.Mount(pprof.DefaultPrefix, pprof.Handler(pprof.Config{}))
//
// App.Mount strips the mount prefix before invoking the handler — a request
// to "/debug/pprof/cmdline" arrives at the handler with URL.Path == "/cmdline".
// Handler restores the configured prefix internally before consulting its
// route table, so:
//   - the registered routes (which carry cfg.Prefix) match
//   - stdlib pprof.Index, which hardcodes "/debug/pprof/" when dispatching
//     sub-profiles like /heap or /goroutine, sees its expected URL.Path shape
//
// pprof.Index's hardcoded prefix means sub-profile dispatch only works with
// cfg.Prefix == DefaultPrefix ("/debug/pprof"). Custom prefixes still serve
// the four named endpoints (cmdline, profile, symbol, trace) and the index
// page itself, but cannot dispatch /heap, /goroutine, /allocs, /mutex,
// /block, /threadcreate from a custom prefix.
//
// Handler panics if Prefix is malformed (empty, missing leading slash, or
// containing a trailing slash).
func Handler(cfg Config) http.Handler {
	cfg = normalize(cfg)
	mux := http.NewServeMux()
	registerPprofRoutes(mux, cfg.Prefix)

	skip := skipSet(cfg.SkipPaths)
	var h http.Handler = mux
	if len(skip) > 0 {
		next := h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, blocked := skip[r.URL.Path]; blocked {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	if cfg.AuthMiddleware != nil {
		h = cfg.AuthMiddleware(h)
	}

	// Wrap with prefix restoration so App.Mount-stripped paths reach the
	// inner mux with their original cfg.Prefix-rooted shape. Direct
	// invocations (path already prefixed) pass through unchanged.
	inner := h
	prefix := cfg.Prefix
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, prefix) {
			inner.ServeHTTP(w, r)
			return
		}
		// App.Mount stripped the prefix. Reconstruct it for the inner mux
		// and for pprof.Index's hardcoded prefix check.
		r2 := r.Clone(r.Context())
		urlCopy := *r.URL
		stripped := r.URL.Path
		if stripped == "" {
			urlCopy.Path = prefix
		} else if stripped[0] == '/' {
			urlCopy.Path = prefix + stripped
		} else {
			urlCopy.Path = prefix + "/" + stripped
		}
		r2.URL = &urlCopy
		inner.ServeHTTP(w, r2)
	})
}

// New returns aarv middleware that intercepts requests whose path lies under
// cfg.Prefix and dispatches them to the pprof endpoint set. Requests outside
// the prefix pass through to next unchanged.
//
// New panics if Prefix is malformed.
func New(cfg Config) aarv.Middleware {
	cfg = normalize(cfg)
	pprofHandler := Handler(cfg)
	prefix := cfg.Prefix

	stdlib := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if matchesPrefix(r.URL.Path, prefix) {
				pprofHandler.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if matchesPrefix(c.Path(), prefix) {
				pprofHandler.ServeHTTP(c.Response(), c.Request())
				return nil
			}
			return next(c)
		}
	})

	return aarv.RegisterNativeMiddleware(stdlib, native)
}

// matchesPrefix reports whether path lies under prefix. The match is exact
// at prefix or after a "/" boundary so that /debug/pprof and
// /debug/pprof/heap match while /debug/pprofX does not.
func matchesPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := path[len(prefix):]
	return rest != "" && rest[0] == '/'
}

func normalize(cfg Config) Config {
	if cfg.Prefix == "" {
		cfg.Prefix = DefaultPrefix
	}
	if !strings.HasPrefix(cfg.Prefix, "/") {
		panic(fmt.Sprintf("pprof: Prefix must begin with '/', got %q", cfg.Prefix))
	}
	if cfg.Prefix == "/" {
		panic("pprof: Prefix must not be the bare root '/'")
	}
	if strings.HasSuffix(cfg.Prefix, "/") {
		panic(fmt.Sprintf("pprof: Prefix must not end with '/', got %q", cfg.Prefix))
	}
	return cfg
}

func skipSet(paths []string) map[string]struct{} {
	if len(paths) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		m[p] = struct{}{}
	}
	return m
}

// registerPprofRoutes attaches the five canonical pprof endpoints plus
// the index handler on mux, all rooted at prefix. We register concrete
// paths rather than the bare prefix because pprof.Index inspects the URL
// path to choose a sub-profile, and ServeMux's prefix-tree matching
// strips the trailing slash for exact-match lookups.
func registerPprofRoutes(mux *http.ServeMux, prefix string) {
	// pprof.Index serves both the directory listing and the named profiles
	// (heap, allocs, goroutine, mutex, block, threadcreate). It dispatches
	// internally based on the last path segment.
	mux.HandleFunc(prefix+"/", pprof.Index)
	mux.HandleFunc(prefix, pprof.Index)
	mux.HandleFunc(prefix+"/cmdline", pprof.Cmdline)
	mux.HandleFunc(prefix+"/profile", pprof.Profile)
	mux.HandleFunc(prefix+"/symbol", pprof.Symbol)
	mux.HandleFunc(prefix+"/trace", pprof.Trace)
}
