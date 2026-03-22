package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nilshah80/aarv"
)

type traceKey struct{}

func tracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = "generated-trace-id"
		}
		ctx := context.WithValue(r.Context(), traceKey{}, traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
		// Set false only if your middleware stack never relies on Aarv context
		// recovery from cloned requests or stdlib middleware that clones requests.
		// aarv.WithRequestContextBridge(false),
	)

	app.Use(tracingMiddleware)

	app.Get("/trace", func(c *aarv.Context) error {
		traceID, _ := c.Context().Value(traceKey{}).(string)

		reqTraceID := ""
		if reqCtx, ok := aarv.FromRequest(c.Request()); ok {
			reqTraceID = reqCtx.Path()
		}

		return c.JSON(http.StatusOK, map[string]any{
			"trace_id":                traceID,
			"aarv_from_request_works": reqTraceID != "",
			"path":                    c.Path(),
		})
	})

	fmt.Println("Middleware Bridge demo on :8080")
	fmt.Println("  GET /trace  with optional X-Trace-ID header")
	fmt.Println("  This demonstrates stdlib middleware using r.WithContext(...)")
	fmt.Println("  For Aarv-native middleware, see examples/custom-middleware")

	app.Listen(":8080")
}
