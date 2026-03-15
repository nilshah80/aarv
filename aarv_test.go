package aarv

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestHelloWorld(t *testing.T) {
	app := New(WithBanner(false))
	app.Get("/hello", func(c *Context) error {
		return c.JSON(200, map[string]string{"message": "hello"})
	})

	tc := NewTestClient(app)
	resp := tc.Get("/hello")
	resp.AssertStatus(t, 200)

	var body map[string]string
	if err := resp.JSON(&body); err != nil {
		t.Fatal(err)
	}
	if body["message"] != "hello" {
		t.Errorf("expected 'hello', got %q", body["message"])
	}
}

func TestAppOptions(t *testing.T) {
	app := New(
		WithBanner(false),
		WithMaxBodySize(1024),
		WithReadTimeout(time.Second),
		WithWriteTimeout(time.Second),
		WithIdleTimeout(time.Second),
	)

	if app.config.Banner != false {
		t.Errorf("WithBanner option not applied")
	}
	if app.config.MaxBodySize != 1024 {
		t.Errorf("WithMaxBodySize option not applied")
	}
	if app.config.ReadTimeout != time.Second {
		t.Errorf("WithReadTimeout option not applied")
	}
}

func TestListenShutdownGraceful(t *testing.T) {
	app := New(WithBanner(false))

	app.Get("/hc", func(c *Context) error {
		return c.Text(200, "ok")
	})

	go func() {
		// Attempt to listen on random ephemeral port
		_ = app.Listen("127.0.0.1:0")
	}()

	// Give the server a moment to start
	time.Sleep(50 * time.Millisecond)

	// Send an interrupt signal to gracefully shut down
	p := syscall.Getpid()
	_ = syscall.Kill(p, syscall.SIGINT)

	// The app.Listen() should return cleanly after graceful shutdown

	// Direct shutdown
	err := app.Shutdown(context.Background())
	if err != nil && err.Error() != "http: Server closed" {
		t.Errorf("Expected nil or 'http: Server closed' on subsequent shutdown, got %v", err)
	}
}

func TestListenWithBanner(t *testing.T) {
	app := New(WithBanner(true))
	app.Get("/hc", func(c *Context) error { return c.Text(http.StatusOK, "ok") })

	go func() {
		_ = app.Listen("127.0.0.1:0")
	}()
	time.Sleep(50 * time.Millisecond)
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
}

