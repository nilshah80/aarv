package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/codec/segmentio"
)

type user struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type createUserReq struct {
	Name  string `json:"name" validate:"required,min=2"`
	Email string `json:"email" validate:"required,email"`
}

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
		aarv.WithRequestContextBridge(false),
		aarv.WithCodec(segmentio.New()),
	)

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"profile": "performance",
			"codec":   "segmentio/encoding/json",
			"bridge":  "disabled",
			"notes": []string{
				"Use this profile only when stdlib middleware does not depend on aarv.FromRequest after r.WithContext clones.",
				"Prefer Aarv-native middleware when possible for the leanest hot path.",
			},
		})
	})

	app.Get("/users", func(c *aarv.Context) error {
		users := make([]user, 64)
		now := time.Now().UTC()
		for i := range users {
			users[i] = user{
				ID:        fmt.Sprintf("usr_%03d", i+1),
				Name:      fmt.Sprintf("User %d", i+1),
				Email:     fmt.Sprintf("user%d@example.com", i+1),
				CreatedAt: now,
			}
		}
		return c.JSON(http.StatusOK, map[string]any{
			"users": users,
			"count": len(users),
		})
	})

	app.Post("/users", aarv.Bind(func(c *aarv.Context, req createUserReq) (user, error) {
		return user{
			ID:        "usr_new",
			Name:      req.Name,
			Email:     req.Email,
			CreatedAt: time.Now().UTC(),
		}, nil
	}))

	fmt.Println("Performance Profile Example on :8080")
	fmt.Println("  GET  /       - profile info")
	fmt.Println("  GET  /users  - larger JSON response")
	fmt.Println("  POST /users  - typed bind path")
	fmt.Println()
	fmt.Println("This profile favors throughput over maximum stdlib middleware compatibility.")
	fmt.Println("Disable the request-context bridge only when your middleware stack does not need aarv.FromRequest after r.WithContext clones.")

	app.Listen(":8080")
}
