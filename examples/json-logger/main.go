// Example: structured logging with ready-made verboselog presets.
//
// This example shows:
// 1. Standard logger middleware for metadata-only logging
// 2. Verbose logger presets for debug, production-safe, and minimal/perf usage
// 3. Built-in middleware/plugins that can use Aarv's native middleware path automatically
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/logger"
	"github.com/nilshah80/aarv/plugins/recover"
	"github.com/nilshah80/aarv/plugins/requestid"
	"github.com/nilshah80/aarv/plugins/verboselog"
)

func debugVerboseConfig() verboselog.Config {
	cfg := verboselog.DefaultConfig()
	cfg.SkipPaths = []string{"/health"}
	cfg.MaxBodySize = 64 * 1024
	return cfg
}

func productionSafeConfig() verboselog.Config {
	cfg := verboselog.DefaultConfig()
	cfg.SkipPaths = []string{"/health"}
	cfg.LogResponseBody = false
	cfg.RedactSensitive = true
	cfg.RedactSurfaces = []verboselog.RedactionSurface{
		verboselog.RedactRequestHeaders,
		verboselog.RedactResponseHeaders,
		verboselog.RedactQueryParams,
		verboselog.RedactRequestBody,
	}
	cfg.SensitiveHeaders = []string{"Authorization", "Cookie", "Set-Cookie", "X-API-Key"}
	cfg.SensitiveFields = []string{"password", "token", "secret", "api_key"}
	return cfg
}

func performanceMinimalConfig() verboselog.Config {
	cfg := verboselog.MinimalConfig()
	cfg.SkipPaths = []string{"/health"}
	cfg.LogLatencyMS = true
	return cfg
}

func main() {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(jsonHandler))

	app := aarv.New(aarv.WithBanner(true))

	// These built-ins now support Aarv's native middleware fast path automatically
	// when the whole route stack is native-compatible.
	app.Use(recover.New(), requestid.New())

	app.Get("/health", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	app.Group("/standard", func(g *aarv.RouteGroup) {
		g.Use(logger.New())
		g.Get("/users", func(c *aarv.Context) error {
			return c.JSON(http.StatusOK, []map[string]any{
				{"id": "1", "name": "Alice"},
				{"id": "2", "name": "Bob"},
			})
		})
	})

	app.Group("/debug", func(g *aarv.RouteGroup) {
		g.Use(verboselog.New(debugVerboseConfig()))
		g.Post("/login", func(c *aarv.Context) error {
			var body map[string]any
			if err := c.BindJSON(&body); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusOK, map[string]any{
				"message": "debug preset",
				"token":   "jwt-token-here",
				"input":   body,
			})
		})
	})

	app.Group("/production", func(g *aarv.RouteGroup) {
		g.Use(verboselog.New(productionSafeConfig()))
		g.Post("/login", func(c *aarv.Context) error {
			var body struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := c.BindJSON(&body); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusOK, map[string]any{
				"message": "production-safe preset",
				"token":   "jwt-token-here",
			})
		})
	})

	app.Group("/minimal", func(g *aarv.RouteGroup) {
		g.Use(verboselog.New(performanceMinimalConfig()))
		g.Get("/users", func(c *aarv.Context) error {
			return c.JSON(http.StatusOK, []map[string]any{
				{"id": "1", "name": "Alice"},
				{"id": "2", "name": "Bob"},
			})
		})
	})

	fmt.Println("Structured Logging Demo on :8080")
	fmt.Println("  GET  /standard/users      — metadata-only logger")
	fmt.Println("  POST /debug/login         — full verbose logging preset")
	fmt.Println("  POST /production/login    — production-safe redaction preset")
	fmt.Println("  GET  /minimal/users       — minimal/perf preset")
	fmt.Println("  GET  /health              — excluded from verbose examples")
	fmt.Println()
	fmt.Println("The built-in recover/requestid/logger/verboselog middleware can use")
	fmt.Println("Aarv's native middleware path automatically when the route stack allows it.")

	app.Listen(":8080")
}
