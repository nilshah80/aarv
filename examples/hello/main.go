package main

import (
	"fmt"
	"time"

	"github.com/nilshah80/aarv"
)

type CreateUserReq struct {
	Name  string `json:"name"  validate:"required,min=2"`
	Email string `json:"email" validate:"required,email"`
	Age   int    `json:"age"   validate:"gte=0,lte=150"`
}

type CreateUserRes struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type GetUserReq struct {
	ID     string `param:"id"`
	Fields string `query:"fields" default:"*"`
}

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
	)

	app.Use(aarv.Recovery(), aarv.Logger())

	// Simple handler
	app.Get("/hello", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{
			"message": "Hello from Aarv!",
			"time":    time.Now().Format(time.RFC3339),
		})
	})

	// Type-safe handler with binding + validation
	app.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (CreateUserRes, error) {
		return CreateUserRes{
			ID:    "usr_123",
			Name:  req.Name,
			Email: req.Email,
		}, nil
	}))

	// Handler with path param + query binding
	app.Get("/users/{id}", aarv.BindReq(func(c *aarv.Context, req GetUserReq) error {
		return c.JSON(200, map[string]string{
			"id":     req.ID,
			"fields": req.Fields,
		})
	}))

	// Route group
	app.Group("/api/v1", func(g *aarv.RouteGroup) {
		g.Get("/health", func(c *aarv.Context) error {
			return c.JSON(200, map[string]string{"status": "ok"})
		})
	})

	fmt.Println("Starting server on :8080...")
	app.Listen(":8080")
}