func TestAppAdditionalCoverage(t *testing.T) {
	t.Run("acquire release and redirect slash", func(t *testing.T) {
		app := New(WithBanner(false), WithRedirectTrailingSlash(true))
		if app.SetNotFoundHandler(func(c *Context) error { return c.Text(404, "missing") }) != app {
			t.Fatal("SetNotFoundHandler should return the app")
		}
		if app.SetMethodNotAllowedHandler(func(c *Context) error { return c.Text(405, "denied") }) != app {
			t.Fatal("SetMethodNotAllowedHandler should return the app")
		}
		app.Get("/slash/", func(c *Context) error { return c.Text(200, "ok") })

		req := httptest.NewRequest(http.MethodGet, "/slash", nil)
		rec := httptest.NewRecorder()
		ctx := app.AcquireContext(rec, req)
		if ctx.app != app || ctx.Request() != req || ctx.Response() != rec {
			t.Fatal("AcquireContext did not initialize the context")
		}
		app.ReleaseContext(ctx)

		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("expected redirect, got %d", rec.Code)
		}
		if rec.Header().Get("Location") != "/slash/" {
			t.Fatalf("unexpected redirect target %q", rec.Header().Get("Location"))
		}

		if !app.shouldRedirectTrailingSlash(httptest.NewRequest(http.MethodGet, "/slash", nil)) {
			t.Fatal("expected redirect helper to match alternative route")
		}
	})

	t.Run("default handlers and on send", func(t *testing.T) {
		app := New(WithBanner(false), WithRedirectTrailingSlash(true))
		app.AddHook(OnSend, func(c *Context) error {
			if bw, ok := c.Response().(*bufferedResponseWriter); ok {
				bw.SetBody([]byte("mutated"))
			}
			return nil
		})
		app.Get("/send/", func(c *Context) error { return c.Text(http.StatusOK, "body") })

		req := httptest.NewRequest(http.MethodGet, "/send?x=1", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/send/?x=1" {
			t.Fatalf("unexpected redirect with query: code=%d loc=%q", rec.Code, rec.Header().Get("Location"))
		}

		resp := NewTestClient(app).Get("/send/")
		if resp.Text() != "mutated" {
			t.Fatalf("expected on-send hook to mutate body, got %q", resp.Text())
		}

		ctx, rec := newAppContext(app, httptest.NewRequest(http.MethodGet, "/", nil))
		ctx.Set("requestId", "req-123")
		if err := app.notFoundHandler(ctx); err != nil || rec.Code != http.StatusNotFound {
			t.Fatalf("unexpected default 404 handler result: code=%d err=%v", rec.Code, err)
		}
		ctx, rec = newAppContext(app, httptest.NewRequest(http.MethodPost, "/", nil))
		ctx.Set("requestId", "req-123")
		if err := app.methodNotAllowedHandler(ctx); err != nil || rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("unexpected default 405 handler result: code=%d err=%v", rec.Code, err)
		}

		altApp := New(WithBanner(false), WithRedirectTrailingSlash(true))
		altApp.Get("/trim", func(c *Context) error { return c.NoContent(http.StatusOK) })
		rec = httptest.NewRecorder()
		altApp.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/trim/", nil))
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/trim" {
			t.Fatalf("unexpected trim redirect: code=%d loc=%q", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("route body limit and pattern helpers", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Post("/limited", func(c *Context) error {
			_, err := c.Body()
			return err
		}, WithRouteMaxBodySize(1))

		resp := NewTestClient(app).Post("/limited", map[string]string{"v": "too large"})
		resp.AssertStatus(t, http.StatusInternalServerError)

		if !matchesPattern("/users/{id}", "/users/10") {
			t.Fatal("expected param pattern to match")
		}
		if !matchesPattern("/files/{path...}", "/files/a/b") {
			t.Fatal("expected wildcard pattern to match")
		}
		if matchesPattern("/users/{id}", "/users") {
			t.Fatal("unexpected pattern match")
		}
		if !matchPatternPart("{id}", "42") {
			t.Fatal("expected part wildcard to match")
		}
		if matchesPattern("/users/{id}", "/admins/10") {
			t.Fatal("unexpected mismatch pattern result")
		}
		if app.shouldRedirectTrailingSlash(httptest.NewRequest(http.MethodGet, "/missing", nil)) {
			t.Fatal("unexpected redirect helper result for missing route")
		}
	})

	t.Run("build route chain fast early returns", func(t *testing.T) {
		app := New(WithBanner(false))
		app.buildRouteChainFast()
		if len(app.routeChainFast) != 0 {
			t.Fatalf("expected no fast route chains without middleware/routes, got %#v", app.routeChainFast)
		}

		app = New(WithBanner(false))
		app.Use(func(next http.Handler) http.Handler { return next })
		app.routeHandlerFast[http.MethodGet] = map[string]routeRuntimeHandler{}
		app.buildRouteChainFast()
		if routes, ok := app.routeChainFast[http.MethodGet]; ok && len(routes) != 0 {
			t.Fatalf("expected empty fast route chain map for empty route set, got %#v", routes)
		}
	})
}

