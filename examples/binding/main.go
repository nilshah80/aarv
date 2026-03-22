package main

import (
	"fmt"
	"net/http"

	"github.com/nilshah80/aarv"
)

type SearchReq struct {
	Query string `query:"q" validate:"required,min=2"`
	Page  int    `query:"page" default:"1" validate:"gte=1,lte=100"`
	Limit int    `query:"limit" default:"20" validate:"gte=1,lte=100"`
	Token string `header:"X-API-Key" validate:"required"`
}

type CreateUserReq struct {
	OrgID   string `param:"orgId" validate:"required"`
	DryRun  bool   `query:"dryRun" default:"false"`
	TraceID string `header:"X-Trace-ID"`
	Name    string `json:"name" validate:"required,min=2"`
	Email   string `json:"email" validate:"required,email"`
	Role    string `json:"role" validate:"oneof=member admin"`
}

func main() {
	app := aarv.New(aarv.WithBanner(true))
	app.Use(aarv.Recovery())

	app.Get("/search", aarv.BindReq(func(c *aarv.Context, req SearchReq) error {
		return c.JSON(http.StatusOK, map[string]any{
			"query": req.Query,
			"page":  req.Page,
			"limit": req.Limit,
			"token": req.Token,
		})
	}))

	app.Post("/orgs/{orgId}/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (map[string]any, error) {
		return map[string]any{
			"created":  !req.DryRun,
			"org_id":   req.OrgID,
			"name":     req.Name,
			"email":    req.Email,
			"role":     req.Role,
			"trace_id": req.TraceID,
		}, nil
	}))

	fmt.Println("Binding demo on :8080")
	fmt.Println("  GET /search?q=aarv&page=2&limit=10   with X-API-Key")
	fmt.Println("  POST /orgs/acme/users?dryRun=true    with JSON body + X-Trace-ID")

	app.Listen(":8080")
}
