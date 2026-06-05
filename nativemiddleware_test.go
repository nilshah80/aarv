package aarv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRegisterNativeMiddleware_DistinctInstancesDoNotCollide is the v0.9.0
// regression guard for the registry-keying issue that motivated the
// redesign. Two distinct NativeMiddleware values, each produced from
// the same source-function literal, must carry independent native
// counterparts and fire their own logic on the correct routes. Before
// v0.9.0 these would have collided on the reflect.ValueOf(m).Pointer()
// keyed registry.
func TestRegisterNativeMiddleware_DistinctInstancesDoNotCollide(t *testing.T) {
	build := func(tag string, calls *int) NativeMiddleware {
		stdlib := Middleware(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("X-Stdlib", tag)
				next.ServeHTTP(w, r)
			})
		})
		native := MiddlewareFunc(func(next HandlerFunc) HandlerFunc {
			return func(c *Context) error {
				*calls++
				c.Response().Header().Add("X-Native", tag)
				return next(c)
			}
		})
		return RegisterNativeMiddleware(stdlib, native)
	}

	var callsA, callsB int
	a := build("a", &callsA)
	b := build("b", &callsB)

	app := New(WithBanner(false))
	app.Use(a, b)
	app.Get("/x", func(c *Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if callsA != 1 || callsB != 1 {
		t.Fatalf("native fns didn't both fire: callsA=%d callsB=%d", callsA, callsB)
	}
	got := rec.Header().Values("X-Native")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("X-Native = %v, want [a b]", got)
	}
}

// TestRegisterNativeMiddleware_PairedShape locks the documented
// invariant: the returned NativeMiddleware's Stdlib and Native fields
// are exactly the values passed in.
func TestRegisterNativeMiddleware_PairedShape(t *testing.T) {
	stdlib := Middleware(func(next http.Handler) http.Handler { return next })
	native := MiddlewareFunc(func(next HandlerFunc) HandlerFunc { return next })
	m := RegisterNativeMiddleware(stdlib, native)
	if m.Stdlib == nil {
		t.Fatal("Stdlib must not be nil")
	}
	if m.Native == nil {
		t.Fatal("Native must not be nil")
	}
}

// TestWrapMiddleware_ReturnsNativeMiddleware confirms WrapMiddleware's
// return type carries both paths (was a single Middleware before v0.9.0).
func TestWrapMiddleware_ReturnsNativeMiddleware(t *testing.T) {
	fn := MiddlewareFunc(func(next HandlerFunc) HandlerFunc { return next })
	mw := WrapMiddleware(fn)
	if mw.Stdlib == nil {
		t.Fatal("Stdlib must not be nil")
	}
	if mw.Native == nil {
		t.Fatal("Native must not be nil for non-nil fn input")
	}
}

// TestWrapMiddleware_NilFn returns a passthrough stdlib middleware with
// no native variant — documented behavior.
func TestWrapMiddleware_NilFn(t *testing.T) {
	mw := WrapMiddleware(nil)
	if mw.Stdlib == nil {
		t.Fatal("Stdlib must be a passthrough, not nil")
	}
	if mw.Native != nil {
		t.Fatal("Native must be nil when fn is nil")
	}
}

func TestAppUse_AcceptsMiddleware(t *testing.T) {
	app := New(WithBanner(false))
	mw := Middleware(func(next http.Handler) http.Handler { return next })
	app.Use(mw) // must not panic
	if len(app.globalMiddleware) != 1 {
		t.Fatalf("globalMiddleware len = %d, want 1", len(app.globalMiddleware))
	}
	if app.globalMiddleware[0].native != nil {
		t.Fatal("bare Middleware must produce a slot with nil native")
	}
}

func TestAppUse_AcceptsNativeMiddleware(t *testing.T) {
	app := New(WithBanner(false))
	mw := RegisterNativeMiddleware(
		func(next http.Handler) http.Handler { return next },
		func(next HandlerFunc) HandlerFunc { return next },
	)
	app.Use(mw)
	if len(app.globalMiddleware) != 1 {
		t.Fatalf("globalMiddleware len = %d, want 1", len(app.globalMiddleware))
	}
	if app.globalMiddleware[0].native == nil {
		t.Fatal("NativeMiddleware must produce a slot with non-nil native")
	}
}

func TestAppUse_AcceptsFuncLiteral(t *testing.T) {
	app := New(WithBanner(false))
	app.Use(func(next http.Handler) http.Handler { return next })
	if len(app.globalMiddleware) != 1 {
		t.Fatalf("globalMiddleware len = %d, want 1", len(app.globalMiddleware))
	}
}

func TestAppUse_PanicsOnNil(t *testing.T) {
	app := New(WithBanner(false))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("App.Use(nil) must panic")
		} else if !strings.Contains(r.(string), "App.Use") {
			t.Fatalf("panic message should name App.Use, got %v", r)
		}
	}()
	app.Use(nil)
}

