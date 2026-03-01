// Example: Middleware chain showcasing all built-in plugins.
// Demonstrates how to configure and compose multiple middleware.
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

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
		aarv.WithReadTimeout(10*time.Second),
		aarv.WithWriteTimeout(10*time.Second),
	)

	// --- Global middleware stack (order matters) ---

	app.Use(
		// 1. Recovery: catch panics, return 500 JSON
		aarv.Recovery(),

		// 2. Request ID: generate/propagate X-Request-ID
		requestid.New(),

		// 3. Structured logging with latency, status, client IP
		logger.New(logger.Config{
			SkipPaths: []string{"/health", "/ready", "/live"},
		}),

		// 4. Security headers: XSS, clickjack, HSTS
		secure.New(secure.Config{
			XSSProtection:         "1; mode=block",
			ContentTypeNosniff:    "nosniff",
			XFrameOptions:         "DENY",
			HSTSMaxAge:            31536000,
			HSTSIncludeSubdomains: true,
			ReferrerPolicy:        "strict-origin-when-cross-origin",
			ContentSecurityPolicy: "default-src 'self'",
		}),

		// 5. CORS: cross-origin access control
		cors.New(cors.Config{
			AllowOrigins:     []string{"https://example.com", "https://app.example.com"},
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
			ExposeHeaders:    []string{"X-Request-ID"},
			AllowCredentials: true,
			MaxAge:           3600,
		}),

		// 6. Body size limit: 2 MB max
		bodylimit.New(2<<20),

		// 7. Request timeout: 5 seconds
		timeout.New(5*time.Second),

		// 8. ETag: auto 304 responses for GET
		etag.New(etag.Config{Weak: true}),

		// 9. Gzip compression for large responses
		compress.New(compress.Config{
			MinSize: 512,
		}),

		// 10. Health/ready/live endpoints
		health.New(health.Config{
			HealthPath: "/health",
			ReadyPath:  "/ready",
			LivePath:   "/live",
			ReadyCheck: func() bool { return true },
			LiveCheck:  func() bool { return true },
		}),
	)

	// --- Routes ---

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"message":    "Hello from middleware-chain example",
			"request_id": c.RequestID(),
		})
	})

	app.Get("/headers", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"info":       "Check response headers for security/CORS/ETag headers",
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
		panic("test panic — recovery middleware should catch this")
	})

	app.Get("/api/info", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"middleware": []string{
				"recovery", "requestid", "logger", "secure",
				"cors", "bodylimit", "timeout", "etag",
				"compress", "health",
			},
			"request_id": c.RequestID(),
		})
	})

	fmt.Println("Middleware Chain Demo on :8080")
	fmt.Println("  GET  /           — hello with request ID")
	fmt.Println("  GET  /headers    — inspect security/CORS headers")
	fmt.Println("  POST /echo       — echo body (body limit = 2MB)")
	fmt.Println("  GET  /panic      — test panic recovery")
	fmt.Println("  GET  /api/info   — list active middleware")
	fmt.Println("  GET  /health     — health check")
	fmt.Println("  GET  /ready      — readiness check")
	fmt.Println("  GET  /live       — liveness check")

	app.Listen(":8080")
}
