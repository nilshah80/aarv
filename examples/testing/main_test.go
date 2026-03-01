// Example: Testing patterns — demonstrates the TestClient for handler testing
// without starting a real server. Run with: go test -v ./examples/testing/
package testing_example

import (
	"net/http"
	"testing"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/requestid"
)

// --- App factory used by all tests ---

func setupApp() *aarv.App {
	app := aarv.New()

	app.Use(aarv.Recovery(), requestid.New())

	// Simple GET
	app.Get("/ping", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "pong")
	})

	// Typed handler with validation
	type CreateReq struct {
		Name  string `json:"name"  validate:"required,min=2"`
		Email string `json:"email" validate:"required,email"`
	}
	type CreateRes struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	app.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateReq) (CreateRes, error) {
		return CreateRes{ID: "usr_001", Name: req.Name, Email: req.Email}, nil
	}))

	// Path params
	type GetReq struct {
		ID string `param:"id"`
	}
	app.Get("/users/{id}", aarv.BindReq(func(c *aarv.Context, req GetReq) error {
		if req.ID == "not_found" {
			return aarv.ErrNotFound("user not found")
		}
		return c.JSON(http.StatusOK, map[string]string{
			"id":   req.ID,
			"name": "Test User",
		})
	}))

	// Query params with defaults
	type ListReq struct {
		Page     int    `query:"page"      default:"1"`
		PageSize int    `query:"page_size" default:"20"`
		Sort     string `query:"sort"      default:"name"`
	}
	type ListRes struct {
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}
	app.Get("/users", aarv.Bind(func(c *aarv.Context, req ListReq) (ListRes, error) {
		return ListRes{Page: req.Page, PageSize: req.PageSize}, nil
	}))

	// Auth-protected endpoint
	app.Get("/me", func(c *aarv.Context) error {
		auth := c.Header("Authorization")
		if auth == "" {
			return aarv.ErrUnauthorized("missing token")
		}
		return c.JSON(http.StatusOK, map[string]string{"auth": auth})
	})

	// Context store
	app.Get("/store", func(c *aarv.Context) error {
		c.Set("key", "value")
		val := c.MustGet("key").(string)
		return c.JSON(http.StatusOK, map[string]string{"key": val})
	})

	// Error responses
	app.Get("/forbidden", func(c *aarv.Context) error {
		return aarv.ErrForbidden("access denied")
	})

	app.Get("/conflict", func(c *aarv.Context) error {
		return aarv.ErrConflict("resource conflict").WithDetail("duplicate entry")
	})

	// No content
	app.Delete("/users/{id}", func(c *aarv.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	// Route group
	app.Group("/api/v2", func(g *aarv.RouteGroup) {
		g.Get("/status", func(c *aarv.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"version": "v2"})
		})
	})

	return app
}

// =============================================================================
// Tests
// =============================================================================

func TestPing(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())
	res := tc.Get("/ping")
	res.AssertStatus(t, http.StatusOK)

	if res.Text() != "pong" {
		t.Errorf("expected pong, got %q", res.Text())
	}
}

func TestCreateUser(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())
	res := tc.Post("/users", map[string]any{
		"name":  "Alice",
		"email": "alice@example.com",
	})
	res.AssertStatus(t, http.StatusOK)

	var body map[string]string
	if err := res.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "Alice" {
		t.Errorf("expected Alice, got %s", body["name"])
	}
	if body["email"] != "alice@example.com" {
		t.Errorf("expected alice@example.com, got %s", body["email"])
	}
}

func TestValidationError(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	// Missing required fields
	res := tc.Post("/users", map[string]any{
		"name": "A", // too short (min=2)
	})
	res.AssertStatus(t, http.StatusUnprocessableEntity)

	var body map[string]any
	if err := res.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "validation_failed" {
		t.Errorf("expected validation_failed, got %v", body["error"])
	}
}

func TestPathParam(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	res := tc.Get("/users/usr_123")
	res.AssertStatus(t, http.StatusOK)

	var body map[string]string
	if err := res.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "usr_123" {
		t.Errorf("expected usr_123, got %s", body["id"])
	}
}

func TestNotFound(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	res := tc.Get("/users/not_found")
	res.AssertStatus(t, http.StatusNotFound)
}

func TestQueryParamsWithDefaults(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	// Use defaults
	res := tc.Get("/users")
	res.AssertStatus(t, http.StatusOK)

	var body map[string]any
	if err := res.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if int(body["page"].(float64)) != 1 {
		t.Errorf("expected page 1, got %v", body["page"])
	}
	if int(body["page_size"].(float64)) != 20 {
		t.Errorf("expected page_size 20, got %v", body["page_size"])
	}

	// Override with query params
	res2 := tc.WithQuery("page", "3").WithQuery("page_size", "10").Get("/users")
	res2.AssertStatus(t, http.StatusOK)

	var body2 map[string]any
	if err := res2.JSON(&body2); err != nil {
		t.Fatal(err)
	}
	if int(body2["page"].(float64)) != 3 {
		t.Errorf("expected page 3, got %v", body2["page"])
	}
}

func TestBearerAuth(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	// No auth — should fail
	res := tc.Get("/me")
	res.AssertStatus(t, http.StatusUnauthorized)

	// With bearer token
	res2 := tc.WithBearer("my-secret-token").Get("/me")
	res2.AssertStatus(t, http.StatusOK)

	var body map[string]string
	if err := res2.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["auth"] != "Bearer my-secret-token" {
		t.Errorf("expected Bearer my-secret-token, got %s", body["auth"])
	}
}

func TestCustomHeaders(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	res := tc.WithHeader("X-Custom", "test-value").Get("/ping")
	res.AssertStatus(t, http.StatusOK)

	// Verify request ID is set by middleware
	if res.Headers.Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header to be set")
	}
}

func TestErrorResponses(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	t.Run("forbidden", func(t *testing.T) {
		res := tc.Get("/forbidden")
		res.AssertStatus(t, http.StatusForbidden)

		var body map[string]any
		res.JSON(&body)
		if body["error"] != "forbidden" {
			t.Errorf("expected error code forbidden, got %v", body["error"])
		}
	})

	t.Run("conflict with detail", func(t *testing.T) {
		res := tc.Get("/conflict")
		res.AssertStatus(t, http.StatusConflict)

		var body map[string]any
		res.JSON(&body)
		if body["error"] != "conflict" {
			t.Errorf("expected error code conflict, got %v", body["error"])
		}
		if body["detail"] != "duplicate entry" {
			t.Errorf("expected detail 'duplicate entry', got %v", body["detail"])
		}
	})
}

func TestDeleteNoContent(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	res := tc.Delete("/users/usr_001")
	res.AssertStatus(t, http.StatusNoContent)

	if len(res.Body) > 0 {
		t.Errorf("expected empty body for 204, got %q", string(res.Body))
	}
}

func TestRouteGroup(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	res := tc.Get("/api/v2/status")
	res.AssertStatus(t, http.StatusOK)

	var body map[string]string
	if err := res.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["version"] != "v2" {
		t.Errorf("expected v2, got %s", body["version"])
	}
}

func TestContextStore(t *testing.T) {
	tc := aarv.NewTestClient(setupApp())

	res := tc.Get("/store")
	res.AssertStatus(t, http.StatusOK)

	var body map[string]string
	if err := res.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["key"] != "value" {
		t.Errorf("expected value, got %s", body["key"])
	}
}