func TestServerLifecycleAdditionalCoverage(t *testing.T) {
	t.Run("startup hook failure", func(t *testing.T) {
		app := New(WithBanner(false))
		app.AddHook(OnStartup, func(*Context) error { return errors.New("boom") })
		if err := app.Listen("127.0.0.1:0"); err == nil || !strings.Contains(err.Error(), "startup hook failed") {
			t.Fatalf("expected startup hook error, got %v", err)
		}
	})

	t.Run("listen helpers and shutdown flows", func(t *testing.T) {
		app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
		server := &http.Server{}
		app.setServer(server)
		var hookCalled, legacyCalled bool
		app.AddHook(OnShutdown, func(*Context) error {
			hookCalled = true
			return nil
		})
		app.OnShutdown(func(interface{ Done() <-chan struct{} }) error {
			legacyCalled = true
			return errors.New("legacy failed")
		})

		err := app.listenAndShutdown(server, func() error { return http.ErrServerClosed })
		if err != nil && err.Error() != "http: Server closed" {
			t.Fatalf("expected nil or closed error, got %v", err)
		}
		if !hookCalled || !legacyCalled {
			t.Fatal("expected shutdown hooks to run")
		}

		if err := app.listenAndShutdown(server, func() error { return errors.New("serve failed") }); err == nil || !strings.Contains(err.Error(), "server error") {
			t.Fatalf("expected wrapped server error, got %v", err)
		}
	})

	t.Run("tls helpers and banner", func(t *testing.T) {
		app := New(WithBanner(false), WithDisableHTTP2(true))
		if err := app.ListenTLS("127.0.0.1:0", "missing-cert.pem", "missing-key.pem"); err == nil {
			t.Fatal("expected ListenTLS failure for missing certificates")
		}
		if err := New(WithBanner(true)).ListenTLS("127.0.0.1:0", "missing-cert.pem", "missing-key.pem"); err == nil {
			t.Fatal("expected ListenTLS failure with banner enabled")
		}
		startupErrApp := New(WithBanner(false))
		startupErrApp.AddHook(OnStartup, func(*Context) error { return errors.New("tls startup") })
		if err := startupErrApp.ListenTLS("127.0.0.1:0", "missing-cert.pem", "missing-key.pem"); err == nil || !strings.Contains(err.Error(), "startup hook failed") {
			t.Fatalf("expected startup hook failure for ListenTLS, got %v", err)
		}

		caFile := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(caFile, []byte("not a pem"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := app.ListenMutualTLS("127.0.0.1:0", "missing-cert.pem", "missing-key.pem", "missing-ca.pem"); err == nil {
			t.Fatal("expected mutual TLS client CA read failure")
		}
		mtlsStartupErrApp := New(WithBanner(false))
		mtlsStartupErrApp.AddHook(OnStartup, func(*Context) error { return errors.New("mtls startup") })
		if err := os.WriteFile(caFile, []byte("not a pem"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := mtlsStartupErrApp.ListenMutualTLS("127.0.0.1:0", "missing-cert.pem", "missing-key.pem", caFile); err == nil || !strings.Contains(err.Error(), "startup hook failed") {
			t.Fatalf("expected startup hook failure for mutual TLS, got %v", err)
		}
		appWithPool := New(WithBanner(false), WithTLSConfig(&tls.Config{ClientCAs: newCertPool()}))
		if err := appWithPool.ListenMutualTLS("127.0.0.1:0", "missing-cert.pem", "missing-key.pem", caFile); err == nil {
			t.Fatal("expected mutual TLS listener failure with preconfigured pool")
		}
		if err := New(WithBanner(true)).ListenMutualTLS("127.0.0.1:0", "missing-cert.pem", "missing-key.pem", caFile); err == nil {
			t.Fatal("expected mutual TLS listener failure with banner enabled")
		}
		if err := app.ListenMutualTLS("127.0.0.1:0", "missing-cert.pem", "missing-key.pem", caFile); err == nil {
			t.Fatal("expected mutual TLS listener failure")
		}

		app.printBanner("127.0.0.1:0", "HTTP")
		if newCertPool() == nil {
			t.Fatal("expected certificate pool")
		}
	})

	t.Run("shutdown without server", func(t *testing.T) {
		if err := New(WithBanner(false)).Shutdown(context.Background()); err != nil {
			t.Fatalf("expected nil shutdown without server, got %v", err)
		}
	})
}

func TestDirectRoutingAdditionalCoverage(t *testing.T) {
	t.Run("serveDirect covers static and dynamic routes", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Get("/static", func(c *Context) error {
			return c.Text(http.StatusOK, "static")
		})
		app.Get("/users/{id}", func(c *Context) error {
			return c.JSON(http.StatusOK, map[string]string{
				"id":      c.Param("id"),
				"request": c.Request().PathValue("id"),
			})
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/static", nil)
		ctx := app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)
		if !app.serveDirect(ctx, rec, req) {
			t.Fatal("expected direct static route dispatch")
		}
		if rec.Code != http.StatusOK || rec.Body.String() != "static" {
			t.Fatalf("unexpected static direct response code=%d body=%q", rec.Code, rec.Body.String())
		}

		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/users/42", nil)
		ctx = app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)
		if !app.serveDirect(ctx, rec, req) {
			t.Fatal("expected direct dynamic route dispatch")
		}
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"42"`) || !strings.Contains(rec.Body.String(), `"request":"42"`) {
			t.Fatalf("unexpected dynamic direct response code=%d body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("serveDirect decodes wildcard path values and catch-all routes", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Get("/files/{path...}", func(c *Context) error {
			return c.JSON(http.StatusOK, map[string]string{"path": c.Param("path")})
		})
		app.Get("/users/{id}", func(c *Context) error {
			return c.JSON(http.StatusOK, map[string]string{"id": c.Param("id")})
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/files/a%2Fb/c", nil)
		ctx := app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)
		if !app.serveDirect(ctx, rec, req) {
			t.Fatal("expected catch-all direct route dispatch")
		}
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path":"a/b/c"`) {
			t.Fatalf("unexpected catch-all direct response code=%d body=%q", rec.Code, rec.Body.String())
		}

		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/users/one/posts", nil)
		ctx = app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)
		if app.serveDirect(ctx, rec, req) {
			t.Fatal("expected non-matching dynamic route to skip direct dispatch")
		}
	})

	t.Run("serveDirect false branches and routing mux fallback", func(t *testing.T) {
		app := New(WithBanner(false))
		app.mux.Handle("/mounted/", http.StripPrefix("/mounted", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		})))
		app.Get("/middleware", func(c *Context) error {
			return c.NoContent(http.StatusAccepted)
		}, WithRouteMiddleware(func(next http.Handler) http.Handler { return next }))

		req := httptest.NewRequest(http.MethodGet, "/missing", nil)
		rec := httptest.NewRecorder()
		ctx := app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)
		if app.serveDirect(ctx, rec, req) {
			t.Fatal("expected missing route to skip direct dispatch")
		}

		req = httptest.NewRequest(http.MethodGet, "/mounted/x", nil)
		rec = httptest.NewRecorder()
		ctx = app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)
		if app.serveDirect(ctx, rec, req) {
			t.Fatal("expected non-aarv mounted route to skip direct dispatch")
		}

		req = httptest.NewRequest(http.MethodGet, "/middleware", nil)
		rec = httptest.NewRecorder()
		ctx = app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)
		if !app.serveDirect(ctx, rec, req) {
			t.Fatal("expected tracked route with route middleware to dispatch through mux handler")
		}
		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected mux-served route to succeed with attached framework context, got %d", rec.Code)
		}

		dynApp := New(WithBanner(false))
		dynApp.Get("/users/{id}", func(c *Context) error {
			return c.JSON(http.StatusOK, map[string]string{"id": c.Param("id")})
		}, WithRouteMiddleware(func(next http.Handler) http.Handler { return next }))

		req = httptest.NewRequest(http.MethodGet, "/users/77", nil)
		rec = httptest.NewRecorder()
		ctx = dynApp.AcquireContext(rec, req)
		defer dynApp.ReleaseContext(ctx)
		if !dynApp.serveDirect(ctx, rec, req) {
			t.Fatal("expected dynamic route with middleware to dispatch through mux")
		}
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"77"`) {
			t.Fatalf("unexpected dynamic mux fallback response code=%d body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("direct pattern helpers", func(t *testing.T) {
		app := New(WithBanner(false))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)

		root := compileDirectPattern("/")
		if !root.match("/", ctx) {
			t.Fatal("expected empty direct pattern to match root path")
		}

		ctx.reset(rec, httptest.NewRequest(http.MethodGet, "/users/99", nil))
		if !compileDirectPattern("/users/{id}").match("/users/99", ctx) {
			t.Fatal("expected param direct pattern to match")
		}
		if got := ctx.Param("id"); got != "99" {
			t.Fatalf("expected cached direct param, got %q", got)
		}

		ctx.reset(rec, httptest.NewRequest(http.MethodGet, "/users", nil))
		if compileDirectPattern("/users/{id}").match("/users", ctx) {
			t.Fatal("unexpected direct pattern match for missing param")
		}

		ctx.reset(rec, httptest.NewRequest(http.MethodGet, "/files", nil))
		if !compileDirectPattern("/files/{path...}").match("/files", ctx) {
			t.Fatal("expected catch-all direct pattern to match empty tail")
		}
		if got := ctx.Param("path"); got != "" {
			t.Fatalf("expected empty catch-all param, got %q", got)
		}

		if got := decodePathValue("%zz"); got != "%zz" {
			t.Fatalf("expected invalid escape to be returned unchanged, got %q", got)
		}
		if got := decodePathValue("a%2Fb"); got != "a/b" {
			t.Fatalf("expected valid escape to be decoded, got %q", got)
		}
	})

	t.Run("routing mux mounted handler no-write fallback and 405 without context", func(t *testing.T) {
		app := New(WithBanner(false))
		app.SetNotFoundHandler(func(c *Context) error {
			return c.Text(http.StatusNotFound, "custom-not-found")
		})
		app.SetMethodNotAllowedHandler(func(c *Context) error {
			return c.Text(http.StatusMethodNotAllowed, "custom-method")
		})
		app.Get("/users/{id}", func(c *Context) error { return c.NoContent(http.StatusOK) })

		app.mux.Handle("/mount/", http.StripPrefix("/mount", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))

		rm := &routingMux{
			mux:           app.mux,
			app:           app,
			routesByKey:   app.routesByKey,
			routeHandlers: app.routeHandlers,
		}

		req := httptest.NewRequest(http.MethodGet, "/mount/child", nil)
		rec := httptest.NewRecorder()
		ctx := app.AcquireContext(rec, req)
		defer app.ReleaseContext(ctx)
		storeRequestContext(req, ctx)
		defer deleteRequestContext(req)
		rm.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || rec.Body.String() != "custom-not-found" {
			t.Fatalf("unexpected mounted fallback response code=%d body=%q", rec.Code, rec.Body.String())
		}

		req = httptest.NewRequest(http.MethodPost, "/users/42", nil)
		rec = httptest.NewRecorder()
		rm.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected stdlib 405 without aarv context, got %d", rec.Code)
		}
	})
}

func TestAddRouteMiddlewarePathAdditionalCoverage(t *testing.T) {
	t.Run("route middleware body limit and handler error use wrapped route handler", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			})
		})
		app.Post("/limited", func(c *Context) error {
			_, err := c.Body()
			return err
		}, WithRouteMaxBodySize(1), WithRouteMiddleware(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			})
		}))

		resp := NewTestClient(app).Post("/limited", map[string]string{"value": "too-large"})
		resp.AssertStatus(t, http.StatusInternalServerError)
	})

	t.Run("dynamic route with global middleware goes through routing mux servehttp path", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-MW", "true")
				next.ServeHTTP(w, r)
			})
		})
		app.Get("/users/{id}", func(c *Context) error {
			return c.JSON(http.StatusOK, map[string]string{"id": c.Param("id")})
		})

		resp := NewTestClient(app).Get("/users/99")
		resp.AssertStatus(t, http.StatusOK)
		if resp.Headers.Get("X-MW") != "true" {
			t.Fatalf("expected middleware header, got %q", resp.Headers.Get("X-MW"))
		}
		if !strings.Contains(resp.Text(), `"id":"99"`) {
			t.Fatalf("unexpected body %q", resp.Text())
		}
	})

	t.Run("exact route with global middleware uses prebuilt chain fast path", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-MW", "true")
				next.ServeHTTP(w, r)
			})
		})
		var onResponseCalled bool
		app.AddHook(OnResponse, func(c *Context) error {
			onResponseCalled = true
			return nil
		})
		app.Get("/fast", func(c *Context) error {
			return c.Text(http.StatusOK, "fast")
		})

		resp := NewTestClient(app).Get("/fast")
		resp.AssertStatus(t, http.StatusOK)
		if resp.Headers.Get("X-MW") != "true" {
			t.Fatalf("expected middleware header, got %q", resp.Headers.Get("X-MW"))
		}
		if resp.Text() != "fast" {
			t.Fatalf("unexpected body %q", resp.Text())
		}
		if !onResponseCalled {
			t.Fatal("expected OnResponse hook to run")
		}
	})
}

func TestAppInternalBranchCoverage(t *testing.T) {
	t.Run("with prefix and mount branch", func(t *testing.T) {
		var prefix string
		WithPrefix("/api")(&prefix)
		if prefix != "/api" {
			t.Fatalf("unexpected plugin prefix: %q", prefix)
		}

		app := New(WithBanner(false))
		app.Mount("/mounted", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}))
		resp := NewTestClient(app).Get("/mounted/test")
		resp.AssertStatus(t, http.StatusOK)
	})

	t.Run("serve http hooks and routing branches", func(t *testing.T) {
		app := New(WithBanner(false))
		var onResponseCalled bool
		app.AddHook(OnResponse, func(c *Context) error {
			onResponseCalled = true
			return nil
		})
		app.Get("/ok", func(c *Context) error { return c.Text(http.StatusOK, "ok") })

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ok", nil)
		app.ServeHTTP(rec, req)
		if !onResponseCalled || rec.Code != http.StatusOK {
			t.Fatalf("unexpected serve result: onResponse=%v code=%d", onResponseCalled, rec.Code)
		}

		mux := &routingMux{
			mux:         http.NewServeMux(),
			app:         app,
			routesByKey: map[string]struct{}{"GET /ok": {}},
		}
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ok", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 fallback without context, got %d", rec.Code)
		}

		app.SetMethodNotAllowedHandler(func(c *Context) error { return errors.New("405 fail") })
		app.SetNotFoundHandler(func(c *Context) error { return errors.New("404 fail") })
		req = httptest.NewRequest(http.MethodPost, "/ok", nil)
		ctx, rec := newAppContext(app, req)
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, ctx))
		ctx.req = req
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected fallback error handling on method not allowed failure, got %d", rec.Code)
		}

		mux = &routingMux{
			mux:         http.NewServeMux(),
			app:         app,
			routesByKey: map[string]struct{}{"BROKENKEY": {}},
		}
		req = httptest.NewRequest(http.MethodGet, "/missing", nil)
		ctx, rec = newAppContext(app, req)
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, ctx))
		ctx.req = req
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected custom 404 error handling for malformed route key case, got %d", rec.Code)
		}

		app.SetNotFoundHandler(func(c *Context) error { return errors.New("404 fail") })
		silentMux := http.NewServeMux()
		silentMux.Handle("/mounted/", http.StripPrefix("/mounted", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
		mux = &routingMux{
			mux:         silentMux,
			app:         app,
			routesByKey: map[string]struct{}{},
		}
		req = httptest.NewRequest(http.MethodGet, "/mounted/path", nil)
		ctx, rec = newAppContext(app, req)
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, ctx))
		ctx.req = req
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected fallback error handling on not found failure, got %d", rec.Code)
		}

		app.SetNotFoundHandler(func(c *Context) error { return c.Text(http.StatusNotFound, "custom not found") })
		req = httptest.NewRequest(http.MethodGet, "/still-missing", nil)
		ctx, rec = newAppContext(app, req)
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, ctx))
		ctx.req = req
		mux = &routingMux{
			mux:         http.NewServeMux(),
			app:         app,
			routesByKey: map[string]struct{}{},
		}
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "custom not found") {
			t.Fatalf("expected custom 404 fallback with context, got code=%d body=%q", rec.Code, rec.Body.String())
		}

		fastHandler := routeRuntimeHandler(func(c *Context, w http.ResponseWriter, r *http.Request) {
			_ = c.Text(http.StatusOK, "fast")
		})
		mux = &routingMux{
			mux:         http.NewServeMux(),
			app:         app,
			routesByKey: map[string]struct{}{},
			routeHandlers: map[string]routeRuntimeHandler{
				"GET /fast": fastHandler,
			},
			routeHandlerFast: map[string]map[string]routeRuntimeHandler{
				"GET": {"/fast": fastHandler},
			},
			directDynamicRoutes: map[string][]directDynamicRoute{
				http.MethodGet: {{
					handler: func(c *Context, w http.ResponseWriter, r *http.Request) {
						_ = c.JSON(http.StatusOK, map[string]string{"id": c.Param("id")})
					},
					pattern: compileDirectPattern("/users/{id}"),
				}},
			},
		}

		req = httptest.NewRequest(http.MethodGet, "/fast", nil)
		ctx, rec = newAppContext(app, req)
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, ctx))
		ctx.req = req
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "fast" {
			t.Fatalf("expected direct static route fast path, got code=%d body=%q", rec.Code, rec.Body.String())
		}

		req = httptest.NewRequest(http.MethodGet, "/users/42", nil)
		ctx, rec = newAppContext(app, req)
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, ctx))
		ctx.req = req
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"42"`) {
			t.Fatalf("expected direct dynamic route fast path, got code=%d body=%q", rec.Code, rec.Body.String())
		}

		genericMux := http.NewServeMux()
		genericMux.Handle("GET /items/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("generic-dynamic"))
		}))
		mux = &routingMux{
			mux:           genericMux,
			app:           app,
			routesByKey:   map[string]struct{}{"GET /items/{id}": {}},
			routeHandlers: map[string]routeRuntimeHandler{},
		}
		req = httptest.NewRequest(http.MethodGet, "/items/7", nil)
		ctx, rec = newAppContext(app, req)
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, ctx))
		ctx.req = req
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "generic-dynamic" {
			t.Fatalf("expected generic dynamic route path, got code=%d body=%q", rec.Code, rec.Body.String())
		}
	})
}

