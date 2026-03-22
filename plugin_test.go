package aarv

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPluginSystem(t *testing.T) {
	app := New()

	testPlugin := &dummyPlugin{}
	app.Register(testPlugin)

	if !testPlugin.registered {
		t.Errorf("Plugin Register was not called")
	}
}

type dummyPlugin struct {
	registered bool
}

func (p *dummyPlugin) Name() string    { return "dummy" }
func (p *dummyPlugin) Version() string { return "1.0.0" }
func (p *dummyPlugin) Register(pc *PluginContext) error {
	p.registered = true

	// Test PluginContext wrapper
	pc.App()
	pc.Logger()
	pc.Use(func(next http.Handler) http.Handler { return next })
	pc.Get("/dummy", func(c *Context) error { return c.NoContent(200) })
	pc.Post("/dummy", func(c *Context) error { return c.NoContent(200) })
	pc.Put("/dummy", func(c *Context) error { return c.NoContent(200) })
	pc.Delete("/dummy", func(c *Context) error { return c.NoContent(200) })

	// Ensure group creation works through wrapper
	pc.Group("/g", func(g *RouteGroup) {
		g.Get("/child", func(c *Context) error { return c.NoContent(200) })
	})

	pc.AddHook(OnRequest, func(c *Context) error { return nil })

	pc.Decorate("dummyIface", nil)

	// Resolve
	val, ok := pc.Resolve("dummyIface")
	if !ok || val != nil {
		panic("unexpected decorated value")
	}

	return nil
}

func TestPluginAndTestClientAdditionalCoverage(t *testing.T) {
	t.Run("plugin helpers", func(t *testing.T) {
		app := New(WithBanner(false))
		pc := newPluginContext(app, "plugin", "/prefix")
		if got := pc.routeOpts(nil); len(got) != 0 {
			t.Fatalf("expected no route opts, got %d", len(got))
		}
		pc.Use(func(next http.Handler) http.Handler { return next })
		if got := pc.routeOpts(nil); len(got) != 0 {
			t.Fatalf("expected route opts to stay unchanged, got %d", len(got))
		}
		pc.Decorate("svc", 42)
		if v, ok := pc.Resolve("svc"); !ok || v.(int) != 42 {
			t.Fatalf("unexpected decorated value: %v %v", v, ok)
		}
		if err := pc.Register(PluginFunc(func(*PluginContext) error { return nil })); err != nil {
			t.Fatalf("unexpected nested plugin error: %v", err)
		}

		fn := PluginFunc(func(*PluginContext) error { return nil })
		if fn.Name() != "anonymous" || fn.Version() != "0.0.0" {
			t.Fatal("unexpected plugin func metadata")
		}
		if err := fn.Register(pc); err != nil {
			t.Fatalf("unexpected plugin func register error: %v", err)
		}

		app.Register(basePlugin{})
		app.Register(dependentPlugin{})
		if !app.hasPlugin("base") || !app.hasPlugin("dependent") || app.hasPlugin("missing") {
			t.Fatal("unexpected plugin registry state")
		}

		prefixed := PluginFunc(func(pc *PluginContext) error {
			pc.Get("/ready", func(c *Context) error { return c.NoContent(http.StatusOK) })
			return nil
		})
		app.Register(prefixed, WithPrefix("/plugin"))
		NewTestClient(app).Get("/plugin/ready").AssertStatus(t, http.StatusOK)

		defer func() {
			if recover() == nil {
				t.Fatal("expected dependency panic")
			}
		}()
		New(WithBanner(false)).Register(dependentPlugin{})
	})

	t.Run("plugin registration failure", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected plugin registration panic")
			}
		}()
		New(WithBanner(false)).Register(brokenPlugin{})
	})

	t.Run("test client helpers", func(t *testing.T) {
		app := New(WithBanner(false))
		app.Put("/users", func(c *Context) error { return c.Text(http.StatusOK, c.Header("Authorization")) })
		app.Patch("/users", func(c *Context) error { return c.Text(http.StatusAccepted, c.Header("Authorization")) })

		tc := NewTestClient(app).WithBearer("token")
		if got := tc.Put("/users", map[string]string{"name": "n"}).Text(); got != "Bearer token" {
			t.Fatalf("unexpected PUT body: %q", got)
		}
		if got := tc.Patch("/users", map[string]string{"name": "n"}); got.Status != http.StatusAccepted {
			t.Fatalf("unexpected PATCH status: %d", got.Status)
		}

		req := httptest.NewRequest(http.MethodPut, "/users", nil)
		req.Header.Set("Authorization", "Bearer direct")
		if resp := tc.Do(req); resp.Status != http.StatusOK {
			t.Fatalf("unexpected direct request status: %d", resp.Status)
		}
	})
}
