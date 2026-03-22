package aarv

import (
	"net/http"
	"reflect"
	"sync"
)

// Middleware is the standard Go middleware signature.
type Middleware func(http.Handler) http.Handler

// MiddlewareFunc is a framework-specific middleware with access to Context.
type MiddlewareFunc func(next HandlerFunc) HandlerFunc

var nativeMiddlewareRegistry sync.Map

func registerNativeMiddleware(m Middleware, fn MiddlewareFunc) Middleware {
	if fn != nil {
		nativeMiddlewareRegistry.Store(reflect.ValueOf(m).Pointer(), fn)
	}
	return m
}

// RegisterNativeMiddleware associates an Aarv-native middleware implementation
// with a standard net/http middleware. It enables the runtime to use a faster
// native exact-route path when every middleware in the chain provides one.
func RegisterNativeMiddleware(m Middleware, fn MiddlewareFunc) Middleware {
	return registerNativeMiddleware(m, fn)
}

func nativeMiddlewareFunc(m Middleware) (MiddlewareFunc, bool) {
	fn, ok := nativeMiddlewareRegistry.Load(reflect.ValueOf(m).Pointer())
	if !ok {
		return nil, false
	}
	mf, ok := fn.(MiddlewareFunc)
	return mf, ok
}

// WrapMiddleware converts a framework MiddlewareFunc to a stdlib Middleware.
func WrapMiddleware(fn MiddlewareFunc) Middleware {
	if fn == nil {
		return func(next http.Handler) http.Handler { return next }
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
	return registerNativeMiddleware(m, fn)
}

// buildChain builds the middleware chain by wrapping the handler.
// First registered = outermost wrapper.
func buildChain(handler http.Handler, middlewares []Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

func buildNativeChain(handler HandlerFunc, middlewares []Middleware) (HandlerFunc, bool) {
	chain := handler
	for i := len(middlewares) - 1; i >= 0; i-- {
		mw, ok := nativeMiddlewareFunc(middlewares[i])
		if !ok {
			return nil, false
		}
		chain = mw(chain)
	}
	return chain, true
}

// Recovery returns a middleware that recovers from panics.
func Recovery() Middleware {
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
	return registerNativeMiddleware(m, native)
}

// Logger returns a middleware that logs requests using slog.
func Logger() Middleware {
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
	return registerNativeMiddleware(m, native)
}