func TestAppNilGuardPaths(t *testing.T) {
	app := New(WithBanner(false), WithCodec(nil), WithLogger(nil))

	app.SetNotFoundHandler(nil)
	app.SetMethodNotAllowedHandler(nil)
	app.AddHook(OnRequest, nil)
	app.AddHookWithPriority(OnResponse, 10, nil)
	app.OnShutdown(nil)
	app.ReleaseContext(nil)

	app.Get("/json", func(c *Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "true"})
	})

	resp := NewTestClient(app).Get("/json")
	resp.AssertStatus(t, http.StatusOK)

	var body map[string]string
	if err := resp.JSON(&body); err != nil {
		t.Fatalf("expected default codec to remain usable, got %v", err)
	}
	if body["ok"] != "true" {
		t.Fatalf("unexpected response body: %#v", body)
	}
}

func TestEffectiveTLSConfigSecurityDefaults(t *testing.T) {
	t.Run("defaults are enforced and config is cloned", func(t *testing.T) {
		base := &tls.Config{}
		app := New(WithBanner(false), WithTLSConfig(base), WithDisableHTTP2(true))

		cfg := app.effectiveTLSConfig(false)
		if cfg == base {
			t.Fatal("expected tls config clone, not original pointer")
		}
		if cfg.MinVersion != tls.VersionTLS12 {
			t.Fatalf("expected TLS 1.2 minimum, got %v", cfg.MinVersion)
		}
		if len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != "http/1.1" {
			t.Fatalf("expected http/1.1 only when disabling http2, got %#v", cfg.NextProtos)
		}
		if base.MinVersion != 0 || len(base.NextProtos) != 0 {
			t.Fatal("expected original tls config to remain unmodified")
		}
	})

	t.Run("stronger config is preserved and mutual tls is enabled", func(t *testing.T) {
		base := &tls.Config{
			MinVersion: tls.VersionTLS13,
			NextProtos: []string{"h2", "http/1.1"},
		}
		app := New(WithBanner(false), WithTLSConfig(base))

		cfg := app.effectiveTLSConfig(true)
		if cfg.MinVersion != tls.VersionTLS13 {
			t.Fatalf("expected stronger min version to be preserved, got %v", cfg.MinVersion)
		}
		if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
			t.Fatalf("expected mutual tls client auth, got %v", cfg.ClientAuth)
		}
		if len(cfg.NextProtos) != 2 || cfg.NextProtos[0] != "h2" {
			t.Fatalf("expected next protos to remain intact, got %#v", cfg.NextProtos)
		}
	})
}

