package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/nilshah80/aarv"
)

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
		aarv.WithErrorHandler(func(c *aarv.Context, err error) {
			status := http.StatusInternalServerError
			payload := map[string]any{
				"error":      "internal_error",
				"message":    err.Error(),
				"request_id": c.RequestID(),
			}

			var appErr *aarv.AppError
			if errors.As(err, &appErr) {
				status = appErr.StatusCode()
				payload["error"] = appErr.Code()
				payload["message"] = appErr.Message()
				if detail := appErr.Detail(); detail != "" {
					payload["detail"] = detail
				}
			}

			_ = c.JSON(status, payload)
		}),
	)

	app.Use(aarv.Recovery())
	app.AddHook(aarv.OnError, func(c *aarv.Context) error {
		fmt.Printf("[hook] OnError path=%s err=%v\n", c.Path(), c.HookError())
		return nil
	})

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"message": "error handling example"})
	})

	app.Get("/not-found", func(c *aarv.Context) error {
		return aarv.ErrNotFound("user not found").WithDetail("lookup by id failed")
	})

	app.Get("/conflict", func(c *aarv.Context) error {
		return aarv.ErrConflict("email already exists")
	})

	app.Get("/boom", func(c *aarv.Context) error {
		return fmt.Errorf("database unavailable")
	})

	fmt.Println("Error Handling demo on :8080")
	fmt.Println("  GET /")
	fmt.Println("  GET /not-found")
	fmt.Println("  GET /conflict")
	fmt.Println("  GET /boom")

	app.Listen(":8080")
}
