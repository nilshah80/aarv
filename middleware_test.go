package aarv

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareChainExecutionOrder(t *testing.T) {
	app := New(WithBanner(false))

	order := []string{}

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "in1")
			next.ServeHTTP(w, r)
			order = append(order, "out1")
		})
	})

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "in2")
			next.ServeHTTP(w, r)
			order = append(order, "out2")
		})
	})

	app.Get("/test", func(c *Context) error {
		order = append(order, "handler")
		return c.Text(200, "ok")
	})

	tc := NewTestClient(app)
	resp := tc.Get("/test")
	resp.AssertStatus(t, 200)

	expected := []string{"in1", "in2", "handler", "out2", "out1"}
	if len(order) != len(expected) {
		t.Fatalf("length mismatch: got %v, expected %v", order, expected)
	}

	for i, v := range expected {
		if order[i] != v {
			t.Errorf("mismatch at idx %d: got %s, expected %s", i, order[i], v)
		}
	}
}

func TestGroupMiddlewareIsolation(t *testing.T) {
	app := New(WithBanner(false))

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("X-Trace", "global")
			next.ServeHTTP(w, r)
		})
	})

	app.Group("/g1", func(g *RouteGroup) {
		g.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("X-Trace", "g1")
				next.ServeHTTP(w, r)
			})
		})

		g.Get("/test", func(c *Context) error {
			return c.Text(200, "ok")
		})
	})

	app.Group("/g2", func(g *RouteGroup) {
		g.Get("/test", func(c *Context) error {
			return c.Text(200, "ok")
		})
	})

	tc := NewTestClient(app)

	// Test g1 gets global and g1
	resp1 := tc.Get("/g1/test")
	resp1.AssertStatus(t, 200)
	traces1 := resp1.Headers.Values("X-Trace")
	if len(traces1) != 2 || traces1[0] != "global" || traces1[1] != "g1" {
		t.Fatalf("g1 trace mismatch: %v", traces1)
	}

	// Test g2 gets only global
	resp2 := tc.Get("/g2/test")
	resp2.AssertStatus(t, 200)
	traces2 := resp2.Headers.Values("X-Trace")
	if len(traces2) != 1 || traces2[0] != "global" {
		t.Fatalf("g2 trace mismatch: %v", traces2)
	}
}

func TestErrorHandling(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/err", func(c *Context) error {
		return ErrNotFound("user not found")
	})

	app.Get("/panic", func(c *Context) error {
		panic("test panic")
	})

	app.Get("/customerror", func(c *Context) error {
		return errors.New("some custom error")
	})

	tc := NewTestClient(app)
	
	resp := tc.Get("/err")
	resp.AssertStatus(t, 404)

	var body errorResponse
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "not_found" {
		t.Errorf("expected error code 'not_found', got %q", body.Error)
	}

	// The panic isn't recovered by default without recovery middleware plugin. 
	// The HTTP standard library recovers panics and drops the connection, returning empty response via TestClient implicitly
	// Or HTTP 500 when using testclient wrapper. 
	
	req := httptest.NewRequest("GET", "/panic", nil)
	w := httptest.NewRecorder()
	
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic to bubble up")
		}
	}()
	app.ServeHTTP(w, req)
}

func TestWrapMiddleware(t *testing.T) {
	mwFunc := func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			c.SetHeader("X-Wrapped", "true")
			return next(c)
		}
	}
	mw := WrapMiddleware(mwFunc)
	
	app := New()
	app.Use(mw)
	app.Get("/wrap", func(c *Context) error { return c.NoContent(200) })

	tc := NewTestClient(app)
	res := tc.Get("/wrap")
	if res.Headers.Get("X-Wrapped") != "true" {
		t.Errorf("Expected X-Wrapped header")
	}
}

func TestLoggerMiddleware(t *testing.T) {
	app := New()
	app.Use(Logger())
	app.Get("/log", func(c *Context) error { return c.NoContent(200) })

	tc := NewTestClient(app)
	res := tc.Get("/log")
	if res.Status != 200 {
		t.Errorf("Expected 200")
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	app := New()
	app.Use(Recovery())
	app.Get("/panic", func(c *Context) error { panic("test panic") })

	tc := NewTestClient(app)
	res := tc.Get("/panic")
	if res.Status != 500 {
		t.Errorf("Expected 500 on panic, got %d", res.Status)
	}
}

func TestMiddlewareBranchCoverage(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	wrapped := WrapMiddleware(func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			c.Set("wrapped", true)
			return next(c)
		}
	})(next)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unexpected wrapped middleware status: %d", rec.Code)
	}

	app := New(WithBanner(false))
	app.Use(WrapMiddleware(func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			return errors.New("middleware failure")
		}
	}))
	app.Get("/mw-error", func(c *Context) error { return c.NoContent(http.StatusOK) })
	NewTestClient(app).Get("/mw-error").AssertStatus(t, http.StatusInternalServerError)

	recovered := Recovery()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec = httptest.NewRecorder()
	recovered.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected recovery status without context: %d", rec.Code)
	}
}
