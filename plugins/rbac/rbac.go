// Package rbac provides role-based access-control middleware for the aarv
// framework. It assumes an upstream authentication middleware has already
// run and exposed the caller's roles via a user-supplied RoleExtractor; the
// plugin then enforces "must have all of these roles" (RequireRoles) or
// "must have at least one of these roles" (RequireAnyRole) and returns 403
// Forbidden on a mismatch.
//
// # Relationship to authentication plugins
//
// rbac handles AUTHORIZATION only — it is not an authentication plugin and
// will not challenge with WWW-Authenticate or 401. Wire your auth plugin
// (plugins/jwt, plugins/bearer, plugins/apikey, plugins/basicauth, or your
// own) BEFORE rbac in the middleware chain so the extractor has an
// authenticated identity to read from. A request that reaches rbac without
// an identity (i.e. RoleExtractor returns nil/empty) is rejected with 403,
// matching the "authenticated but lacks privilege" semantics of RFC 7235 —
// callers that want a 401 in that case should put the auth plugin upstream
// to short-circuit before rbac runs.
//
// # Cross-path response parity
//
// rbac registers both a native (*aarv.HandlerFunc) and a stdlib
// (http.Handler) middleware path. Their denial responses are byte-identical
// (status, body bytes, Content-Type) only when ALL of the following hold:
//
//   - the framework uses its default ErrorHandler (no [WithErrorHandler]),
//   - the framework uses its default JSON codec / "application/json"
//     content type (no [WithCodec] customization that would change either
//     the serialized error shape or the Content-Type header), AND
//   - no [OnError] hook mutates the response (status, headers, or body).
//
// The native path returns [aarv.ErrForbidden] to the framework, which
// runs OnError, may invoke a user [WithErrorHandler], and serializes via
// the configured codec. The stdlib path bypasses all three: it writes
// "application/json" + the framework's default error JSON directly. Any
// of those three customizations therefore causes the two paths to
// diverge. If you need symmetric custom behavior across both paths,
// either stay on the framework defaults or install your customization at
// a layer outside the plugin (e.g. wrap the plugin's middleware
// yourself).
//
// # Example
//
//	authz := rbac.New(rbac.Config{
//	    RoleExtractor: func(c *aarv.Context) []string {
//	        if u, ok := bearer.From(c); ok {
//	            return u.(*User).Roles
//	        }
//	        return nil
//	    },
//	})
//
//	app.Get("/admin", adminHandler, aarv.WithRouteMiddleware(authz.RequireRoles("admin")))
//	app.Get("/posts/{id}/edit", editHandler,
//	    aarv.WithRouteMiddleware(authz.RequireAnyRole("admin", "editor")))
package rbac

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/nilshah80/aarv"
)

// RoleExtractor returns the roles assigned to the caller of the current
// request. The plugin invokes it on every protected request and treats a
// nil/empty return as "no roles" (which fails any non-empty role check). The
// extractor MUST be safe for concurrent use; it will be invoked from
// multiple goroutines.
type RoleExtractor func(c *aarv.Context) []string

// Config holds the shared configuration for an Authorizer. RoleExtractor is
// required; ErrorMessage falls back to "insufficient privileges".
type Config struct {
	// RoleExtractor pulls the caller's roles out of the request context.
	// Required; New panics if nil.
	RoleExtractor RoleExtractor

	// ErrorMessage is the message returned to clients on a 403. Default:
	// "insufficient privileges". The plugin does not include the missing
	// role names in the response — that would leak the policy surface to
	// unauthorized callers.
	ErrorMessage string
}

// Authorizer is the factory for role-check middlewares. It is constructed
// once per RoleExtractor and reused across many RequireRoles /
// RequireAnyRole calls so a single application can declare many distinct
// authorization policies on top of the same extractor.
type Authorizer struct {
	cfg Config
}

// New constructs an Authorizer. It panics if cfg.RoleExtractor is nil:
// silently passing every request through is unsafe for an authz plugin.
func New(cfg Config) *Authorizer {
	if cfg.RoleExtractor == nil {
		panic("rbac: Config.RoleExtractor is required")
	}
	if cfg.ErrorMessage == "" {
		cfg.ErrorMessage = "insufficient privileges"
	}
	return &Authorizer{cfg: cfg}
}

