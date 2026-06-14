package aarv

import (
	"fmt"
	"net/http"
	"reflect"
	"runtime"
)

// Middleware is the standard Go middleware signature.
type Middleware func(http.Handler) http.Handler

// MiddlewareFunc is a framework-specific middleware with access to Context.
type MiddlewareFunc func(next HandlerFunc) HandlerFunc

// NativeMiddleware bundles a stdlib Middleware with its aarv-native
// MiddlewareFunc counterpart. Each value carries its own identity, so
// multiple instances of the same wrapper helper or plugin do not
// interfere with each other's native fast-path registration.
//
// Plugin constructors that wire both paths return NativeMiddleware via
// RegisterNativeMiddleware. Users pass the value to App.Use,
// RouteGroup.Use, PluginContext.Use, WithRouteMiddleware, or SkipPaths
// — all of which accept any of {Middleware, NativeMiddleware,
// func(http.Handler) http.Handler}.
//
// Stdlib is required; Use-time validation panics on a nil Stdlib so
// misuse is caught at registration rather than at chain build. Native
// is optional — a NativeMiddleware with Native == nil registers as
// stdlib-only and downgrades any route whose chain includes it to the
// stdlib path.
type NativeMiddleware struct {
	Stdlib Middleware
	Native MiddlewareFunc

	// Name is an optional debug label surfaced by RouteInfo.Middleware and
	// App.GlobalMiddleware. Set it directly or via NamedMiddleware. When
	// empty, introspection falls back to a best-effort reflect-derived label
	// that is NOT a stable contract.
	Name string
}

// RegisterNativeMiddleware bundles a stdlib middleware with its
// aarv-native counterpart. No global registry is consulted; each call
// produces an independent NativeMiddleware value.
func RegisterNativeMiddleware(m Middleware, fn MiddlewareFunc) NativeMiddleware {
	return NativeMiddleware{Stdlib: m, Native: fn}
}

// NamedMiddleware attaches a stable debug name to any middleware (Middleware,
// NativeMiddleware, or func(http.Handler) http.Handler) for introspection via
// RouteInfo.Middleware and App.GlobalMiddleware. The name is the only stable
// identity contract; unnamed middleware fall back to a best-effort
// reflect-derived label. Panics on nil or an unsupported type, like Use.
func NamedMiddleware(name string, mw any) NativeMiddleware {
	slot := coerceSlot(mw, "NamedMiddleware", 0)
	return NativeMiddleware{Stdlib: slot.stdlib, Native: slot.native, Name: name}
}

// WrapMiddleware converts a framework MiddlewareFunc into a
// NativeMiddleware with both paths wired. A nil fn yields a pass-through
// stdlib middleware with no native variant.
func WrapMiddleware(fn MiddlewareFunc) NativeMiddleware {
	if fn == nil {
		return NativeMiddleware{
			Stdlib: func(next http.Handler) http.Handler { return next },
		}
	}
	m := Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, ok := contextFromRequest(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			wrappedNext := func(c *Context) error {
				next.ServeHTTP(w, r)
				return nil
			}
			if err := fn(wrappedNext)(ctx); err != nil {
				ctx.app.handleError(ctx, err)
			}
		})
	})
	return NativeMiddleware{Stdlib: m, Native: fn}
}

// middlewareSlot is the internal storage shape used by App, RouteGroup,
// and routeConfig in place of the historical []Middleware. A slot with a
// non-nil native lets buildNativeChain compose the fast path without
// any lookup. A slot with native == nil forces the chain builder to
// downgrade to stdlib for any route that includes it.
type middlewareSlot struct {
	stdlib Middleware
	native MiddlewareFunc
	name   string
}

// middlewareNames maps slots to display labels for introspection. Each entry
// uses the explicit name when set, otherwise a best-effort reflect-derived
// label (see middlewareLabel). Returns nil for an empty slice. Always
// allocates a fresh slice, so callers may mutate the result freely.
func middlewareNames(slots []middlewareSlot) []string {
	if len(slots) == 0 {
		return nil
	}
	names := make([]string, len(slots))
	for i, s := range slots {
		if s.name != "" {
			names[i] = s.name
		} else {
			names[i] = middlewareLabel(s.stdlib)
		}
	}
	return names
}

// middlewareLabel derives a best-effort debug label for an unnamed middleware
// from its underlying function symbol. For closures this is typically the
// constructor's "...func1" form — useful for debugging, not a stable contract.
func middlewareLabel(m Middleware) string {
	if m == nil {
		return "unknown"
	}
	if fn := runtime.FuncForPC(reflect.ValueOf(m).Pointer()); fn != nil {
		return fn.Name()
	}
	return "unknown"
}

