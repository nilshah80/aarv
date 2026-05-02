package aarv

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRoutePattern_Exact asserts that RoutePattern returns the registered
// path verbatim for an exact (non-dynamic) match through the direct fast path.
func TestRoutePattern_Exact(t *testing.T) {
	app := New()
	var observed string
	app.Get("/x", func(c *Context) error {
		observed = c.RoutePattern()
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	if observed != "/x" {
		t.Fatalf("RoutePattern: want %q, got %q", "/x", observed)
	}
}

// TestRoutePattern_Dynamic asserts that RoutePattern returns the source
// pattern (with brace placeholders) rather than the resolved request path.
func TestRoutePattern_Dynamic(t *testing.T) {
	app := New()
	var observed string
	app.Get("/users/{id}", func(c *Context) error {
		observed = c.RoutePattern()
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	if observed != "/users/{id}" {
		t.Fatalf("RoutePattern: want %q, got %q", "/users/{id}", observed)
	}
}

// TestRoutePattern_GroupedDynamic verifies that grouped dynamic routes return
// the full path including the group prefix.
func TestRoutePattern_GroupedDynamic(t *testing.T) {
	app := New()
	var observed string
	app.Group("/api", func(g *RouteGroup) {
		g.Get("/users/{id}", func(c *Context) error {
			observed = c.RoutePattern()
			return c.Text(http.StatusOK, "ok")
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	if observed != "/api/users/{id}" {
		t.Fatalf("RoutePattern: want %q, got %q", "/api/users/{id}", observed)
	}
}

// TestRoutePattern_CatchAll verifies that catch-all wildcard segments retain
// the {name...} suffix in the pattern.
func TestRoutePattern_CatchAll(t *testing.T) {
	app := New()
	var observed string
	app.Get("/files/{path...}", func(c *Context) error {
		observed = c.RoutePattern()
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/files/a/b/c.txt", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	if observed != "/files/{path...}" {
		t.Fatalf("RoutePattern: want %q, got %q", "/files/{path...}", observed)
	}
}

// TestRoutePattern_RouteLevelMiddlewareSeesPreNext asserts that route-level
// middleware (registered via WithRouteMiddleware) can read RoutePattern
// *before* calling next, since dispatch sets the pattern before invoking the
// route's wrapped handler chain.
//
// Global middleware (app.Use) runs BEFORE routing — see
// TestRoutePattern_GlobalMiddlewareSeesPostNext for the post-next case.
func TestRoutePattern_RouteLevelMiddlewareSeesPreNext(t *testing.T) {
	app := New()
	var preNext string
	rmw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c, ok := FromRequest(r); ok {
				preNext = c.RoutePattern()
			}
			next.ServeHTTP(w, r)
		})
	}
	app.Get("/users/{id}", func(c *Context) error {
		return c.Text(http.StatusOK, "ok")
	}, WithRouteMiddleware(rmw))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/7", nil)
	app.ServeHTTP(rec, req)

	if preNext != "/users/{id}" {
		t.Fatalf("middleware pre-next: want %q, got %q", "/users/{id}", preNext)
	}
}

// TestRoutePattern_GlobalMiddlewareSeesPostNext asserts that global stdlib
// middleware can read RoutePattern *after* next.ServeHTTP returns. This is the
// canonical pattern for Prometheus / OTel: record metrics with the pattern
// label after the handler runs.
func TestRoutePattern_GlobalMiddlewareSeesPostNext(t *testing.T) {
	app := New()
	var postNext string
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if c, ok := FromRequest(r); ok {
				postNext = c.RoutePattern()
			}
		})
	})
	app.Get("/users/{id}", func(c *Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/9", nil)
	app.ServeHTTP(rec, req)

	if postNext != "/users/{id}" {
		t.Fatalf("middleware post-next: want %q, got %q", "/users/{id}", postNext)
	}
}

// TestRoutePattern_NotFound asserts RoutePattern is empty for unmatched paths.
func TestRoutePattern_NotFound(t *testing.T) {
	app := New()
	app.Get("/x", func(c *Context) error { return c.Text(http.StatusOK, "ok") })

	var observed string
	observed = "sentinel"
	app.SetNotFoundHandler(func(c *Context) error {
		observed = c.RoutePattern()
		return c.Text(http.StatusNotFound, "nope")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
	if observed != "" {
		t.Fatalf("RoutePattern on 404: want empty, got %q", observed)
	}
}

// TestRoutePattern_MethodNotAllowed asserts RoutePattern is empty when the
// path matched but the method did not.
func TestRoutePattern_MethodNotAllowed(t *testing.T) {
	app := New()
	app.Get("/x", func(c *Context) error { return c.Text(http.StatusOK, "ok") })

	var observed string
	observed = "sentinel"
	app.SetMethodNotAllowedHandler(func(c *Context) error {
		observed = c.RoutePattern()
		return c.Text(http.StatusMethodNotAllowed, "nope")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: want 405, got %d", rec.Code)
	}
	if observed != "" {
		t.Fatalf("RoutePattern on 405: want empty, got %q", observed)
	}
}

// TestRoutePattern_MountedHandler asserts that handlers installed via
// App.Mount do not get a RoutePattern populated — they live outside the
// registered aarv route table.
func TestRoutePattern_MountedHandler(t *testing.T) {
	app := New()
	mounted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No aarv Context resolvable from a plain http.HandlerFunc unless the
		// caller bridged it. The point of the test: even when the aarv
		// dispatcher routes the request to a mounted handler, no pattern is
		// recorded for any aarv Context that exists in scope.
		w.WriteHeader(http.StatusTeapot)
	})
	app.Mount("/mounted/", mounted)

	// Capture any aarv Context observable from a wrapping middleware so we
	// can assert RoutePattern stays empty on mounted-handler paths.
	var observed string
	observed = "sentinel"
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if c, ok := FromRequest(r); ok {
				observed = c.RoutePattern()
			}
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mounted/foo", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status: want 418, got %d", rec.Code)
	}
	if observed != "" {
		t.Fatalf("RoutePattern on Mount: want empty, got %q", observed)
	}
}

// TestRoutePattern_PoolReuseIndependence verifies that the routePattern field
// is cleared on context pool return: a second request through a different
// dispatch path observes only its own pattern, never the previous request's.
func TestRoutePattern_PoolReuseIndependence(t *testing.T) {
	app := New()
	app.Get("/users/{id}", func(c *Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	var observed string
	observed = "sentinel"
	app.SetNotFoundHandler(func(c *Context) error {
		observed = c.RoutePattern()
		return c.Text(http.StatusNotFound, "nope")
	})

	// First request: matched dynamic route.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/users/1", nil)
	app.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first req status: want 200, got %d", rec1.Code)
	}

	// Second request: 404 — must not see /users/{id} leaked from prior request.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/missing", nil)
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("second req status: want 404, got %d", rec2.Code)
	}
	if observed != "" {
		t.Fatalf("RoutePattern leaked across pool reuse: want empty, got %q", observed)
	}
}

// TestSetLogger_Override asserts that SetLogger replaces the cached logger
// returned by subsequent Logger() calls within the same request.
func TestSetLogger_Override(t *testing.T) {
	app := New()
	custom := slog.New(slog.NewTextHandler(&strings.Builder{}, nil)).With("scope", "custom")
	var got *slog.Logger
	app.Get("/x", func(c *Context) error {
		c.SetLogger(custom)
		got = c.Logger()
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(rec, req)

	if got != custom {
		t.Fatalf("Logger() did not return the override")
	}
}

// TestSetLogger_NilClears asserts that SetLogger(nil) clears any previous
// override, and the next Logger() call rebuilds the default request logger
// from app.logger.
func TestSetLogger_NilClears(t *testing.T) {
	app := New()
	custom := slog.New(slog.NewTextHandler(&strings.Builder{}, nil)).With("scope", "custom")
	var afterClear *slog.Logger
	app.Get("/x", func(c *Context) error {
		c.SetLogger(custom)
		c.SetLogger(nil)
		afterClear = c.Logger()
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	app.ServeHTTP(rec, req)

	if afterClear == custom {
		t.Fatalf("Logger() returned the override after SetLogger(nil)")
	}
	if afterClear == nil {
		t.Fatalf("Logger() returned nil after SetLogger(nil)")
	}
}

// TestSetLogger_NoLeakAcrossRequests asserts that a SetLogger override does
// not survive the request's pool return — the next request gets a fresh
// default logger.
func TestSetLogger_NoLeakAcrossRequests(t *testing.T) {
	app := New()
	custom := slog.New(slog.NewTextHandler(&strings.Builder{}, nil)).With("scope", "custom")
	var firstLogger, secondLogger *slog.Logger
	calls := 0
	app.Get("/x", func(c *Context) error {
		calls++
		switch calls {
		case 1:
			c.SetLogger(custom)
			firstLogger = c.Logger()
		case 2:
			secondLogger = c.Logger()
		}
		return c.Text(http.StatusOK, "ok")
	})

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		app.ServeHTTP(rec, req)
	}

	if firstLogger != custom {
		t.Fatalf("first request: Logger() did not return override")
	}
	if secondLogger == custom {
		t.Fatalf("override leaked into second request")
	}
}

// TestStripMethodPattern_Cases exercises the helper directly; the dispatch
// sites that use it are exercised indirectly by the routingMux fallback
// tests in aarv_test.go and by a successful match against a Mount-prefixed
// pattern in TestRoutePattern_MountedHandler above.
func TestStripMethodPattern_Cases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"GET /users/{id}", "/users/{id}"},
		{"POST /x", "/x"},
		{"/no-method", "/no-method"},
		{"GET ", "GET "},                 // empty rest preserves input (no path after space)
		{"lowercase /x", "lowercase /x"}, // not a stdlib mux pattern shape
	}
	for _, tc := range cases {
		got := stripMethodPattern(tc.in)
		if got != tc.want {
			t.Errorf("stripMethodPattern(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}
}