func TestConcurrentServeHTTP(t *testing.T) {
	app := New(WithBanner(false))
	var served atomic.Int64

	app.Get("/items/{id}", func(c *Context) error {
		served.Add(1)
		return c.JSON(http.StatusOK, map[string]string{
			"id":   c.Param("id"),
			"echo": c.QueryDefault("echo", "none"),
		})
	})

	const workers = 64
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			req := httptest.NewRequest(http.MethodGet, "/items/"+strconv.Itoa(i)+"?echo=value", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				errCh <- errors.New("unexpected status")
				return
			}

			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				errCh <- err
				return
			}
			if body["id"] != strconv.Itoa(i) || body["echo"] != "value" {
				errCh <- errors.New("unexpected body payload")
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if served.Load() != workers {
		t.Fatalf("expected %d requests served, got %d", workers, served.Load())
	}
}

func TestAdditionalCoreCoverage(t *testing.T) {
	t.Run("wildcard prefix mismatch", func(t *testing.T) {
		if matchesPattern("/files/{path...}", "/users/a/b") {
			t.Fatal("expected wildcard pattern prefix mismatch to fail")
		}
	})

	t.Run("listenAndShutdown signal path", func(t *testing.T) {
		app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
		server := &http.Server{Addr: "127.0.0.1:0"}
		done := make(chan struct{})

		go func() {
			time.Sleep(50 * time.Millisecond)
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
			time.Sleep(25 * time.Millisecond)
			close(done)
		}()

		err := app.listenAndShutdown(server, func() error {
			<-done
			return http.ErrServerClosed
		})
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("expected clean shutdown or server closed error, got %v", err)
		}
	})

	t.Run("assert status mismatch helper", func(t *testing.T) {
		reporter := &recordingStatusReporter{}
		(&TestResponse{Status: http.StatusOK, Body: []byte("body")}).assertStatus(reporter, http.StatusCreated)
		if reporter.calls != 1 {
			t.Fatalf("expected one status mismatch report, got %d", reporter.calls)
		}
	})
}

