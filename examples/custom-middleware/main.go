package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
)

type traceKey struct{}

func requestIDStdlib(name string) aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), traceKey{}, id)))
		})
	}
}

func requestIDNative(name string) aarv.MiddlewareFunc {
	return func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			id := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
			c.SetHeader("X-Request-ID", id)
			c.Set("request_id", id)
			c.SetContextValue(traceKey{}, id)
			return next(c)
		}
	}
}

func requestLoggerStdlib(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[stdlib] %s %s\n", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func requestLoggerNative(next aarv.HandlerFunc) aarv.HandlerFunc {
	return func(c *aarv.Context) error {
		fmt.Printf("[native] %s %s\n", c.Method(), c.Path())
		return next(c)
	}
}

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
	)

	dualRequestID := aarv.RegisterNativeMiddleware(
		requestIDStdlib("dual"),
		requestIDNative("dual"),
	)
	dualLogger := aarv.RegisterNativeMiddleware(requestLoggerStdlib, requestLoggerNative)

	app.Group("/stdlib", func(g *aarv.RouteGroup) {
		g.Use(requestIDStdlib("stdlib"), requestLoggerStdlib)
		g.Get("/hello", func(c *aarv.Context) error {
			traceID, _ := c.Context().Value(traceKey{}).(string)
			return c.JSON(http.StatusOK, map[string]any{
				"mode":            "stdlib-only",
				"trace_id":        traceID,
				"aarv_from_store": false,
			})
		})
	})

	app.Group("/native", func(g *aarv.RouteGroup) {
		g.Use(aarv.WrapMiddleware(requestIDNative("native")), aarv.WrapMiddleware(requestLoggerNative))
		g.Get("/hello", func(c *aarv.Context) error {
			traceID, _ := c.Context().Value(traceKey{}).(string)
			requestID, _ := c.Get("request_id")
			return c.JSON(http.StatusOK, map[string]any{
				"mode":            "native-only",
				"trace_id":        traceID,
				"aarv_from_store": requestID,
			})
		})
	})

	app.Group("/dual", func(g *aarv.RouteGroup) {
		g.Use(dualRequestID, dualLogger)
		g.Get("/hello", func(c *aarv.Context) error {
			traceID, _ := c.Context().Value(traceKey{}).(string)
			requestID, _ := c.Get("request_id")
			return c.JSON(http.StatusOK, map[string]any{
				"mode":            "dual-registered",
				"trace_id":        traceID,
				"aarv_from_store": requestID,
			})
		})
	})

	fmt.Println("Custom Middleware Demo on :8080")
	fmt.Println("  GET /stdlib/hello  — plain net/http middleware")
	fmt.Println("  GET /native/hello  — Aarv-native middleware via WrapMiddleware")
	fmt.Println("  GET /dual/hello    — stdlib + native registration in one middleware")
	fmt.Println()
	fmt.Println("Use the dual pattern when you want ecosystem compatibility plus Aarv's fast path.")

	app.Listen(":8080")
}
