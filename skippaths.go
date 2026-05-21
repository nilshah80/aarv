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
// # Native fast path: not preserved (yet)
//
// The current implementation is stdlib-only. Any route whose chain
// includes a SkipPaths-wrapped middleware runs on aarv's stdlib chain,
// even when every other middleware in the chain has a native pair.
// Background: aarv's native middleware registry keys on
// reflect.ValueOf(m).Pointer(), which returns the *code pointer* for
// func values — closures from the same source function literal share a
// registry slot. A wrapper helper like SkipPaths produces its stdlib
// closure from the same source each call, so two SkipPaths
// registrations in the same App would silently overwrite each other's
// native variant. Rather than ship that footgun, this version skips
// native registration entirely; the chain downgrades to stdlib for
// routes that include SkipPaths.
//
// If you cannot afford the stdlib downgrade for a specific middleware,
// many plugins ship a per-instance SkipPaths config field
// (`prometheus.Config.SkipPaths`, `logger.Config.SkipPaths`,
// `compress.Config.SkipPaths`, `etag.Config.SkipPaths`,
// `verboselog.Config.SkipPaths`) that performs the skip on the native
// path inside the plugin. Prefer those for hot paths until the
// registry keying is fixed and this helper can re-introduce the
// native variant.
//
// Path matching is exact and case-sensitive against r.URL.Path.
// SkipPaths does not normalize trailing slashes, strip query strings,
// or apply pattern matching — feed it the canonical paths the rest of
// your stack uses.
//
// An empty paths slice returns mw unchanged so SkipPaths can be wired
// in unconditionally without an empty-set special case at the caller.
// A nil mw returns nil — the misuse is loud rather than silent.
func SkipPaths(paths []string, mw Middleware) Middleware {
	if len(paths) == 0 || mw == nil {
		return mw
	}
	skip := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		skip[p] = struct{}{}
	}

	return Middleware(func(next http.Handler) http.Handler {
		// Build the wrapped path once; the per-request check just
		// chooses between `next` (skipped) and `wrapped` (run through
		// mw and then on to next).
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := skip[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	})
}
