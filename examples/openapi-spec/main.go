// Example: register typed handlers via aarv.BindRoute, then mount the
// OpenAPI plugin (auto-generates the spec from RouteInfo + validate
// tags) and the openapi-ui plugin (Swagger UI + ReDoc viewers).
//
// Run:
//
//	go run . -addr 127.0.0.1:8080
//
// Then in a browser:
//
//	http://127.0.0.1:8080/docs    Swagger UI
//	http://127.0.0.1:8080/redoc   ReDoc
//
// And from a terminal:
//
//	curl -s http://127.0.0.1:8080/openapi.json | jq .
//	curl -s http://127.0.0.1:8080/openapi.yaml
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/openapi"
	openapiui "github.com/nilshah80/aarv/plugins/openapi-ui"
)

type CreateUserReq struct {
	Name  string `json:"name"  validate:"required,min=2,max=64"`
	Email string `json:"email" validate:"required,email"`
	Role  string `json:"role"  validate:"oneof=admin editor viewer"`
}

type CreateUserRes struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type GetUserReq struct {
	ID string `param:"id"`
}

type ErrorBody struct {
	Error string `json:"error"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "bind address")
	flag.Parse()

	app := aarv.New(aarv.WithBanner(true))
	app.Use(aarv.Recovery(), aarv.Logger())

	aarv.BindRoute(app, "POST", "/users",
		func(c *aarv.Context, req CreateUserReq) (CreateUserRes, error) {
			return CreateUserRes{
				ID:    "usr_" + strings.ToLower(req.Name),
				Name:  req.Name,
				Email: req.Email,
				Role:  req.Role,
			}, nil
		},
		aarv.WithSummary("Create a user"),
		aarv.WithDescription("Creates a new user account. Email must be unique."),
		aarv.WithOperationID("createUser"),
		aarv.WithTags("users"),
		aarv.WithResponse(201, "Created"),
		aarv.WithResponse(409, "Email already in use"),
		aarv.WithResponse(422, "Validation failed"),
	)

	aarv.BindRoute(app, "GET", "/users/{id}",
		func(c *aarv.Context, req GetUserReq) (CreateUserRes, error) {
			return CreateUserRes{ID: req.ID, Name: "Example", Email: "x@y", Role: "viewer"}, nil
		},
		aarv.WithSummary("Fetch a user by ID"),
		aarv.WithOperationID("getUser"),
		aarv.WithTags("users"),
		aarv.WithResponse(404, "User not found"),
	)

	// Mount the OpenAPI spec generator. SecuritySchemes here is purely
	// declarative — operations don't yet auto-attach security
	// requirements (12.6b non-goal). Set them on the spec via custom
	// post-processing if you need that.
	if _, err := openapi.New(app, openapi.Config{
		Title:       "Users API",
		Version:     "1.0.0",
		Description: "Demo of aarv's OpenAPI plugin.",
		Servers: []openapi.Server{
			{URL: "http://" + *addr, Description: "local"},
		},
		SecuritySchemes: map[string]openapi.SecurityScheme{
			"bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
		},
	}); err != nil {
		log.Fatalf("openapi.New: %v", err)
	}

	// Mount Swagger UI + ReDoc viewers, both pointing at the JSON spec.
	if err := openapiui.Mount(app, openapiui.Config{
		Title:   "Users API",
		SpecURL: "/openapi.json",
	}); err != nil {
		log.Fatalf("openapiui.Mount: %v", err)
	}

	fmt.Printf("HTTP on %s — try /openapi.json, /openapi.yaml, /docs, /redoc\n", *addr)
	if err := app.Listen(*addr); err != nil {
		log.Fatal(err)
	}
}
