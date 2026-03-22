package main

import (
	"fmt"
	"net/http"

	"github.com/nilshah80/aarv"
)

func apiVersion(version string) aarv.Middleware {
	return aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			c.SetHeader("X-API-Version", version)
			return next(c)
		}
	})
}

func requireAdmin(next aarv.HandlerFunc) aarv.HandlerFunc {
	return func(c *aarv.Context) error {
		if c.Header("X-Admin") != "true" {
			return aarv.ErrForbidden("admin access required")
		}
		return next(c)
	}
}

func main() {
	app := aarv.New(aarv.WithBanner(true))
	app.Use(aarv.Recovery())

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"message": "route groups example"})
	})

	app.Group("/api", func(api *aarv.RouteGroup) {
		api.Use(apiVersion("v1"))

		api.Get("/health", func(c *aarv.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
		})

		api.Group("/users", func(users *aarv.RouteGroup) {
			users.Get("", func(c *aarv.Context) error {
				return c.JSON(http.StatusOK, []map[string]any{
					{"id": "usr_1", "name": "Alice"},
					{"id": "usr_2", "name": "Bob"},
				})
			})

			users.Get("/{id}", func(c *aarv.Context) error {
				return c.JSON(http.StatusOK, map[string]any{
					"id":   c.Param("id"),
					"name": "Alice",
				})
			})
		})

		api.Group("/admin", func(admin *aarv.RouteGroup) {
			admin.Use(aarv.WrapMiddleware(requireAdmin))

			admin.Delete("/users/{id}", func(c *aarv.Context) error {
				return c.JSON(http.StatusOK, map[string]string{
					"deleted": c.Param("id"),
				})
			})
		})
	})

	fmt.Println("Route Groups demo on :8080")
	fmt.Println("  GET /")
	fmt.Println("  GET /api/health")
	fmt.Println("  GET /api/users")
	fmt.Println("  GET /api/users/usr_1")
	fmt.Println("  DELETE /api/admin/users/usr_1  with X-Admin: true")

	app.Listen(":8080")
}
