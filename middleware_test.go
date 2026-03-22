package aarv

import (
	"context"
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

func TestGlobalGroupRouteMiddlewareOrdering(t *testing.T) {
	app := New(WithBanner(false))
	order := []string{}

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "global-in")
			next.ServeHTTP(w, r)
			order = append(order, "global-out")
		})
	})

	app.Group("/g", func(g *RouteGroup) {
		g.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "group-in")
				next.ServeHTTP(w, r)
				order = append(order, "group-out")
			})
		})

		g.Get("/test", func(c *Context) error {
			order = append(order, "handler")
			return c.Text(http.StatusOK, "ok")
		}, WithRouteMiddleware(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "route-in")
				next.ServeHTTP(w, r)
				order = append(order, "route-out")
			})
		}))
	})

	resp := NewTestClient(app).Get("/g/test")
	resp.AssertStatus(t, http.StatusOK)

	expected := []string{
		"global-in",
		"group-in",
		"route-in",
		"handler",
		"route-out",
		"group-out",
		"global-out",
	}
	if len(order) != len(expected) {
		t.Fatalf("middleware order mismatch: got %v want %v", order, expected)
	}
	for i, want := range expected {
		if order[i] != want {
			t.Fatalf("middleware order[%d] = %q, want %q (full=%v)", i, order[i], want, order)
		}
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

func TestWrapMiddlewareErrorPath(t *testing.T) {
	app := New(WithBanner(false))
	app.errorHandler = func(c *Context, err error) {
		http.Error(c.Response(), "wrapped:"+err.Error(), http.StatusTeapot)
	}
	req := httptest.NewRequest(http.MethodGet, "/wrap-err", nil)
	rec := httptest.NewRecorder()
	ctx := app.AcquireContext(rec, req)
	defer app.ReleaseContext(ctx)
	ctx.SetContext(req.Context())

	mw := WrapMiddleware(func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			return errors.New("middleware fail")
		}
	})
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	})).ServeHTTP(rec, ctx.Request())

	if rec.Code != http.StatusTeapot || rec.Body.String() != "wrapped:middleware fail\n" {
		t.Fatalf("unexpected wrapped error response code=%d body=%q", rec.Code, rec.Body.String())
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

func TestMiddlewareNilAndPanicRecoveryPaths(t *testing.T) {
	t.Run("nil wrapped middleware becomes passthrough", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Use(WrapMiddleware(nil))
		app.Get("/ok", func(c *Context) error { return c.NoContent(http.StatusNoContent) })

		resp := NewTestClient(app).Get("/ok")
		resp.AssertStatus(t, http.StatusNoContent)
	})

	t.Run("recovery catches panic in downstream middleware", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Use(Recovery())
		app.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("middleware boom")
			})
		})
		app.Get("/panic", func(c *Context) error { return c.NoContent(http.StatusOK) })

		resp := NewTestClient(app).Get("/panic")
		resp.AssertStatus(t, http.StatusInternalServerError)
	})

	t.Run("recovery catches panic in route middleware", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Use(Recovery())
		app.Get("/panic", func(c *Context) error {
			return c.NoContent(http.StatusOK)
		}, WithRouteMiddleware(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("route middleware boom")
			})
		}))

		resp := NewTestClient(app).Get("/panic")
		resp.AssertStatus(t, http.StatusInternalServerError)
	})
}

func TestMiddlewareAdditionalCoverage(t *testing.T) {
	t.Run("register native middleware stores mapping", func(t *testing.T) {
		m := RegisterNativeMiddleware(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			})
		}, func(next HandlerFunc) HandlerFunc {
			return func(c *Context) error { return next(c) }
		})

		if _, ok := nativeMiddlewareFunc(m); !ok {
			t.Fatal("expected native middleware mapping to be registered")
		}
	})

	t.Run("wrapped middleware falls back to next when aarv context is absent", func(t *testing.T) {
		called := false
		wrapped := WrapMiddleware(func(next HandlerFunc) HandlerFunc {
			return func(c *Context) error {
				t.Fatal("middleware should not receive aarv context on plain request")
				return nil
			}
		})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusAccepted)
		}))

		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plain", nil))
		if !called || rec.Code != http.StatusAccepted {
			t.Fatalf("expected wrapped middleware to pass through, called=%v code=%d", called, rec.Code)
		}
	})

	t.Run("logger middleware works without aarv context", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Logger()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plain", nil))

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected logger passthrough status, got %d", rec.Code)
		}
	})

	t.Run("wrapped middleware and logger use aarv context when present", func(t *testing.T) {
		app := New(WithBanner(false))
		req := httptest.NewRequest(http.MethodGet, "/ctx", nil)
		rec := httptest.NewRecorder()
		ctx := app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)

		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, ctx))
		ctx.req = req

		wrapped := WrapMiddleware(func(next HandlerFunc) HandlerFunc {
			return func(c *Context) error {
				c.Set("wrapped", "yes")
				return next(c)
			}
		})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got, _ := ctx.Get("wrapped"); got != "yes" {
				t.Fatalf("expected wrapped marker, got %#v", got)
			}
			w.WriteHeader(http.StatusCreated)
		}))
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected wrapped status 201, got %d", rec.Code)
		}

		rec = httptest.NewRecorder()
		Logger()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected logger status 202, got %d", rec.Code)
		}
	})
}
