// Example: Segmentio codec — high-performance drop-in JSON replacement.
//
// The segmentio/encoding/json library is API-compatible with encoding/json
// but provides ~2-3x faster encoding/decoding through optimized reflection
// and allocation-free encoding paths.
//
// Run: go run main.go
// Test: curl http://localhost:8080/
package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/codec/segmentio"
)

type User struct {
	ID        int       `json:"id"`
	Name      string    `json:"name" validate:"required,min=2"`
	Email     string    `json:"email" validate:"required,email"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateUserReq struct {
	Name  string `json:"name" validate:"required,min=2"`
	Email string `json:"email" validate:"required,email"`
}

func main() {
	// Use the Segmentio codec for ~2-3x faster JSON encoding/decoding
	// compared to encoding/json, with the same API compatibility.
	app := aarv.New(
		aarv.WithCodec(segmentio.New()),
		aarv.WithBanner(true),
	)

	app.Use(aarv.Recovery(), aarv.Logger())

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"codec":       "segmentio/encoding/json",
			"description": "High-performance drop-in replacement for encoding/json",
			"performance": "~2-3x faster than stdlib",
			"api":         "100% compatible with encoding/json",
		})
	})

	app.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (User, error) {
		return User{
			ID:        1,
			Name:      req.Name,
			Email:     req.Email,
			CreatedAt: time.Now(),
		}, nil
	}))

	// Generate a moderately complex response to show encoding performance
	app.Get("/benchmark", func(c *aarv.Context) error {
		users := make([]User, 100)
		for i := range users {
			users[i] = User{
				ID:        i + 1,
				Name:      fmt.Sprintf("User %d", i+1),
				Email:     fmt.Sprintf("user%d@example.com", i+1),
				CreatedAt: time.Now(),
			}
		}
		return c.JSON(http.StatusOK, map[string]any{
			"users": users,
			"count": len(users),
		})
	})

	fmt.Println("Segmentio Codec Example on :8080")
	fmt.Println("  GET  /          — codec info")
	fmt.Println("  POST /users     — create user (typed handler)")
	fmt.Println("  GET  /benchmark — 100 users (performance test)")
	fmt.Println()
	fmt.Println("  Segmentio is a drop-in replacement for encoding/json")
	fmt.Println("  that provides ~2-3x faster encoding/decoding.")

	app.Listen(":8080")
}
