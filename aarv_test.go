package aarv

import (
	"net/http"
	"testing"
)

func TestHelloWorld(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/hello", func(c *Context) error {
		return c.JSON(200, map[string]string{"message": "hello"})
	})

	tc := NewTestClient(app)
	resp := tc.Get("/hello")
	resp.AssertStatus(t, 200)

	var body map[string]string
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["message"] != "hello" {
		t.Errorf("expected 'hello', got %q", body["message"])
	}
}

func TestBind(t *testing.T) {
	type Req struct {
		Name  string `json:"name" validate:"required,min=2"`
		Email string `json:"email" validate:"required,email"`
	}
	type Res struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	app := New(WithBanner(false))
	app.Post("/users", Bind(func(c *Context, req Req) (Res, error) {
		return Res{ID: "1", Name: req.Name}, nil
	}))

	tc := NewTestClient(app)

	// Valid request
	resp := tc.Post("/users", map[string]string{"name": "Alice", "email": "alice@test.com"})
	resp.AssertStatus(t, 200)

	var res Res
	if err := resp.JSON(&res); err != nil {
		t.Fatal(err)
	}
	if res.Name != "Alice" {
		t.Errorf("expected 'Alice', got %q", res.Name)
	}
}

func TestBindValidation(t *testing.T) {
	type Req struct {
		Name  string `json:"name" validate:"required,min=2"`
		Email string `json:"email" validate:"required,email"`
	}
	type Res struct {
		ID string `json:"id"`
	}

	app := New(WithBanner(false))
	app.Post("/users", Bind(func(c *Context, req Req) (Res, error) {
		return Res{ID: "1"}, nil
	}))

	tc := NewTestClient(app)

	// Missing required fields
	resp := tc.Post("/users", map[string]string{})
	resp.AssertStatus(t, 422)
}

func TestPathParam(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/users/{id}", func(c *Context) error {
		return c.JSON(200, map[string]string{"id": c.Param("id")})
	})

	tc := NewTestClient(app)
	resp := tc.Get("/users/42")
	resp.AssertStatus(t, 200)

	var body map[string]string
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "42" {
		t.Errorf("expected '42', got %q", body["id"])
	}
}

func TestBindReqWithParams(t *testing.T) {
	type Req struct {
		ID     string `param:"id"`
		Fields string `query:"fields" default:"*"`
	}

	app := New(WithBanner(false))
	app.Get("/users/{id}", BindReq(func(c *Context, req Req) error {
		return c.JSON(200, map[string]string{
			"id":     req.ID,
			"fields": req.Fields,
		})
	}))

	tc := NewTestClient(app)
	resp := tc.WithQuery("fields", "name,email").Get("/users/abc")
	resp.AssertStatus(t, 200)

	var body map[string]string
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "abc" {
		t.Errorf("expected 'abc', got %q", body["id"])
	}
	if body["fields"] != "name,email" {
		t.Errorf("expected 'name,email', got %q", body["fields"])
	}
}

func TestBindReqDefaults(t *testing.T) {
	type Req struct {
		ID     string `param:"id"`
		Fields string `query:"fields" default:"*"`
	}

	app := New(WithBanner(false))
	app.Get("/users/{id}", BindReq(func(c *Context, req Req) error {
		return c.JSON(200, map[string]string{"fields": req.Fields})
	}))

	tc := NewTestClient(app)
	resp := tc.Get("/users/abc")
	resp.AssertStatus(t, 200)

	var body map[string]string
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["fields"] != "*" {
		t.Errorf("expected '*', got %q", body["fields"])
	}
}

func TestRouteGroup(t *testing.T) {
	app := New(WithBanner(false))
	app.Group("/api", func(g *RouteGroup) {
		g.Get("/health", func(c *Context) error {
			return c.JSON(200, map[string]string{"status": "ok"})
		})
	})

	tc := NewTestClient(app)
	resp := tc.Get("/api/health")
	resp.AssertStatus(t, 200)
}

func TestMiddleware(t *testing.T) {
	app := New(WithBanner(false))

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Custom", "aarv")
			next.ServeHTTP(w, r)
		})
	})

	app.Get("/test", func(c *Context) error {
		return c.Text(200, "ok")
	})

	tc := NewTestClient(app)
	resp := tc.Get("/test")
	resp.AssertStatus(t, 200)

	if resp.Headers.Get("X-Custom") != "aarv" {
		t.Errorf("expected X-Custom header 'aarv', got %q", resp.Headers.Get("X-Custom"))
	}
}

func TestErrorHandling(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/notfound", func(c *Context) error {
		return ErrNotFound("user not found")
	})

	tc := NewTestClient(app)
	resp := tc.Get("/notfound")
	resp.AssertStatus(t, 404)

	var body errorResponse
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "not_found" {
		t.Errorf("expected error code 'not_found', got %q", body.Error)
	}
}

func TestContextStore(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/store", func(c *Context) error {
		c.Set("key", "value")
		v, ok := c.Get("key")
		if !ok {
			return c.Error(500, "key not found")
		}
		return c.JSON(200, map[string]string{"key": v.(string)})
	})

	tc := NewTestClient(app)
	resp := tc.Get("/store")
	resp.AssertStatus(t, 200)
}

func TestHook(t *testing.T) {
	app := New(WithBanner(false))

	app.AddHook(OnRequest, func(c *Context) error {
		c.Set("hooked", true)
		return nil
	})

	app.Get("/hook", func(c *Context) error {
		v, _ := GetTyped[bool](c, "hooked")
		return c.JSON(200, map[string]bool{"hooked": v})
	})

	tc := NewTestClient(app)
	resp := tc.Get("/hook")
	resp.AssertStatus(t, 200)

	var body map[string]bool
	_ = resp.JSON(&body)
	if !body["hooked"] {
		t.Error("expected hooked to be true")
	}
}

func TestTextResponse(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/text", func(c *Context) error {
		return c.Text(200, "hello world")
	})

	tc := NewTestClient(app)
	resp := tc.Get("/text")
	resp.AssertStatus(t, 200)

	if resp.Text() != "hello world" {
		t.Errorf("expected 'hello world', got %q", resp.Text())
	}
}

func TestNoContent(t *testing.T) {
	app := New(WithBanner(false))
	app.Delete("/item", func(c *Context) error {
		return c.NoContent(204)
	})

	tc := NewTestClient(app)
	resp := tc.Delete("/item")
	resp.AssertStatus(t, 204)
}
