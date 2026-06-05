package aarv

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeMiddleware records whether its stdlib and native variants were
// invoked. Used to assert that SkipPaths actually shorts the chain past
// the inner middleware on both paths.
type fakeMiddleware struct {
	label       string
	stdlibCalls int
	nativeCalls int
}

// stdlibOnly returns a stdlib Middleware that bumps stdlibCalls each
// time it runs. No native pair.
func (f *fakeMiddleware) stdlibOnly() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.stdlibCalls++
			w.Header().Set("X-Inner", "stdlib:"+f.label)
			next.ServeHTTP(w, r)
		})
	}
}

// nativePair returns a NativeMiddleware with both stdlib and native
// counts wired up. Lets tests assert that SkipPaths preserves the
// native fast path under multiple instances.
func (f *fakeMiddleware) nativePair() NativeMiddleware {
	stdlib := Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.stdlibCalls++
			w.Header().Set("X-Inner", "stdlib:"+f.label)
			next.ServeHTTP(w, r)
		})
	})
	native := MiddlewareFunc(func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			f.nativeCalls++
			c.Response().Header().Set("X-Inner", "native:"+f.label)
			return next(c)
		}
	})
	return RegisterNativeMiddleware(stdlib, native)
}

func TestSkipPaths_StdlibBypassesInnerOnSkippedPath(t *testing.T) {
	f := &fakeMiddleware{label: "a"}
	mw := SkipPaths([]string{"/health"}, f.stdlibOnly())

	rec := httptest.NewRecorder()
	mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if f.stdlibCalls != 0 {
		t.Fatalf("inner middleware ran on skipped path: stdlibCalls=%d", f.stdlibCalls)
	}
	if got := rec.Header().Get("X-Inner"); got != "" {
		t.Fatalf("inner left a marker on skipped path: %q", got)
	}
}

func TestSkipPaths_StdlibRunsInnerOnNonSkippedPath(t *testing.T) {
	f := &fakeMiddleware{label: "a"}
	mw := SkipPaths([]string{"/health"}, f.stdlibOnly())

	rec := httptest.NewRecorder()
	mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))

	if f.stdlibCalls != 1 {
		t.Fatalf("inner did not run on non-skipped path: stdlibCalls=%d", f.stdlibCalls)
	}
	if got := rec.Header().Get("X-Inner"); got != "stdlib:a" {
		t.Fatalf("X-Inner = %q, want %q", got, "stdlib:a")
	}
}

// TestSkipPaths_PreservesNativeFastPathAcrossInstances is the v0.9.0
// regression guard. Two distinct SkipPaths wrappers carry their own
// native fns; the chain builder must run each wrapper's native variant
// on its own routes without interference.
func TestSkipPaths_PreservesNativeFastPathAcrossInstances(t *testing.T) {
	a := &fakeMiddleware{label: "a"}
	b := &fakeMiddleware{label: "b"}

	sp1 := SkipPaths([]string{"/skip-a"}, a.nativePair())
	sp2 := SkipPaths([]string{"/skip-b"}, b.nativePair())

	if sp1.Native == nil {
		t.Fatal("sp1.Native must not be nil — inner had a native pair")
	}
	if sp2.Native == nil {
		t.Fatal("sp2.Native must not be nil — inner had a native pair")
	}

	app := New(WithBanner(false))
	app.Use(sp1, sp2)
	app.Get("/api", func(c *Context) error {
		return c.Text(http.StatusOK, "ok")
	})
	app.Get("/skip-a", func(c *Context) error {
		return c.Text(http.StatusOK, "ok")
	})
	app.Get("/skip-b", func(c *Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	// Hot path /api: both inners must run on native (not stdlib).
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))
	if a.nativeCalls != 1 || b.nativeCalls != 1 {
		t.Fatalf("native pair didn't fire on /api: a.native=%d b.native=%d", a.nativeCalls, b.nativeCalls)
	}
	if a.stdlibCalls != 0 || b.stdlibCalls != 0 {
		t.Fatalf("stdlib path fired unexpectedly on /api: a.stdlib=%d b.stdlib=%d", a.stdlibCalls, b.stdlibCalls)
	}

	// /skip-a: a's inner must be skipped; b's inner still runs.
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/skip-a", nil))
	if a.nativeCalls != 1 {
		t.Fatalf("a's inner fired on /skip-a (was %d, want unchanged from 1)", a.nativeCalls)
	}
	if b.nativeCalls != 2 {
		t.Fatalf("b's inner didn't fire on /skip-a: b.native=%d, want 2", b.nativeCalls)
	}

	// /skip-b: b's inner must be skipped; a's inner still runs.
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/skip-b", nil))
	if b.nativeCalls != 2 {
		t.Fatalf("b's inner fired on /skip-b (was %d, want unchanged from 2)", b.nativeCalls)
	}
	if a.nativeCalls != 2 {
		t.Fatalf("a's inner didn't fire on /skip-b: a.native=%d, want 2", a.nativeCalls)
	}
}

