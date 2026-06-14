package aarv

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func passThrough() Middleware {
	return func(next http.Handler) http.Handler { return next }
}

// TestNamedMiddlewareInRouteInfo: explicit names from NamedMiddleware and
// NativeMiddleware.Name appear in RouteInfo.Middleware in execution order.
func TestNamedMiddlewareInRouteInfo(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/x", func(c *Context) error { return c.NoContent(204) },
		WithRouteMiddleware(
			NamedMiddleware("auth", passThrough()),
			NativeMiddleware{Stdlib: passThrough(), Name: "ratelimit"},
		),
	)

	routes := app.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if got := routes[0].Middleware; !slices.Equal(got, []string{"auth", "ratelimit"}) {
		t.Fatalf("expected [auth ratelimit], got %v", got)
	}
}

// TestNamedMiddlewareGroupOrdering: group middleware precede route middleware
// (outermost first), and app-global middleware is excluded.
func TestNamedMiddlewareGroupOrdering(t *testing.T) {
	app := New(WithBanner(false))
	app.Use(NamedMiddleware("global", passThrough()))

	app.Group("/api", func(g *RouteGroup) {
		g.Use(NamedMiddleware("group-a", passThrough()), NamedMiddleware("group-b", passThrough()))
		g.Get("/y", func(c *Context) error { return c.NoContent(204) },
			WithRouteMiddleware(NamedMiddleware("route-a", passThrough())),
		)
	})

	routes := app.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	want := []string{"group-a", "group-b", "route-a"}
	if got := routes[0].Middleware; !slices.Equal(got, want) {
		t.Fatalf("expected %v (no global), got %v", want, got)
	}
	if got := app.GlobalMiddleware(); !slices.Equal(got, []string{"global"}) {
		t.Fatalf("expected global middleware [global], got %v", got)
	}
}

// TestUnnamedMiddlewareFallbackLabel: unnamed middleware get a non-empty
// best-effort label (not a stable contract, just non-empty and not "unknown").
func TestUnnamedMiddlewareFallbackLabel(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/z", func(c *Context) error { return c.NoContent(204) },
		WithRouteMiddleware(passThrough()),
	)

	mw := app.Routes()[0].Middleware
	if len(mw) != 1 {
		t.Fatalf("expected 1 middleware label, got %v", mw)
	}
	if mw[0] == "" || mw[0] == "unknown" {
		t.Fatalf("expected a best-effort label, got %q", mw[0])
	}
}

// TestRouteInfoMiddlewareDeepCopy: mutating the returned slice must not affect
// framework state or later Routes() calls.
func TestRouteInfoMiddlewareDeepCopy(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/c", func(c *Context) error { return c.NoContent(204) },
		WithRouteMiddleware(NamedMiddleware("orig", passThrough())),
	)

	first := app.Routes()
	first[0].Middleware[0] = "mutated"

	second := app.Routes()
	if second[0].Middleware[0] != "orig" {
		t.Fatalf("Routes() leaked internal state: got %q", second[0].Middleware[0])
	}
}

// TestRouteWithoutMiddlewareHasNilSlice: routes with no route/group middleware
// report a nil Middleware slice (omitempty keeps JSON clean).
func TestRouteWithoutMiddlewareHasNilSlice(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/none", func(c *Context) error { return c.NoContent(204) })
	if mw := app.Routes()[0].Middleware; mw != nil {
		t.Fatalf("expected nil Middleware for a plain route, got %v", mw)
	}
}

// TestNamedMiddlewarePreservesNativePath: NamedMiddleware must not drop the
// native MiddlewareFunc, so the wrapped middleware still runs.
func TestNamedMiddlewarePreservesNativePath(t *testing.T) {
	var ran bool
	native := WrapMiddleware(func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			ran = true
			return next(c)
		}
	})
	named := NamedMiddleware("tracer", native)
	if named.Native == nil {
		t.Fatal("NamedMiddleware dropped the native MiddlewareFunc")
	}

	app := New(WithBanner(false))
	app.Use(named)
	app.Get("/run", func(c *Context) error { return c.NoContent(204) })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/run", nil))
	if !ran {
		t.Fatal("named middleware did not execute")
	}
}
