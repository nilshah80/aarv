// Example: Middleware chain showcasing production-style plugin ordering.
// Demonstrates how to configure global, native, and route-specific middleware.
package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/bodylimit"
	"github.com/nilshah80/aarv/plugins/compress"
	"github.com/nilshah80/aarv/plugins/cors"
	"github.com/nilshah80/aarv/plugins/etag"
	"github.com/nilshah80/aarv/plugins/health"
	"github.com/nilshah80/aarv/plugins/logger"
	"github.com/nilshah80/aarv/plugins/requestid"
	"github.com/nilshah80/aarv/plugins/secure"
	"github.com/nilshah80/aarv/plugins/timeout"
)

func nativeMarker(next aarv.HandlerFunc) aarv.HandlerFunc {
	return func(c *aarv.Context) error {
		c.SetHeader("X-Native-Middleware", "enabled")
		return next(c)
	}
}

func requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "dev-key" {
			http.Error(w, `{"error":"missing api key"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
		aarv.WithReadTimeout(10*time.Second),
		aarv.WithWriteTimeout(10*time.Second),
	)

	app.Use(
		aarv.Recovery(),
		requestid.New(),
		logger.New(logger.Config{
			SkipPaths: []string{"/health", "/ready", "/live"},
		}),
		secure.New(secure.Config{
			XSSProtection:         "1; mode=block",
			ContentTypeNosniff:    "nosniff",
			XFrameOptions:         "DENY",
			HSTSMaxAge:            31536000,
			HSTSIncludeSubdomains: true,
			ReferrerPolicy:        "strict-origin-when-cross-origin",
			ContentSecurityPolicy: "default-src 'self'",
		}),
		cors.New(cors.Config{
			AllowOrigins:     []string{"https://example.com", "https://app.example.com"},
			AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions},
			AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key"},
			ExposeHeaders:    []string{"X-Request-ID", "X-Native-Middleware"},
			AllowCredentials: true,
			MaxAge:           3600,
		}),
		bodylimit.New(2<<20),
		timeout.New(5*time.Second),
		etag.New(etag.Config{Weak: true}),
		compress.New(compress.Config{MinSize: 512}),
		health.New(health.Config{
			ReadyCheck: func() bool { return true },
			LiveCheck:  func() bool { return true },
			Info: map[string]any{
				"service": "middleware-chain",
				"version": "dev",
			},
		}),
		aarv.WrapMiddleware(nativeMarker),
	)

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"message":    "Hello from middleware-chain example",
			"request_id": c.RequestID(),
		})
	})

	app.Get("/headers", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"info":       "Check response headers for security, CORS, ETag, and native middleware markers",
			"request_id": c.RequestID(),
		})
	})

	app.Post("/echo", func(c *aarv.Context) error {
		body, err := c.Body()
		if err != nil {
			return aarv.ErrBadRequest("failed to read body")
		}
		return c.Blob(http.StatusOK, "application/json", body)
	})

	app.Get("/panic", func(c *aarv.Context) error {
		panic("test panic: recovery middleware should catch this")
	})

	app.Get("/api/info", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"middleware": []string{
				"recovery", "requestid", "logger", "secure",
				"cors", "bodylimit", "timeout", "etag",
				"compress", "health", "native-marker",
			},
			"request_id": c.RequestID(),
		})
	})

	app.Get("/private", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"route": "private"})
	}, aarv.WithRouteMiddleware(requireAPIKey))

	fmt.Println("Middleware Chain Demo on :8080")
	fmt.Println("  GET  /           - hello with request ID")
	fmt.Println("  GET  /headers    - inspect security/CORS/ETag/native headers")
	fmt.Println("  POST /echo       - echo body (body limit = 2MB)")
	fmt.Println("  GET  /panic      - test panic recovery")
	fmt.Println("  GET  /api/info   - list active middleware")
	fmt.Println("  GET  /private    - route-specific middleware; requires X-API-Key: dev-key")
	fmt.Println("  GET  /health     - health check")
	fmt.Println("  GET  /ready      - readiness check")
	fmt.Println("  GET  /live       - liveness check")

	_ = app.Listen(":8080")
}
