package main

import (
	"fmt"
	"net/http"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/openapi"
	openapiui "github.com/nilshah80/aarv/plugins/openapi-ui"
)

// CreateUserReq is the JSON body bound by POST /users; OpenAPI picks up
// validate tags as JSON Schema constraints.
type CreateUserReq struct {
	Name  string `json:"name" validate:"required,min=2,max=64"`
	Email string `json:"email" validate:"required,email"`
	Role  string `json:"role" validate:"oneof=admin editor viewer"`
}

// UserRes is the response shape returned by both create and get.
type UserRes struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func main() {
	app := aarv.New(aarv.WithBanner(true))
	app.Use(aarv.Recovery())

	aarv.BindRoute(app, "POST", "/users",
		func(c *aarv.Context, req CreateUserReq) (UserRes, error) {
			return UserRes{ID: "usr_1", Name: req.Name, Email: req.Email, Role: req.Role}, nil
		},
		aarv.WithSummary("Create user"),
		aarv.WithOperationID("createUser"),
		aarv.WithTags("users"),
		aarv.WithResponse(http.StatusCreated, "Created"),
		aarv.WithResponse(http.StatusUnprocessableEntity, "Validation failed"),
	)

	aarv.BindRoute(app, "GET", "/users/{id}",
		func(c *aarv.Context, req struct {
			ID string `param:"id" validate:"required"`
		}) (UserRes, error) {
			return UserRes{ID: req.ID, Name: "Alice", Email: "alice@example.com", Role: "admin"}, nil
		},
		aarv.WithSummary("Get user"),
		aarv.WithOperationID("getUser"),
		aarv.WithTags("users"),
		aarv.WithResponse(http.StatusNotFound, "User not found"),
	)

	if _, err := openapi.New(app, openapi.Config{
		Title:       "Aarv OpenAPI Example",
		Version:     "1.0.0",
		Description: "Generated from Aarv route metadata.",
	}); err != nil {
		panic(err)
	}

	if err := openapiui.Mount(app, openapiui.Config{
		Title: "Aarv OpenAPI Example",
	}); err != nil {
		panic(err)
	}

	fmt.Println("OpenAPI example on :8080")
	fmt.Println("  GET /openapi.json")
	fmt.Println("  GET /openapi.yaml")
	fmt.Println("  GET /docs")
	fmt.Println("  GET /redoc")

	_ = app.Listen(":8080")
}