// RequireRoles returns a middleware that admits the request only when every
// role in the argument list is present in the caller's role set (logical
// AND). Calling with zero roles panics — "no constraint" should be expressed
// by omitting the middleware, not by registering a no-op.
func (a *Authorizer) RequireRoles(roles ...string) aarv.Middleware {
	if len(roles) == 0 {
		panic("rbac: RequireRoles requires at least one role")
	}
	required := snapshot(roles)
	return a.middleware(func(have []string) bool {
		for _, r := range required {
			if !slices.Contains(have, r) {
				return false
			}
		}
		return true
	})
}

// RequireAnyRole returns a middleware that admits the request when at least
// one role in the argument list is present in the caller's role set
// (logical OR). Calling with zero roles panics for the same reason as
// RequireRoles.
func (a *Authorizer) RequireAnyRole(roles ...string) aarv.Middleware {
	if len(roles) == 0 {
		panic("rbac: RequireAnyRole requires at least one role")
	}
	allowed := snapshot(roles)
	return a.middleware(func(have []string) bool {
		for _, r := range allowed {
			if slices.Contains(have, r) {
				return true
			}
		}
		return false
	})
}

// middleware builds a native + stdlib pair around the supplied predicate.
// Their denial responses are byte-identical (status 403, Content-Type
// "application/json", identical JSON body) only when the framework uses
// its default ErrorHandler, default JSON codec/content type, and no
// response-mutating OnError hook — see the package godoc for the full
// list and TestCustomErrorHandler_DivergesAcrossPaths for the
// machine-checked limitation. With any of those customizations the two
// paths diverge because the native path flows through the framework's
// response pipeline (OnError → ErrorHandler → codec) while the stdlib
// path writes JSON directly.
func (a *Authorizer) middleware(allow func(have []string) bool) aarv.Middleware {
	cfg := a.cfg

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if allow(cfg.RoleExtractor(c)) {
				return next(c)
			}
			return aarv.ErrForbidden(cfg.ErrorMessage)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := aarv.FromRequest(r)
			if !ok {
				// Fail closed: without an aarv.Context the RoleExtractor
				// (which takes *aarv.Context) cannot run. The only
				// supported deployment is rbac mounted inside an aarv.App;
				// treat anything else as misconfiguration that should
				// reject rather than silently admit.
				writeForbidden(w, r, cfg.ErrorMessage)
				return
			}
			// Re-bind c to the (possibly upstream-mutated) r before
			// invoking the extractor. An upstream stdlib middleware may
			// have done much more than r.WithContext: header injection,
			// path rewriting, body decompression, prefix stripping, etc.
			// (*Context).BindRequest is the right tool here — unlike
			// SetContext, it swaps the entire *http.Request on c rather
			// than only the context.Context, so c.Path() / c.Header() /
			// c.Context().Value(...) all reflect what the upstream chain
			// has been seeing. It also handles the registry-cleanup dance
			// for WithRequestContextBridge(false) so the returned r is
			// what the downstream chain (and writeForbidden's request_id
			// recovery) must consume.
			r = c.BindRequest(r)
			if allow(cfg.RoleExtractor(c)) {
				next.ServeHTTP(w, r)
				return
			}
			writeForbidden(w, r, cfg.ErrorMessage)
		})
	})
	return aarv.RegisterNativeMiddleware(m, native)
}

// snapshot copies the role slice so a caller mutating the original after
// middleware construction cannot change the policy out from under live
// requests.
func snapshot(roles []string) []string {
	out := make([]string, len(roles))
	copy(out, roles)
	return out
}

// errorBody mirrors aarv's framework default JSON error shape so the stdlib
// path and the framework's default ErrorHandler/codec emit byte-identical
// responses. A custom WithErrorHandler, WithCodec, or response-mutating
// OnError runs only on the native path and will produce a different body
// (or Content-Type) on the wire — see the package godoc.
type errorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func writeForbidden(w http.ResponseWriter, r *http.Request, message string) {
	requestID := ""
	if c, ok := aarv.FromRequest(r); ok {
		requestID = c.RequestID()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(errorBody{
		Error:     "forbidden",
		Message:   message,
		RequestID: requestID,
	})
}
