// Example: JSON v2 codec — the experimental encoding/json/v2 implementation.
//
// The go-json-experiment/json library is a prototype of what encoding/json/v2
// might look like in a future Go release. It offers improved performance,
// better semantics, and more options than the current stdlib.
//
// Features:
// - ~2x faster than encoding/json
// - Stricter parsing (rejects duplicate keys by default)
// - Better handling of edge cases
// - More configuration options
//
// Run: go run main.go
// Test: curl http://localhost:8080/
package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/codec/jsonv2"
)

type Config struct {
	AppName     string            `json:"app_name"`
	Version     string            `json:"version"`
	Features    []string          `json:"features"`
	Settings    map[string]any    `json:"settings"`
	Enabled     bool              `json:"enabled"`
	LastUpdated time.Time         `json:"last_updated"`
}

type UpdateConfigReq struct {
	AppName  string         `json:"app_name" validate:"required,min=2"`
	Settings map[string]any `json:"settings"`
}

func main() {
	// Create the JSON v2 codec
	// This uses go-json-experiment/json which may become encoding/json/v2
	codec := jsonv2.New()

	app := aarv.New(
		aarv.WithCodec(codec),
		aarv.WithBanner(true),
	)

	app.Use(aarv.Recovery(), aarv.Logger())

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"codec":       "go-json-experiment/json (v2)",
			"description": "Experimental encoding/json/v2 prototype",
			"performance": "~2x faster than stdlib",
			"features": []string{
				"Stricter parsing",
				"Rejects duplicate keys",
				"Better edge case handling",
				"More configuration options",
			},
		})
	})

	app.Post("/config", aarv.Bind(func(c *aarv.Context, req UpdateConfigReq) (Config, error) {
		return Config{
			AppName:     req.AppName,
			Version:     "1.0.0",
			Features:    []string{"feature1", "feature2"},
			Settings:    req.Settings,
			Enabled:     true,
			LastUpdated: time.Now(),
		}, nil
	}))

	// Demonstrate strict parsing behavior
	app.Get("/config/default", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, Config{
			AppName:  "MyApp",
			Version:  "2.0.0",
			Features: []string{"auth", "caching", "logging"},
			Settings: map[string]any{
				"timeout_ms":  5000,
				"max_retries": 3,
				"debug":       false,
			},
			Enabled:     true,
			LastUpdated: time.Now(),
		})
	})

	// Complex nested structure to show encoding capabilities
	app.Get("/complex", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"nested": map[string]any{
				"level1": map[string]any{
					"level2": map[string]any{
						"level3": "deep value",
					},
				},
			},
			"mixed_array": []any{
				1,
				"two",
				3.0,
				true,
				nil,
				map[string]string{"key": "value"},
			},
			"unicode": "Hello, 世界! 🚀",
			"special": "<script>alert('xss')</script>",
		})
	})

	fmt.Println("JSON v2 Codec Example on :8080")
	fmt.Println("  GET  /              — codec info")
	fmt.Println("  POST /config        — update config (typed handler)")
	fmt.Println("  GET  /config/default — default config")
	fmt.Println("  GET  /complex       — complex nested structure")
	fmt.Println()
	fmt.Println("  JSON v2 (go-json-experiment) provides stricter parsing")
	fmt.Println("  and better performance than the current stdlib.")
	fmt.Println()
	fmt.Println("  Note: This uses the experimental json/v2 prototype that")
	fmt.Println("  may become the future encoding/json/v2 in Go.")

	app.Listen(":8080")
}
