package pprof

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

// contextDeadlineNow returns a context that is already past its deadline,
// so any handler honoring ctx.Done returns immediately.
func contextDeadlineNow() (context.Context, context.CancelFunc) {
	return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
}

// pprof's /profile and /trace endpoints force a 30-second collection window
// (seconds<=0 in the query is silently treated as 30). To keep the test
// suite fast we exercise:
//   - the index page and the cheap sub-routes (cmdline, symbol) by actually
//     invoking the handler and asserting status + content-type
//   - profile and trace by canceling the request mid-collection via a context
//     with an immediate deadline; the handler returns whatever it has at
//     cancellation, which is sufficient to prove the route is wired.

var fastPprofSubPaths = []struct {
	path        string
	contentType string
}{
	{path: DefaultPrefix + "/", contentType: "text/html"},
	{path: DefaultPrefix + "/cmdline", contentType: "text/plain"},
	{path: DefaultPrefix + "/symbol", contentType: "text/plain"},
}

func TestHandler_DefaultPrefix_FastSubRoutes(t *testing.T) {
	h := Handler(Config{})
	for _, tc := range fastPprofSubPaths {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status: want 200, got %d", rec.Code)
			}
			if tc.contentType != "" {
				ct := rec.Header().Get("Content-Type")
				if !strings.HasPrefix(ct, tc.contentType) {
					t.Fatalf("content-type: want prefix %q, got %q", tc.contentType, ct)
				}
			}
		})
	}
}

// TestHandler_ProfileAndTrace_RoutesWired verifies the /profile and /trace
// routes are registered without paying their collection-duration cost. We
// cancel the request context immediately; pprof respects context cancellation
// and returns promptly, but the response code (and the fact that any code is
// written at all) proves the route reached pprof.Profile / pprof.Trace.
func TestHandler_ProfileAndTrace_RoutesWired(t *testing.T) {
	h := Handler(Config{})
	for _, sub := range []string{"/profile", "/trace"} {
		t.Run(sub, func(t *testing.T) {
			ctx, cancel := contextDeadlineNow()
			defer cancel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, DefaultPrefix+sub+"?seconds=1", nil).WithContext(ctx)
			h.ServeHTTP(rec, req)
			// pprof writes its content-type header before collection begins.
			// Asserting the header was set is enough to prove the route fired.
			if rec.Header().Get("Content-Type") == "" {
				t.Fatalf("%s: route did not reach pprof handler (no Content-Type set)", sub)
			}
		})
	}
}

func TestHandler_CustomPrefix(t *testing.T) {
	h := Handler(Config{Prefix: "/_debug"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_debug/cmdline", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
}

func TestHandler_AuthMiddlewareBlocksWithout401(t *testing.T) {
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	h := Handler(Config{AuthMiddleware: auth})

	t.Run("blocks without auth", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/cmdline", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status: want 401, got %d", rec.Code)
		}
	})

	t.Run("passes with auth", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/cmdline", nil)
		req.Header.Set("Authorization", "Bearer secret")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d", rec.Code)
		}
	})
}

func TestHandler_SkipPaths_Excludes(t *testing.T) {
	h := Handler(Config{SkipPaths: []string{DefaultPrefix + "/cmdline"}})

	t.Run("skipped path returns 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/cmdline", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status: want 404 from skip, got %d", rec.Code)
		}
	})

	t.Run("non-skipped path still works", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/symbol", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d", rec.Code)
		}
	})
}

func TestNew_PassesThroughForNonPrefixedPaths(t *testing.T) {
	mw := New(Config{})
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	mw(next).ServeHTTP(rec, req)
	if !called {
		t.Fatal("next not called for non-pprof path")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status: want 418, got %d", rec.Code)
	}
}

func TestNew_HandlesPprofPaths(t *testing.T) {
	mw := New(Config{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next must not be called for pprof path")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/cmdline", nil)
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
}

func TestNew_NativePathHandlesPprof(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{}))
	// Register a route that should NOT be reached for pprof paths.
	app.Get("/health", func(c *aarv.Context) error {
		return c.Text(http.StatusOK, "ok")
	})

	t.Run("health route still works", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d", rec.Code)
		}
	})

	t.Run("pprof intercepted via middleware", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/cmdline", nil)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d", rec.Code)
		}
	})
}

