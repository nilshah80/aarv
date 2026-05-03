package aarv

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestWithRouteIdempotencyTTL_NegativePanics asserts the construction
// guard rejects a negative duration. A negative TTL would either
// confuse stores (Redis SET EX rejects negatives) or expire entries
// instantly — neither is what the caller meant.
func TestWithRouteIdempotencyTTL_NegativePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on negative TTL")
		}
	}()
	WithRouteIdempotencyTTL(-time.Second)
}

// TestRouteInfo_ExposesIdempotencyTTL ensures App.Routes() surfaces
// the per-route TTL so introspection consumers (e.g. an admin UI)
// can audit configured routes.
func TestRouteInfo_ExposesIdempotencyTTL(t *testing.T) {
	a := New()
	a.Post("/items", func(c *Context) error { return c.NoContent(204) }, WithRouteIdempotencyTTL(15*time.Minute))
	a.Get("/items", func(c *Context) error { return c.NoContent(204) })

	got := a.Routes()
	for _, r := range got {
		if r.Method == "POST" && r.Pattern == "/items" {
			if r.IdempotencyTTL == nil {
				t.Fatalf("POST /items should expose IdempotencyTTL")
			}
			if *r.IdempotencyTTL != 15*time.Minute {
				t.Fatalf("got %v want 15m", *r.IdempotencyTTL)
			}
		}
		if r.Method == "GET" && r.Pattern == "/items" {
			if r.IdempotencyTTL != nil {
				t.Fatalf("GET /items should not have IdempotencyTTL")
			}
		}
	}
}

// TestRouteIdempotencyTTL_RequestTimeAccessor_FastPath verifies the
// accessor wired through the App fast path (App.Get registration).
func TestRouteIdempotencyTTL_RequestTimeAccessor_FastPath(t *testing.T) {
	a := New()
	var seen time.Duration
	var ok bool
	a.Post("/items", func(c *Context) error {
		seen, ok = c.RouteIdempotencyTTL()
		return c.NoContent(204)
	}, WithRouteIdempotencyTTL(7*time.Minute))

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("POST", "/items", nil))
	if !ok {
		t.Fatalf("RouteIdempotencyTTL not found on matched fast-path route")
	}
	if seen != 7*time.Minute {
		t.Fatalf("got %v want 7m", seen)
	}
}

// TestRouteIdempotencyTTL_RequestTimeAccessor_DynamicPath verifies
// the dynamic-route fast path stores and resolves the TTL by
// pattern (not by URL.Path).
func TestRouteIdempotencyTTL_RequestTimeAccessor_DynamicPath(t *testing.T) {
	a := New()
	var seen time.Duration
	var ok bool
	a.Patch("/items/{id}", func(c *Context) error {
		seen, ok = c.RouteIdempotencyTTL()
		return c.NoContent(204)
	}, WithRouteIdempotencyTTL(2*time.Minute))

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("PATCH", "/items/abc", nil))
	if !ok {
		t.Fatalf("RouteIdempotencyTTL not found on matched dynamic-route")
	}
	if seen != 2*time.Minute {
		t.Fatalf("got %v want 2m", seen)
	}
}

// TestRouteIdempotencyTTL_RequestTimeAccessor_GroupPath verifies
// group routes carry the option through to the index, with the
// fully-prefixed pattern as the key.
func TestRouteIdempotencyTTL_RequestTimeAccessor_GroupPath(t *testing.T) {
	a := New()
	var seen time.Duration
	var ok bool
	a.Group("/v1", func(g *RouteGroup) {
		g.Post("/links", func(c *Context) error {
			seen, ok = c.RouteIdempotencyTTL()
			return c.NoContent(204)
		}, WithRouteIdempotencyTTL(time.Hour))
	})

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/links", nil))
	if !ok {
		t.Fatalf("RouteIdempotencyTTL not found on group route")
	}
	if seen != time.Hour {
		t.Fatalf("got %v want 1h", seen)
	}
}

// TestRouteIdempotencyTTL_DynamicGroupPath covers the fourth path:
// dynamic routes inside a group.
func TestRouteIdempotencyTTL_DynamicGroupPath(t *testing.T) {
	a := New()
	var seen time.Duration
	var ok bool
	a.Group("/v1", func(g *RouteGroup) {
		g.Delete("/links/{id}", func(c *Context) error {
			seen, ok = c.RouteIdempotencyTTL()
			return c.NoContent(204)
		}, WithRouteIdempotencyTTL(5*time.Minute))
	})

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("DELETE", "/v1/links/abc", nil))
	if !ok {
		t.Fatalf("RouteIdempotencyTTL not found on dynamic group route")
	}
	if seen != 5*time.Minute {
		t.Fatalf("got %v want 5m", seen)
	}
}

// TestRouteIdempotencyTTL_NotConfiguredReturnsFalse ensures the
// accessor returns false when the option was not used. This is the
// case that lets idempotency middleware fall back to its global TTL.
func TestRouteIdempotencyTTL_NotConfiguredReturnsFalse(t *testing.T) {
	a := New()
	var ok bool
	a.Post("/items", func(c *Context) error {
		_, ok = c.RouteIdempotencyTTL()
		return c.NoContent(204)
	})

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("POST", "/items", nil))
	if ok {
		t.Fatalf("expected RouteIdempotencyTTL to return false for unconfigured route")
	}
}

// TestRouteIdempotencyTTL_ZeroValueOptOut verifies that a zero
// duration is honored as "set, but to zero" — the per-route opt-out
// signal. The accessor returns (0, true) so the idempotency
// middleware can distinguish opt-out from "not configured".
func TestRouteIdempotencyTTL_ZeroValueOptOut(t *testing.T) {
	a := New()
	var seen time.Duration
	var ok bool
	a.Post("/no-cache", func(c *Context) error {
		seen, ok = c.RouteIdempotencyTTL()
		return c.NoContent(204)
	}, WithRouteIdempotencyTTL(0))

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest("POST", "/no-cache", nil))
	if !ok {
		t.Fatalf("zero TTL should still be 'set' (return ok=true)")
	}
	if seen != 0 {
		t.Fatalf("got %v want zero", seen)
	}
}
