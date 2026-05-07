package aarv

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestBindRequest_NilContextReturnsRequestUnchanged covers the c==nil
// guard on BindRequest. Defensive: BindRequest is called from plugin
// stdlib paths after FromRequest, which can return (nil, false) on
// non-aarv mounts, and the nil receiver must short-circuit cleanly.
func TestBindRequest_NilContextReturnsRequestUnchanged(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	var c *Context
	if got := c.BindRequest(r); got != r {
		t.Fatal("BindRequest on nil receiver must return r unchanged")
	}
}

// TestBindRequest_NilRequestReturnsCurrentBound covers the r==nil
// guard. Defensive: the only natural way to hit it is misuse, but the
// guard exists so a misuse doesn't nil-deref.
func TestBindRequest_NilRequestReturnsCurrentBound(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/x", func(c *Context) error {
		curRR := c.req
		got := c.BindRequest(nil)
		if got != curRR {
			t.Fatal("BindRequest(nil) must return the currently-bound request")
		}
		return c.NoContent(204)
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 204 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// TestBindRequest_LargeBodyCacheIsDropped covers the
// `cap(c.bodyCache) > maxReusableBodyCache → c.bodyCache = nil`
// branch — large buffers are released rather than retained, matching
// reset()'s allocation discipline.
func TestBindRequest_LargeBodyCacheIsDropped(t *testing.T) {
	app := New(WithBanner(false))
	app.Post("/p", func(c *Context) error {
		// Stuff a deliberately oversized cache buffer onto c. We only
		// need cap > maxReusableBodyCache; len doesn't matter for the
		// branch.
		c.bodyCache = make([]byte, 0, maxReusableBodyCache+1024)
		c.bodyRead = true

		c.BindRequest(httptest.NewRequest("POST", "/p", strings.NewReader("new")))

		if c.bodyCache != nil {
			t.Fatalf("oversized bodyCache should be dropped to nil; got cap=%d", cap(c.bodyCache))
		}
		if c.bodyRead {
			t.Fatal("bodyRead should be reset after BindRequest")
		}
		return c.NoContent(204)
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/p", strings.NewReader("orig")))
	if rec.Code != 204 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
}

// TestBindRequest_BridgeOffDeletesPrevRegistry covers the branch where
// WithRequestContextBridge(false) opts out of the in-context marker
// and instead uses a pointer-keyed registry, so BindRequest must
// remove the previous request's entry to keep the registry from
// leaking. We exercise the branch directly via storeRequestContext +
// BindRequest rather than through the full dispatch path so the test
// focuses on the BindRequest contract, not the dispatcher's bridge
// wiring.
func TestBindRequest_BridgeOffDeletesPrevRegistry(t *testing.T) {
	app := New(WithBanner(false), WithRequestContextBridge(false))
	c := &Context{app: app}

	prev := httptest.NewRequest("POST", "/p", strings.NewReader("orig"))
	storeRequestContext(prev, c)
	c.req = prev
	if got, ok := contextFromRequest(prev); !ok || got != c {
		t.Fatal("setup: prev request should resolve to c via the registry")
	}

	next := httptest.NewRequest("POST", "/p", strings.NewReader("new"))
	got := c.BindRequest(next)
	if got != c.req {
		t.Fatal("BindRequest must return the new bound request")
	}
	if _, ok := contextFromRequest(prev); ok {
		t.Fatal("BindRequest(bridge=false) must delete the previous request's registry entry")
	}
	// Cleanup: the test allocated a registry entry for `next` indirectly
	// (BindRequest under bridge=false doesn't register, but the framework
	// would have on a real request). Nothing to clean here — registry is
	// keyed on `next` only if BindRequest stored it.
}

// TestRouteIdempotencyTTL_NilContextGuards covers the early-return
// guards on RouteIdempotencyTTL when called on a context that hasn't
// been fully initialised by the framework (or is a literal nil
// receiver).
func TestRouteIdempotencyTTL_NilContextGuards(t *testing.T) {
	var c *Context
	if d, ok := c.RouteIdempotencyTTL(); d != 0 || ok {
		t.Fatalf("nil receiver: got (%v, %v); want (0, false)", d, ok)
	}

	// Bare context with no app and no routePattern.
	c2 := &Context{}
	if d, ok := c2.RouteIdempotencyTTL(); d != 0 || ok {
		t.Fatalf("empty Context: got (%v, %v); want (0, false)", d, ok)
	}
}

// TestRouteIdempotencyTTL_PatternNotInMethodMap covers the branch
// where the method IS in the TTL map but the matched route pattern
// is NOT — i.e. some other route under the same method has a TTL,
// but this one does not.
func TestRouteIdempotencyTTL_PatternNotInMethodMap(t *testing.T) {
	app := New(WithBanner(false))
	// Register POST /with-ttl with a TTL, and POST /no-ttl without.
	app.Post("/with-ttl", func(c *Context) error { return c.NoContent(204) }, WithRouteIdempotencyTTL(2*time.Hour))
	app.Post("/no-ttl", func(c *Context) error {
		// Same method as the TTL'd route, different pattern → method
		// is in the outer map but pattern is not in the inner map.
		if d, ok := c.RouteIdempotencyTTL(); d != 0 || ok {
			t.Fatalf("/no-ttl probe: got (%v, %v); want (0, false)", d, ok)
		}
		return c.NoContent(204)
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/no-ttl", nil))
	if rec.Code != 204 {
		t.Fatalf("status = %d", rec.Code)
	}
}

// TestRouteIdempotencyTTL_MethodNotConfigured covers the branch where
// the route pattern matches but the method has no TTL override.
// Triggered by registering a TTL on POST and probing as GET on the
// same pattern.
func TestRouteIdempotencyTTL_MethodNotConfigured(t *testing.T) {
	app := New(WithBanner(false))
	app.Post("/x", func(c *Context) error { return c.NoContent(204) }, WithRouteIdempotencyTTL(2*time.Hour))
	app.Get("/x", func(c *Context) error {
		if d, ok := c.RouteIdempotencyTTL(); d != 0 || ok {
			t.Fatalf("GET probe: got (%v, %v); want (0, false)", d, ok)
		}
		return c.NoContent(204)
	})
	// Confirm POST resolves the TTL (sanity).
	app.Put("/x", func(c *Context) error { // PUT also has no TTL → method-not-in-map
		if d, ok := c.RouteIdempotencyTTL(); d != 0 || ok {
			t.Fatalf("PUT probe: got (%v, %v); want (0, false)", d, ok)
		}
		return c.NoContent(204)
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 204 {
		t.Fatalf("GET status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("PUT", "/x", nil))
	if rec.Code != 204 {
		t.Fatalf("PUT status = %d", rec.Code)
	}
}