type recordingStatusReporter struct {
	calls int
}

func (r *recordingStatusReporter) Helper() {}

func (r *recordingStatusReporter) Errorf(string, ...any) {
	r.calls++
}

type errReadCloser struct {
	err error
}

func (e errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e errReadCloser) Close() error             { return nil }

type featureResponseWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	flushed  int
	hijacked bool
	pushes   []string
	writeErr error
}

func (w *featureResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *featureResponseWriter) WriteHeader(code int) {
	w.status = code
}

func (w *featureResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.body.Write(b)
}

func (w *featureResponseWriter) Flush() {
	w.flushed++
}

func (w *featureResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return nil, nil, nil
}

func (w *featureResponseWriter) Push(target string, _ *http.PushOptions) error {
	w.pushes = append(w.pushes, target)
	return nil
}

type parserValue string

func (p *parserValue) ParseParam(value string) error {
	if value == "bad" {
		return errors.New("parse failure")
	}
	*p = parserValue(strings.ToUpper(value))
	return nil
}

type customBinderPayload struct {
	Value string
}

func (c *customBinderPayload) BindFromContext(ctx *Context) error {
	c.Value = ctx.Query("value")
	return nil
}

type failingJSONValue struct{}

func (f failingJSONValue) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal failure")
}

