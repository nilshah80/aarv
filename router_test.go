package aarv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestRoutingMethods(t *testing.T) {
	app := New(WithBanner(false))

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}

	for _, method := range methods {
		m := method // capture
		handler := func(c *Context) error {
			return c.Text(200, m)
		}

		switch m {
		case "GET":
			app.Get("/test", handler)
		case "POST":
			app.Post("/test", handler)
		case "PUT":
			app.Put("/test", handler)
		case "DELETE":
			app.Delete("/test", handler)
		case "PATCH":
			app.Patch("/test", handler)
		case "HEAD":
			app.Head("/test", handler)
		case "OPTIONS":
			app.Options("/test", handler)
		}
	}

	app.Any("/any", func(c *Context) error {
		return c.Text(200, "ANY")
	})

	for _, method := range methods {
		req := httptest.NewRequest(method, "/test", nil)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("Method %s failed", method)
		}
		if method != "HEAD" {
			if w.Body.String() != method {
				t.Errorf("Method %s body mismatch", method)
			}
		}
	}

	for _, method := range methods {
		req := httptest.NewRequest(method, "/any", nil)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("Any failed for %s", method)
		}
	}
}

func TestMount(t *testing.T) {
	app := New(WithBanner(false))
	subHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write([]byte("sub")); err != nil {
			t.Fatalf("unexpected subhandler write error: %v", err)
		}
	})

	app.Mount("/sub/", subHandler)

	tc := NewTestClient(app)
	resp := tc.Get("/sub/x")
	resp.AssertStatus(t, 200)
	if resp.Text() != "sub" {
		t.Errorf("Mount mismatch")
	}
}

func TestRoutesList(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/users", func(c *Context) error { return c.Text(200, "ok") }, WithName("GetUsers"), WithDescription("Desc"), WithTags("A", "B"), WithDeprecated(), WithOperationID("op1"), WithRouteMaxBodySize(100), WithSummary("sum1"))

	routes := app.Routes()
	if len(routes) != 1 {
		t.Fatalf("Expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Method != "GET" || r.Pattern != "/users" || r.Name != "GetUsers" || r.Description != "Desc" || len(r.Tags) != 2 || !r.Deprecated {
		t.Errorf("Route metadata mismatch: %+v", r)
	}

	c, ok := FromRequest(httptest.NewRequest("GET", "/users", nil))
	if ok || c != nil {
		t.Errorf("FromRequest should return false empty context")
	}
}

func TestCustom404And405(t *testing.T) {
	app := New(WithBanner(false))

	app.SetNotFoundHandler(func(c *Context) error {
		return c.Text(404, "Custom404")
	})

	app.SetMethodNotAllowedHandler(func(c *Context) error {
		return c.Text(405, "Custom405")
	})

	app.Get("/valid", func(c *Context) error { return c.Text(200, "ok") })

	req404 := httptest.NewRequest("GET", "/invalid", nil)
	ctx404 := app.AcquireContext(httptest.NewRecorder(), req404)
	if err := app.notFoundHandler(ctx404); err != nil {
		t.Fatalf("not found handler returned error: %v", err)
	}
	app.ReleaseContext(ctx404)

	// 405
	req := httptest.NewRequest("POST", "/valid", strings.NewReader(""))
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("Expected 405, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Custom405") {
		t.Errorf("405 mismatch: %s", w.Body.String())
	}
}

func TestRouteGroupAdditionalCoverage(t *testing.T) {
	app := New(WithBanner(false))
	app.Group("/api", func(g *RouteGroup) {
		g.Patch("/patch", func(c *Context) error { return c.NoContent(http.StatusOK) })
		g.Head("/head", func(c *Context) error { return c.NoContent(http.StatusOK) })
		g.Options("/options", func(c *Context) error { return c.NoContent(http.StatusOK) })
		g.Any("/any", func(c *Context) error { return c.NoContent(http.StatusOK) })
		g.Group("/nested", func(sub *RouteGroup) {
			sub.Get("/child", func(c *Context) error { return c.NoContent(http.StatusOK) })
		})
	})

	tc := NewTestClient(app)
	tc.Patch("/api/patch", nil).AssertStatus(t, http.StatusOK)
	tc.Do(httptest.NewRequest(http.MethodHead, "/api/head", nil)).AssertStatus(t, http.StatusOK)
	tc.Do(httptest.NewRequest(http.MethodOptions, "/api/options", nil)).AssertStatus(t, http.StatusOK)
	tc.Get("/api/any").AssertStatus(t, http.StatusOK)
	tc.Get("/api/nested/child").AssertStatus(t, http.StatusOK)

	app = New(WithBanner(false))
	app.Get("/direct", func(c *Context) error { return c.NoContent(http.StatusOK) }, WithRouteMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Route", "true")
			next.ServeHTTP(w, r)
		})
	}))
	rec := httptest.NewRecorder()
	app.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/direct", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected direct mux call without aarv context to fail, got %d", rec.Code)
	}
}
