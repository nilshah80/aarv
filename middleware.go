package aarv

import "net/http"

// Middleware is the standard Go middleware signature.
type Middleware func(http.Handler) http.Handler

// MiddlewareFunc is a framework-specific middleware with access to Context.
type MiddlewareFunc func(next HandlerFunc) HandlerFunc

// WrapMiddleware converts a framework MiddlewareFunc to a stdlib Middleware.
func WrapMiddleware(fn MiddlewareFunc) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, ok := r.Context().Value(ctxKey{}).(*Context)
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
	}
}

// buildChain builds the middleware chain by wrapping the handler.
// First registered = outermost wrapper.
func buildChain(handler http.Handler, middlewares []Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// Recovery returns a middleware that recovers from panics.
func Recovery() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					ctx, ok := r.Context().Value(ctxKey{}).(*Context)
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
	}
}

// Logger returns a middleware that logs requests using slog.
func Logger() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, ok := r.Context().Value(ctxKey{}).(*Context)
			if ok {
				ctx.app.logger.Info("request",
					"method", r.Method,
					"path", r.URL.Path,
					"remote", r.RemoteAddr,
				)
			}
			next.ServeHTTP(w, r)
		})
	}
}