// coerceSlot is the type-switch helper shared by App.Use, RouteGroup.Use,
// PluginContext.Use, WithRouteMiddleware, and SkipPaths. It validates
// inputs at registration time so misuse is caught loudly, not silently
// during chain build.
//
// Accepted argument types:
//   - Middleware (or its untyped equivalent func(http.Handler) http.Handler)
//   - NativeMiddleware
//
// Anything else panics with a message naming the caller, the argument
// index, and the unsupported type.
func coerceSlot(arg any, call string, index int) middlewareSlot {
	switch v := arg.(type) {
	case nil:
		panic(fmt.Sprintf("aarv: %s: argument %d is nil", call, index))
	case Middleware:
		if v == nil {
			panic(fmt.Sprintf("aarv: %s: argument %d is a nil Middleware", call, index))
		}
		return middlewareSlot{stdlib: v}
	case func(http.Handler) http.Handler:
		if v == nil {
			panic(fmt.Sprintf("aarv: %s: argument %d is a nil middleware function", call, index))
		}
		return middlewareSlot{stdlib: Middleware(v)}
	case NativeMiddleware:
		if v.Stdlib == nil {
			panic(fmt.Sprintf("aarv: %s: argument %d is NativeMiddleware with nil Stdlib", call, index))
		}
		return middlewareSlot{stdlib: v.Stdlib, native: v.Native, name: v.Name}
	default:
		panic(fmt.Sprintf("aarv: %s: argument %d has unsupported type %T; want Middleware, NativeMiddleware, or func(http.Handler) http.Handler", call, index, arg))
	}
}

// coerceSlots applies coerceSlot to a variadic any slice. The returned
// []middlewareSlot has the same length and order as args.
func coerceSlots(args []any, call string) []middlewareSlot {
	slots := make([]middlewareSlot, len(args))
	for i, a := range args {
		slots[i] = coerceSlot(a, call, i)
	}
	return slots
}

// buildChain builds the stdlib middleware chain by wrapping the handler.
// First registered = outermost wrapper.
func buildChain(handler http.Handler, slots []middlewareSlot) http.Handler {
	for i := len(slots) - 1; i >= 0; i-- {
		handler = slots[i].stdlib(handler)
	}
	return handler
}

// buildNativeChain builds the framework-native chain. Returns (nil,
// false) the first time it encounters a slot with a nil native fn — the
// caller is expected to fall back to the stdlib chain in that case.
func buildNativeChain(handler HandlerFunc, slots []middlewareSlot) (HandlerFunc, bool) {
	chain := handler
	for i := len(slots) - 1; i >= 0; i-- {
		if slots[i].native == nil {
			return nil, false
		}
		chain = slots[i].native(chain)
	}
	return chain, true
}

// allNative reports whether every slot has a non-nil native fn. Used by
// dispatch.buildRouteChainFast as the gate that decides whether the
// native fast chain is even worth attempting.
func allNative(slots []middlewareSlot) bool {
	for i := range slots {
		if slots[i].native == nil {
			return false
		}
	}
	return true
}

// Recovery returns a middleware that recovers from panics on both the
// stdlib and aarv-native paths.
func Recovery() NativeMiddleware {
	native := MiddlewareFunc(func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			defer func() {
				if rec := recover(); rec != nil {
					c.app.logger.Error("panic recovered",
						"error", rec,
						"method", c.req.Method,
						"path", c.req.URL.Path,
					)
					c.app.handleError(c, ErrInternal(nil).WithDetail("panic recovered"))
				}
			}()
			return next(c)
		}
	})
	m := Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					ctx, ok := contextFromRequest(r)
					if ok {
						ctx.app.logger.Error("panic recovered",
							"error", rec,
							"method", r.Method,
							"path", r.URL.Path,
						)
						// Write to recovery's own ResponseWriter, bypassing any
						// broken middleware wrappers (etag, compress) that panicked.
						ctx.res = w
						ctx.app.handleError(ctx, ErrInternal(nil).WithDetail("panic recovered"))
					} else {
						w.WriteHeader(http.StatusInternalServerError)
					}
				}
			}()
			next.ServeHTTP(w, r)
		})
	})
	return NativeMiddleware{Stdlib: m, Native: native}
}

// Logger returns a middleware that logs each request via the framework
// slog logger. Wires both the stdlib and aarv-native paths.
func Logger() NativeMiddleware {
	native := MiddlewareFunc(func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			c.app.logger.Info("request",
				"method", c.req.Method,
				"path", c.req.URL.Path,
				"remote", c.req.RemoteAddr,
			)
			return next(c)
		}
	})
	m := Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, ok := contextFromRequest(r)
			if ok {
				ctx.app.logger.Info("request",
					"method", r.Method,
					"path", r.URL.Path,
					"remote", r.RemoteAddr,
				)
			}
			next.ServeHTTP(w, r)
		})
	})
	return NativeMiddleware{Stdlib: m, Native: native}
}
