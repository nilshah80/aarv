package aarv

import "net/http"

// SkipPaths returns mw wrapped so that requests whose URL path exactly
// matches any element of paths bypass mw entirely. The wrapped chain
// continues directly to the next middleware as if mw were not present.
//
// Use it for the recurring case of excluding observability endpoints
// from middleware that add overhead or change response semantics:
//
//	app.Use(aarv.SkipPaths(
//	    []string{"/health", "/ready", "/live", "/metrics"},
//	    compress.New(),
//	))
//
// mw accepts any of:
//
//   - aarv.Middleware
//   - aarv.NativeMiddleware (the return type of every plugin's New
//     constructor and of aarv.WrapMiddleware / aarv.RegisterNativeMiddleware)
//   - func(http.Handler) http.Handler (the untyped form of Middleware)
//
// The returned NativeMiddleware preserves the native fast path when the
// inner middleware supplies one, and propagates the inner middleware's
// Name so introspection still reports it. Distinct SkipPaths instances
// each carry their own native fn; no global registry is consulted, so
// multiple SkipPaths-wrapped middlewares in the same App do not
// collide.
//
// Path matching is exact and case-sensitive against r.URL.Path on the
// stdlib path and c.Path() on the native path. SkipPaths does not
// normalize trailing slashes, strip query strings, or apply pattern
// matching — feed it the canonical paths the rest of your stack uses.
//
// An empty paths slice returns the inner unchanged (wrapped into a
// NativeMiddleware) so SkipPaths can be wired in unconditionally
// without an empty-set special case at the caller. A nil mw panics
// immediately — the failure is loud rather than silent.
func SkipPaths(paths []string, mw any) NativeMiddleware {
	if mw == nil {
		panic("aarv.SkipPaths: mw must not be nil")
	}
	slot := coerceSlot(mw, "SkipPaths", 0)
	if len(paths) == 0 {
		return NativeMiddleware{Stdlib: slot.stdlib, Native: slot.native, Name: slot.name}
	}
	skip := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		skip[p] = struct{}{}
	}

	stdlib := Middleware(func(next http.Handler) http.Handler {
		wrapped := slot.stdlib(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := skip[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	})

	if slot.native == nil {
		// Inner has no native variant — chain downgrades to stdlib for
		// any route that includes this SkipPaths.
		return NativeMiddleware{Stdlib: stdlib, Name: slot.name}
	}

	native := MiddlewareFunc(func(next HandlerFunc) HandlerFunc {
		wrapped := slot.native(next)
		return func(c *Context) error {
			if _, ok := skip[c.Path()]; ok {
				return next(c)
			}
			return wrapped(c)
		}
	})
	return NativeMiddleware{Stdlib: stdlib, Native: native, Name: slot.name}
}
