package aarv

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
		app.server = &http.Server{}
		var hookCalled, legacyCalled bool
		app.AddHook(OnShutdown, func(*Context) error {
			hookCalled = true
			return nil
		})
		app.OnShutdown(func(interface{ Done() <-chan struct{} }) error {
			legacyCalled = true
			return errors.New("legacy failed")
		})

		err := app.listenAndShutdown(func() error { return http.ErrServerClosed })
		if err != nil && err.Error() != "http: Server closed" {
			t.Fatalf("expected nil or closed error, got %v", err)
		}
		if !hookCalled || !legacyCalled {
			t.Fatal("expected shutdown hooks to run")
		}

		if err := app.listenAndShutdown(func() error { return errors.New("serve failed") }); err == nil || !strings.Contains(err.Error(), "server error") {
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
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected standard 404 fallback for malformed route key case, got %d", rec.Code)
		}
	})
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
func (failingCodec) MarshalBytes(any) ([]byte, error) { return nil, errors.New("marshal bytes failure") }
func (failingCodec) ContentType() string              { return "application/fail" }

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
