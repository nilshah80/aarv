// Example: JSON Structured Logging with Full Request/Response Dump
//
// This example demonstrates two logging approaches:
// 1. Standard logger - logs metadata only (method, path, status, latency)
// 2. Verbose logger - logs full request/response including headers and body
//
// Run: go run main.go
// Test: curl -X POST http://localhost:8080/users -H "Content-Type: application/json" -d '{"name":"alice"}'
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/verboselog"
	"github.com/nilshah80/aarv/plugins/logger"
)

func main() {
	// Configure slog for JSON output
	// This affects both logger and verboselog plugins
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(jsonHandler))

	app := aarv.New()

	// Choose your logging strategy:

	// Option 1: Standard logger (metadata only, low overhead)
	// Output: {"time":"...","level":"INFO","msg":"request","method":"GET","path":"/users",...}
	// app.Use(logger.New())

	// Option 2: Full verbose logger (captures headers + body, higher overhead)
	// Output: {"time":"...","level":"INFO","msg":"http_dump","request_headers":{...},"request_body":"...",...}
	app.Use(verboselog.New(verboselog.Config{
		LogRequestBody:     true,
		LogResponseBody:    true,
		LogRequestHeaders:  true,
		LogResponseHeaders: true,
		MaxBodySize:        64 * 1024, // 64KB max
		SkipPaths:          []string{"/health"},
		// Sensitive data is auto-redacted
		SensitiveHeaders: []string{"Authorization", "Cookie", "X-API-Key"},
		SensitiveFields:  []string{"password", "token", "secret", "api_key"},
	}))

	// Example: Use standard logger for high-traffic routes
	// and verbose logger only for specific debug routes
	app.Group("/debug", func(g *aarv.RouteGroup) {
		g.Use(verboselog.New())
		g.Get("/inspect", func(c *aarv.Context) error {
			return c.JSON(200, map[string]string{"debug": "enabled"})
		})
	})

	// Regular routes with standard logger
	app.Group("/api", func(g *aarv.RouteGroup) {
		g.Use(logger.New(logger.Config{
			SkipPaths: []string{"/api/health"},
		}))

		g.Get("/users", func(c *aarv.Context) error {
			return c.JSON(200, []map[string]any{
				{"id": "1", "name": "Alice", "email": "alice@example.com"},
				{"id": "2", "name": "Bob", "email": "bob@example.com"},
			})
		})

		g.Post("/users", func(c *aarv.Context) error {
			var user struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			}
			if err := c.BindJSON(&user); err != nil {
				return c.JSON(400, map[string]string{"error": err.Error()})
			}
			return c.JSON(201, map[string]any{
				"id":    "3",
				"name":  user.Name,
				"email": user.Email,
			})
		})

		// Login endpoint - password will be redacted in logs
		g.Post("/login", func(c *aarv.Context) error {
			var creds struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := c.BindJSON(&creds); err != nil {
				return c.JSON(400, map[string]string{"error": err.Error()})
			}
			// In logs, password will show as [REDACTED]
			return c.JSON(200, map[string]string{
				"token":   "jwt-token-here",
				"message": "logged in",
			})
		})
	})

	// Health check - excluded from logging
	app.Get("/health", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	fmt.Println("Server starting on :8080")
	fmt.Println()
	fmt.Println("Example requests:")
	fmt.Println("  curl http://localhost:8080/api/users")
	fmt.Println("  curl -X POST http://localhost:8080/api/users -H 'Content-Type: application/json' -d '{\"name\":\"charlie\"}'")
	fmt.Println("  curl -X POST http://localhost:8080/api/login -H 'Content-Type: application/json' -d '{\"username\":\"alice\",\"password\":\"secret123\"}'")
	fmt.Println("  curl http://localhost:8080/health  # Not logged")
	fmt.Println()
	fmt.Println("Watch the JSON logs in stdout...")

	app.Listen(":8080")
}

// Example log output (JSON formatted):
//
// Standard logger output:
// {
//   "time": "2024-06-20T10:30:00Z",
//   "level": "INFO",
//   "msg": "request",
//   "method": "POST",
//   "path": "/api/users",
//   "status": 201,
//   "latency": "1.234ms",
//   "client_ip": "127.0.0.1",
//   "user_agent": "curl/8.0.1",
//   "bytes_out": 45,
//   "request_id": "01HX..."
// }
//
// Verbose logger output:
// {
//   "time": "2024-06-20T10:30:00Z",
//   "level": "INFO",
//   "msg": "http_dump",
//   "request_id": "01HX...",
//   "method": "POST",
//   "path": "/api/login",
//   "query": {},
//   "client_ip": "127.0.0.1",
//   "user_agent": "curl/8.0.1",
//   "content_type": "application/json",
//   "content_length": 45,
//   "request_headers": {
//     "Content-Type": "application/json",
//     "Authorization": "[REDACTED]"
//   },
//   "request_body": "{\"username\":\"alice\",\"password\":\"[REDACTED]\"}",
//   "status": 200,
//   "latency": "1.234ms",
//   "latency_ms": 1.234,
//   "response_headers": {
//     "Content-Type": "application/json"
//   },
//   "response_body": "{\"token\":\"[REDACTED]\",\"message\":\"logged in\"}",
//   "bytes_out": 42
// }
