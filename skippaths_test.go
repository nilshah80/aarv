package aarv

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeMiddleware records whether it was invoked. Used to assert that
// SkipPaths actually shorts the chain past the inner middleware.
type fakeMiddleware struct {
	stdlibCalls int
}

// stdlib returns the inner middleware as a stdlib Middleware. It bumps
// stdlibCalls every time the wrapped handler is invoked.
func (f *fakeMiddleware) stdlib() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.stdlibCalls++
			w.Header().Set("X-Inner", "stdlib")
			next.ServeHTTP(w, r)
		})
	}
}

func TestSkipPaths_StdlibBypassesInnerOnSkippedPath(t *testing.T) {
	f := &fakeMiddleware{}
	mw := SkipPaths([]string{"/health"}, f.stdlib())

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	f := &fakeMiddleware{}
	mw := SkipPaths([]string{"/health"}, f.stdlib())

	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))

	if f.stdlibCalls != 1 {
		t.Fatalf("inner did not run on non-skipped path: stdlibCalls=%d", f.stdlibCalls)
	}
	if got := rec.Header().Get("X-Inner"); got != "stdlib" {
		t.Fatalf("X-Inner = %q, want %q", got, "stdlib")
	}
}

// TestSkipPaths_DoesNotRegisterNativePair locks in the documented
// stdlib-only behavior. The native middleware registry keys on closure
// code pointers (see middleware.go), so a wrapper helper that
// registered a native variant would let two SkipPaths instances
// silently overwrite each other's pair. Until that registry quirk is
// fixed, SkipPaths must NOT register a native variant — the chain
// builder will detect the missing pair and downgrade to stdlib for
// any route including this middleware. That's a perf regression but
// not a correctness bug, which is the right tradeoff while the
// registry is broken.
func TestSkipPaths_DoesNotRegisterNativePair(t *testing.T) {
	f := &fakeMiddleware{}
	wrapped := SkipPaths([]string{"/health"}, f.stdlib())
	if _, ok := nativeMiddlewareFunc(wrapped); ok {
		t.Fatal("SkipPaths must not register a native pair until the registry-keying issue is fixed; see skippaths.go doc comment")
	}
}

func TestSkipPaths_EmptyPathsReturnsInnerUnchanged(t *testing.T) {
	f := &fakeMiddleware{}
	got := SkipPaths(nil, f.stdlib())

	rec := httptest.NewRecorder()
	got(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))

	if f.stdlibCalls != 1 {
		t.Fatalf("empty paths must not skip; got stdlibCalls=%d", f.stdlibCalls)
	}
}

func TestSkipPaths_NilInnerReturnsNil(t *testing.T) {
	if got := SkipPaths([]string{"/x"}, nil); got != nil {
		t.Fatal("SkipPaths(_, nil) must return nil to make the misuse loud")
	}
}

func TestSkipPaths_MultipleSkipPathsAllShortCircuit(t *testing.T) {
	f := &fakeMiddleware{}
	mw := SkipPaths([]string{"/health", "/ready", "/live", "/metrics"}, f.stdlib())

	for _, path := range []string{"/health", "/ready", "/live", "/metrics"} {
		rec := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	}
	if f.stdlibCalls != 0 {
		t.Fatalf("inner ran on at least one skipped path: stdlibCalls=%d", f.stdlibCalls)
	}

	// Non-listed path still runs inner.
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1", nil))
	if f.stdlibCalls != 1 {
		t.Fatalf("inner did not run on non-listed path: stdlibCalls=%d", f.stdlibCalls)
	}
}