// TestHandler_AppMountStripsPrefix confirms Handler restores cfg.Prefix
// when used through App.Mount, which strips the mount prefix before
// invoking the handler. Without internal restoration, the inner mux's
// registered routes (rooted at cfg.Prefix) would 404, and pprof.Index's
// hardcoded "/debug/pprof/" prefix check would fail to dispatch sub-profiles.
func TestHandler_AppMountStripsPrefix(t *testing.T) {
	app := aarv.New(aarv.WithBanner(false))
	app.Mount(DefaultPrefix, Handler(Config{}))

	t.Run("named endpoint reachable through Mount", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/cmdline", nil)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d (Mount stripping must be reversed)", rec.Code)
		}
	})

	t.Run("index reachable through Mount", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/", nil)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d", rec.Code)
		}
	})

	t.Run("index sub-profile dispatch through Mount", func(t *testing.T) {
		// /debug/pprof/heap routes through pprof.Index, which uses a
		// hardcoded "/debug/pprof/" prefix check; the path-restoration
		// inside Handler is what makes this case work.
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/heap?debug=1", nil)
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d", rec.Code)
		}
	})
}

// TestHandler_PrefixRestoreEdgeCases exercises the path-rewrite branches
// for empty and non-slash-prefixed stripped paths.
func TestHandler_PrefixRestoreEdgeCases(t *testing.T) {
	h := Handler(Config{})
	t.Run("empty stripped path becomes prefix", func(t *testing.T) {
		// Direct invocation simulating Mount stripping the entire prefix
		// down to "" — the rewrite should produce exactly cfg.Prefix.
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.Path = ""
		h.ServeHTTP(rec, req)
		// The Index page is registered at both DefaultPrefix and
		// DefaultPrefix+"/"; we don't assert status because an empty path
		// is an unusual case in practice. The branch is exercised either
		// way and the test must not panic.
	})

	t.Run("non-slash stripped path", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/cmdline", nil)
		// Simulate a stripped path without a leading slash (defensive
		// branch — App.Mount adds a leading slash, but the helper still
		// handles this shape).
		req.URL.Path = "cmdline"
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("non-slash path: status %d", rec.Code)
		}
	})
}

// TestNew_NativePath_InterceptsBeforeHandler exercises the native
// aarv.MiddlewareFunc branch where the request path matches the pprof
// prefix. The aarv-registered handler at the same path must NOT be reached
// because the pprof middleware intercepts first via the native fast path.
func TestNew_NativePath_InterceptsBeforeHandler(t *testing.T) {
	app := aarv.New()
	app.Use(New(Config{}))
	app.Get(DefaultPrefix+"/cmdline", func(c *aarv.Context) error {
		t.Fatal("aarv handler must not be reached for pprof path")
		return nil
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, DefaultPrefix+"/cmdline", nil)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 from pprof, got %d", rec.Code)
	}
}

func TestMatchesPrefix_BoundaryCases(t *testing.T) {
	cases := []struct {
		path, prefix string
		want         bool
	}{
		{"/debug/pprof", "/debug/pprof", true},
		{"/debug/pprof/", "/debug/pprof", true},
		{"/debug/pprof/heap", "/debug/pprof", true},
		{"/debug/pprofX", "/debug/pprof", false}, // must not match adjacent prefix
		{"/debug", "/debug/pprof", false},        // partial below prefix
		{"/api/users", "/debug/pprof", false},
		{"", "/debug/pprof", false},
	}
	for _, tc := range cases {
		got := matchesPrefix(tc.path, tc.prefix)
		if got != tc.want {
			t.Errorf("matchesPrefix(%q, %q): want %v, got %v", tc.path, tc.prefix, tc.want, got)
		}
	}
}

func TestNormalize_PanicsOnMalformedPrefix(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
	}{
		{"missing leading slash", "debug/pprof"},
		{"trailing slash", "/debug/pprof/"},
		{"bare root", "/"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("normalize(%q) did not panic", tc.prefix)
				}
			}()
			normalize(Config{Prefix: tc.prefix})
		})
	}
}

func TestNormalize_DefaultsEmptyPrefix(t *testing.T) {
	got := normalize(Config{})
	if got.Prefix != DefaultPrefix {
		t.Fatalf("prefix default: want %q, got %q", DefaultPrefix, got.Prefix)
	}
}

func TestHandler_PanicsOnMalformedPrefix(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Handler did not panic on bad prefix")
		}
	}()
	Handler(Config{Prefix: "no-slash"})
}

func TestNew_PanicsOnMalformedPrefix(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("New did not panic on bad prefix")
		}
	}()
	New(Config{Prefix: "no-slash"})
}

// TestSkipSet_Empty verifies that the helper returns nil for an empty input
// — caller code uses len() == 0 as a fast bypass.
func TestSkipSet_Empty(t *testing.T) {
	if got := skipSet(nil); got != nil {
		t.Fatalf("skipSet(nil): want nil, got %v", got)
	}
	if got := skipSet([]string{}); got != nil {
		t.Fatalf("skipSet(empty): want nil, got %v", got)
	}
}