func TestSkipPaths_StdlibOnlyInnerDowngradesNative(t *testing.T) {
	f := &fakeMiddleware{label: "stdlib-only"}
	mw := SkipPaths([]string{"/health"}, f.stdlibOnly())
	if mw.Native != nil {
		t.Fatal("inner has no native pair; SkipPaths must leave Native nil to downgrade chain to stdlib")
	}
	if mw.Stdlib == nil {
		t.Fatal("Stdlib must always be populated for non-empty inputs")
	}
}

func TestSkipPaths_EmptyPathsReturnsInnerUnchanged(t *testing.T) {
	f := &fakeMiddleware{label: "a"}
	got := SkipPaths(nil, f.stdlibOnly())

	rec := httptest.NewRecorder()
	got.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))

	if f.stdlibCalls != 1 {
		t.Fatalf("empty paths must not skip; got stdlibCalls=%d", f.stdlibCalls)
	}
}

func TestSkipPaths_AcceptsNativeMiddleware(t *testing.T) {
	f := &fakeMiddleware{label: "a"}
	mw := SkipPaths([]string{"/skip"}, f.nativePair())
	if mw.Stdlib == nil {
		t.Fatal("Stdlib must be populated")
	}
	if mw.Native == nil {
		t.Fatal("Native must be populated when inner is NativeMiddleware")
	}
}

func TestSkipPaths_AcceptsMiddleware(t *testing.T) {
	f := &fakeMiddleware{label: "a"}
	// Pass as Middleware via interface conversion to exercise the
	// Middleware branch of coerceSlot (the stdlibOnly() return is
	// already typed Middleware via the method signature, but this
	// makes the test's intent explicit).
	mw := SkipPaths([]string{"/skip"}, Middleware(f.stdlibOnly()))
	if mw.Stdlib == nil {
		t.Fatal("Stdlib must be populated")
	}
	if mw.Native != nil {
		t.Fatal("Native must be nil when inner is a bare Middleware")
	}
}

func TestSkipPaths_AcceptsFuncLiteral(t *testing.T) {
	called := 0
	fn := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called++
			next.ServeHTTP(w, r)
		})
	}
	mw := SkipPaths([]string{"/skip"}, fn)
	if mw.Stdlib == nil {
		t.Fatal("Stdlib must be populated for func literal")
	}
	rec := httptest.NewRecorder()
	mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))
	if called != 1 {
		t.Fatalf("called = %d, want 1", called)
	}
}

func TestSkipPaths_PanicsOnNilMiddleware(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("SkipPaths(_, nil) must panic")
		}
		msg, _ := r.(string)
		if msg == "" {
			t.Fatalf("panic value = %v, want a non-empty string message", r)
		}
	}()
	SkipPaths([]string{"/x"}, nil)
}

func TestSkipPaths_PanicsOnUnsupportedType(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("SkipPaths must panic on unsupported type")
		}
	}()
	SkipPaths([]string{"/x"}, 42)
}

func TestSkipPaths_PanicsOnNilStdlibInNativeMiddleware(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("SkipPaths must panic on NativeMiddleware with nil Stdlib")
		}
	}()
	SkipPaths([]string{"/x"}, NativeMiddleware{})
}

func TestSkipPaths_MultipleSkipPathsAllShortCircuit(t *testing.T) {
	f := &fakeMiddleware{label: "a"}
	mw := SkipPaths([]string{"/health", "/ready", "/live", "/metrics"}, f.stdlibOnly())

	for _, path := range []string{"/health", "/ready", "/live", "/metrics"} {
		rec := httptest.NewRecorder()
		mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	}
	if f.stdlibCalls != 0 {
		t.Fatalf("inner ran on at least one skipped path: stdlibCalls=%d", f.stdlibCalls)
	}

	rec := httptest.NewRecorder()
	mw.Stdlib(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1", nil))
	if f.stdlibCalls != 1 {
		t.Fatalf("inner did not run on non-listed path: stdlibCalls=%d", f.stdlibCalls)
	}
}
