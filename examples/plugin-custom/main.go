package main

import (
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/nilshah80/aarv"
)

// CounterPlugin is a tiny aarv.Plugin demonstrating decoration, scoped
// middleware, scoped routes, and a shutdown hook.
type CounterPlugin struct {
	requests atomic.Int64
}

// Name implements aarv.Plugin.
func (p *CounterPlugin) Name() string { return "counter" }

// Version implements aarv.Plugin.
func (p *CounterPlugin) Version() string { return "1.0.0" }

// Register wires the plugin's middleware, route, and shutdown hook into
// the supplied scope.
func (p *CounterPlugin) Register(pc *aarv.PluginContext) error {
	pc.Decorate("counter", p)

	pc.Use(aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			p.requests.Add(1)
			return next(c)
		}
	}))

	pc.Get("/stats", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]int64{
			"requests": p.requests.Load(),
		})
	})

	pc.AddHook(aarv.OnShutdown, func(c *aarv.Context) error {
		fmt.Println("counter plugin shutdown, requests:", p.requests.Load())
		return nil
	})

	return nil
}

func main() {
	app := aarv.New(aarv.WithBanner(true))
	app.Use(aarv.Recovery())

	app.Register(&CounterPlugin{}, aarv.WithPrefix("/internal"))

	app.Register(aarv.PluginFunc(func(pc *aarv.PluginContext) error {
		pc.Get("/debug/routes", func(c *aarv.Context) error {
			return c.JSON(http.StatusOK, pc.App().Routes())
		})
		return nil
	}))

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"message": "custom plugin example"})
	})

	fmt.Println("Custom plugin example on :8080")
	fmt.Println("  GET /")
	fmt.Println("  GET /internal/stats")
	fmt.Println("  GET /debug/routes")

	_ = app.Listen(":8080")
}