type failingCodec struct{}

func (failingCodec) Decode(io.Reader, any) error      { return errors.New("decode failure") }
func (failingCodec) Encode(io.Writer, any) error      { return errors.New("encode failure") }
func (failingCodec) UnmarshalBytes([]byte, any) error { return errors.New("unmarshal failure") }
func (failingCodec) MarshalBytes(any) ([]byte, error) {
	return nil, errors.New("marshal bytes failure")
}
func (failingCodec) ContentType() string { return "application/fail" }

type basePlugin struct{}

func (basePlugin) Name() string    { return "base" }
func (basePlugin) Version() string { return "1.0.0" }
func (basePlugin) Register(*PluginContext) error {
	return nil
}

type dependentPlugin struct{}

func (dependentPlugin) Name() string    { return "dependent" }
func (dependentPlugin) Version() string { return "1.0.0" }
func (dependentPlugin) Register(*PluginContext) error {
	return nil
}

func (dependentPlugin) Dependencies() []string { return []string{"base"} }

type brokenPlugin struct{}

func (brokenPlugin) Name() string    { return "broken" }
func (brokenPlugin) Version() string { return "1.0.0" }
func (brokenPlugin) Register(*PluginContext) error {
	return errors.New("register failure")
}

type selfValidating struct{}

func (selfValidating) Validate() []ValidationError {
	return []ValidationError{{Field: "self", Tag: "custom"}}
}

type structLevelValidated struct {
	Value string `json:"value"`
}

func (s structLevelValidated) ValidateStruct() []ValidationError {
	if s.Value == "bad" {
		return []ValidationError{{Field: "value", Tag: "struct"}}
	}
	return nil
}

func newAppContext(app *App, req *http.Request) (*Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	ctx := app.AcquireContext(rec, req)
	return ctx, rec
}