func TestAppUse_PanicsOnUnsupportedType(t *testing.T) {
	app := New(WithBanner(false))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("App.Use(42) must panic")
		}
	}()
	app.Use(42)
}

// TestAppUse_PanicsOnTypedNilMiddleware covers coerceSlot's
// `case Middleware:` nil-check branch: a typed-nil Middleware
// matches the case (interface non-nil but underlying func is nil),
// and the second `if v == nil` panic fires.
func TestAppUse_PanicsOnTypedNilMiddleware(t *testing.T) {
	app := New(WithBanner(false))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("App.Use(typed nil Middleware) must panic")
		} else if !strings.Contains(r.(string), "nil Middleware") {
			t.Fatalf("panic message should mention nil Middleware, got %v", r)
		}
	}()
	var mw Middleware // typed nil
	app.Use(mw)
}

// TestAppUse_PanicsOnTypedNilHandlerFunc covers coerceSlot's
// `case func(http.Handler) http.Handler:` nil-check branch.
func TestAppUse_PanicsOnTypedNilHandlerFunc(t *testing.T) {
	app := New(WithBanner(false))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("App.Use(typed nil func(http.Handler) http.Handler) must panic")
		} else if !strings.Contains(r.(string), "nil middleware function") {
			t.Fatalf("panic message should mention nil middleware function, got %v", r)
		}
	}()
	var fn func(http.Handler) http.Handler // typed nil
	app.Use(fn)
}

func TestAppUse_PanicsOnNativeMiddlewareWithNilStdlib(t *testing.T) {
	app := New(WithBanner(false))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("App.Use(NativeMiddleware{}) must panic on nil Stdlib")
		}
	}()
	app.Use(NativeMiddleware{})
}

func TestRouteGroupUse_AcceptsMixedTypes(t *testing.T) {
	app := New(WithBanner(false))
	var called bool
	app.Group("/v1", func(g *RouteGroup) {
		g.Use(
			Middleware(func(next http.Handler) http.Handler { return next }),
			RegisterNativeMiddleware(
				func(next http.Handler) http.Handler { return next },
				func(next HandlerFunc) HandlerFunc { return next },
			),
		)
		g.Get("/x", func(c *Context) error {
			called = true
			return c.Text(http.StatusOK, "ok")
		})
	})

	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/x", nil))
	if !called {
		t.Fatal("handler did not run")
	}
}

func TestPluginContextUse_ForwardsAnyVariadic(t *testing.T) {
	// PluginContext.Use delegates to RouteGroup.Use(...any).
	// Simulate by exercising the registered plugin path.
	app := New(WithBanner(false))
	app.Register(&fakePlugin{})
	// If PluginContext.Use's variadic-any forwarding to RouteGroup.Use
	// were broken, registration above would have failed at compile or
	// panicked. The presence of one route confirms the wiring works.
	if len(app.routes) != 1 {
		t.Fatalf("plugin route not registered: %v", app.routes)
	}
}

type fakePlugin struct{}

func (fakePlugin) Name() string    { return "fake" }
func (fakePlugin) Version() string { return "test" }
func (fakePlugin) Register(pc *PluginContext) error {
	pc.Use(
		// Mix all three accepted argument types to exercise coerceSlot.
		Middleware(func(next http.Handler) http.Handler { return next }),
		func(next http.Handler) http.Handler { return next },
		RegisterNativeMiddleware(
			func(next http.Handler) http.Handler { return next },
			func(next HandlerFunc) HandlerFunc { return next },
		),
	)
	pc.Get("/probe", func(c *Context) error { return c.Text(http.StatusOK, "ok") })
	return nil
}

func TestWithRouteMiddleware_AcceptsAny(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/r",
		func(c *Context) error { return c.Text(http.StatusOK, "ok") },
		WithRouteMiddleware(
			Middleware(func(next http.Handler) http.Handler { return next }),
			RegisterNativeMiddleware(
				func(next http.Handler) http.Handler { return next },
				func(next HandlerFunc) HandlerFunc { return next },
			),
		),
	)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/r", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestBuildNativeChain_FailsOnSlotWithoutNative(t *testing.T) {
	slots := []middlewareSlot{
		{stdlib: func(next http.Handler) http.Handler { return next }, native: nil},
	}
	_, ok := buildNativeChain(func(c *Context) error { return nil }, slots)
	if ok {
		t.Fatal("buildNativeChain must return false when any slot has nil native")
	}
}

func TestBuildNativeChain_SucceedsWhenAllSlotsHaveNative(t *testing.T) {
	slots := []middlewareSlot{
		{
			stdlib: func(next http.Handler) http.Handler { return next },
			native: func(next HandlerFunc) HandlerFunc { return next },
		},
	}
	_, ok := buildNativeChain(func(c *Context) error { return nil }, slots)
	if !ok {
		t.Fatal("buildNativeChain must succeed when every slot has a native fn")
	}
}
