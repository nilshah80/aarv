package aarv

import (
	"context"
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
	var apiGroup *RouteGroup
	app.Group("/api", func(g *RouteGroup) {
		apiGroup = g
		g.Patch("/patch", func(c *Context) error { return c.NoContent(http.StatusOK) })
		g.Head("/head", func(c *Context) error { return c.NoContent(http.StatusOK) })
		g.Options("/options", func(c *Context) error { return c.NoContent(http.StatusOK) })
		g.Any("/any", func(c *Context) error { return c.NoContent(http.StatusOK) })
		g.Group("/nested", func(sub *RouteGroup) {
			sub.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Nested", "true")
					next.ServeHTTP(w, r)
				})
			})
			sub.Get("/child", func(c *Context) error { return c.NoContent(http.StatusOK) })
		})
	})

	tc := NewTestClient(app)
	tc.Patch("/api/patch", nil).AssertStatus(t, http.StatusOK)
	tc.Do(httptest.NewRequest(http.MethodHead, "/api/head", nil)).AssertStatus(t, http.StatusOK)
	tc.Do(httptest.NewRequest(http.MethodOptions, "/api/options", nil)).AssertStatus(t, http.StatusOK)
	tc.Get("/api/any").AssertStatus(t, http.StatusOK)
	resp := tc.Get("/api/nested/child")
	resp.AssertStatus(t, http.StatusOK)
	if resp.Headers.Get("X-Nested") != "true" {
		t.Fatalf("expected nested group middleware header, got %q", resp.Headers.Get("X-Nested"))
	}

	rec := httptest.NewRecorder()
	apiGroup.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/patch", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected direct group mux call without aarv context to fail, got %d", rec.Code)
	}

	errReq := httptest.NewRequest(http.MethodGet, "/explode", nil)
	errCtx, errRec := newAppContext(app, errReq)
	errReq = errReq.WithContext(context.WithValue(errReq.Context(), ctxKey{}, errCtx))
	errCtx.req = errReq
	apiGroup.Get("/explode", func(c *Context) error { return ErrBadRequest("group failed") })
	apiGroup.mux.ServeHTTP(errRec, errReq)
	if errRec.Code != http.StatusBadRequest {
		t.Fatalf("expected grouped handler error to be handled, got %d", errRec.Code)
	}

	app = New(WithBanner(false))
	app.Get("/direct", func(c *Context) error { return c.NoContent(http.StatusOK) }, WithRouteMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Route", "true")
			next.ServeHTTP(w, r)
		})
	}))
	rec = httptest.NewRecorder()
	app.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/direct", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected direct mux call without aarv context to fail, got %d", rec.Code)
	}
}

func TestRequestContextHelpersAdditionalCoverage(t *testing.T) {
	t.Run("contextFromRequest lookup order", func(t *testing.T) {
		if ctx, ok := contextFromRequest(nil); ok || ctx != nil {
			t.Fatalf("expected nil request lookup to fail, got %#v %v", ctx, ok)
		}

		app := New(WithBanner(false))
		req := httptest.NewRequest(http.MethodGet, "/ctx", nil)
		ctx, _ := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		storeRequestContext(req, ctx)
		t.Cleanup(func() {
			deleteRequestContext(req)
		})

		if got, ok := contextFromRequest(req); !ok || got != ctx {
			t.Fatalf("expected registry lookup to win, got %#v %v", got, ok)
		}

		otherReq := httptest.NewRequest(http.MethodGet, "/ctx", nil).WithContext(context.WithValue(context.Background(), ctxKey{}, ctx))
		if got, ok := contextFromRequest(otherReq); !ok || got != ctx {
			t.Fatalf("expected context fallback lookup, got %#v %v", got, ok)
		}
	})

	t.Run("withFrameworkContext preserves aarv context", func(t *testing.T) {
		if withFrameworkContext(nil, nil) != nil {
			t.Fatal("expected nil request to stay nil")
		}

		app := New(WithBanner(false))
		req := httptest.NewRequest(http.MethodGet, "/ctx", nil)
		ctx, _ := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		if got := withFrameworkContext(req, nil); got != req {
			t.Fatal("expected nil aarv context to leave request unchanged")
		}

		reqWithCtx := withFrameworkContext(req, ctx)
		if got, ok := contextFromRequest(reqWithCtx); !ok || got != ctx {
			t.Fatalf("expected framework context attachment, got %#v %v", got, ok)
		}

		if reqAgain := withFrameworkContext(reqWithCtx, ctx); reqAgain != reqWithCtx {
			t.Fatal("expected helper to avoid cloning when context already attached")
		}
	})

	t.Run("stripPrefixPreserveContext branches", func(t *testing.T) {
		handler := stripPrefixPreserveContext("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Path", r.URL.Path)
			if ctx, ok := contextFromRequest(r); ok && ctx != nil {
				w.Header().Set("X-Has-Context", "true")
			}
			w.WriteHeader(http.StatusNoContent)
		}))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected not found for non-matching prefix, got %d", rec.Code)
		}

		app := New(WithBanner(false))
		req := httptest.NewRequest(http.MethodGet, "/api", nil)
		ctx, _ := newAppContext(app, req)
		defer app.ReleaseContext(ctx)

		storeRequestContext(req, ctx)
		t.Cleanup(func() {
			deleteRequestContext(req)
		})

		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected stripped request to succeed, got %d", rec.Code)
		}
		if rec.Header().Get("X-Path") != "/" {
			t.Fatalf("expected stripped path '/', got %q", rec.Header().Get("X-Path"))
		}
		if rec.Header().Get("X-Has-Context") != "true" {
			t.Fatalf("expected context propagation to stripped request")
		}
	})
}

type testCtxKeyDB struct{}
type testCtxKeyUsername struct{}

func TestStandardMiddlewareWithContextCompatibility(t *testing.T) {
	t.Run("global middleware clone keeps aarv context", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), testCtxKeyDB{}, "connected")))
			})
		})
		app.Get("/users/{id}", func(c *Context) error {
			return c.JSON(http.StatusOK, map[string]any{
				"id": c.Param("id"),
				"db": c.Request().Context().Value(testCtxKeyDB{}),
			})
		})

		resp := NewTestClient(app).Get("/users/42")
		resp.AssertStatus(t, http.StatusOK)
		if !strings.Contains(resp.Text(), `"id":"42"`) || !strings.Contains(resp.Text(), `"db":"connected"`) {
			t.Fatalf("unexpected response body %q", resp.Text())
		}
	})

	t.Run("group middleware clone keeps aarv context", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Group("/api", func(g *RouteGroup) {
			g.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx := context.WithValue(r.Context(), testCtxKeyUsername{}, "admin")
					next.ServeHTTP(w, r.WithContext(ctx))
				})
			})
			g.Get("/protected", func(c *Context) error {
				return c.JSON(http.StatusOK, map[string]any{
					"user": c.Request().Context().Value(testCtxKeyUsername{}),
				})
			})
		})

		resp := NewTestClient(app).Get("/api/protected")
		resp.AssertStatus(t, http.StatusOK)
		if !strings.Contains(resp.Text(), `"user":"admin"`) {
			t.Fatalf("unexpected response body %q", resp.Text())
		}
	})
}
