// Example: Lifecycle hooks — demonstrates all hook phases, priorities,
// shutdown hooks, and OnError handling.
package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/requestid"
)

var requestCount atomic.Int64

type lifecycleReq struct {
	Name string `json:"name" validate:"required,min=2"`
}

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
	)

	app.Use(
		aarv.Recovery(),
		requestid.New(),
		aarv.Logger(),
	)

	// --- OnStartup hook: runs once before server starts accepting traffic ---
	app.AddHook(aarv.OnStartup, func(c *aarv.Context) error {
		fmt.Println("[hook] OnStartup: server is starting up")
		return nil
	})

	// --- OnRequest hook: runs at the start of every request ---
	app.AddHook(aarv.OnRequest, func(c *aarv.Context) error {
		n := requestCount.Add(1)
		c.Set("request_number", n)
		c.Logger().Info("OnRequest hook",
			"request_number", n,
			"method", c.Request().Method,
			"path", c.Request().URL.Path,
		)
		return nil
	})

	// --- OnRequest with priority: lower priority runs first ---
	// This hook runs before the one above (priority -1 < 0)
	app.AddHookWithPriority(aarv.OnRequest, -1, func(c *aarv.Context) error {
		c.Set("hook_start", time.Now())
		return nil
	})

	// --- OnError hook: runs when a handler returns an error ---
	app.AddHook(aarv.OnError, func(c *aarv.Context) error {
		c.Logger().Warn("OnError hook triggered",
			"path", c.Request().URL.Path,
			"request_id", c.RequestID(),
			"error", c.HookError(),
		)
		return nil
	})

	// --- PreRouting hook: runs after OnRequest and before route dispatch ---
	app.AddHook(aarv.PreRouting, func(c *aarv.Context) error {
		c.Set("phase_prerouting", true)
		return nil
	})

	// --- PreParsing hook: runs before body decoding for bind handlers ---
	app.AddHook(aarv.PreParsing, func(c *aarv.Context) error {
		c.Set("phase_preparsing", true)
		return nil
	})

	// --- PreValidation hook: runs after binding/defaults, before validation ---
	app.AddHook(aarv.PreValidation, func(c *aarv.Context) error {
		c.Set("phase_prevalidation", true)
		return nil
	})

	// --- PreHandler hook: runs immediately before the user handler ---
	app.AddHook(aarv.PreHandler, func(c *aarv.Context) error {
		c.Set("phase_prehandler", true)
		return nil
	})

	// --- OnShutdown hook: runs when server receives SIGINT/SIGTERM ---
	app.AddHook(aarv.OnShutdown, func(c *aarv.Context) error {
		fmt.Println("[hook] OnShutdown: cleaning up resources")
		return nil
	})

	// --- Shutdown hook with context deadline ---
	app.OnShutdown(func(ctx interface{ Done() <-chan struct{} }) error {
		fmt.Println("[shutdown] Graceful shutdown started")
		select {
		case <-time.After(100 * time.Millisecond):
			fmt.Println("[shutdown] Cleanup completed")
		case <-ctx.Done():
			fmt.Println("[shutdown] Deadline exceeded, aborting cleanup")
		}
		return nil
	})

	// --- Routes ---

	app.Get("/", func(c *aarv.Context) error {
		startVal, _ := c.Get("hook_start")
		start, _ := startVal.(time.Time)
		reqNumVal, _ := c.Get("request_number")
		reqNum, _ := reqNumVal.(int64)

		return c.JSON(http.StatusOK, map[string]any{
			"message":        "Hooks example",
			"request_number": reqNum,
			"hook_overhead":  time.Since(start).String(),
			"request_id":     c.RequestID(),
		})
	})

	app.Get("/stats", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"total_requests": requestCount.Load(),
		})
	})

	app.Post("/lifecycle", aarv.Bind(func(c *aarv.Context, req lifecycleReq) (map[string]any, error) {
		return map[string]any{
			"name":             req.Name,
			"pre_routing":      c.MustGet("phase_prerouting"),
			"pre_parsing":      c.MustGet("phase_preparsing"),
			"pre_validation":   c.MustGet("phase_prevalidation"),
			"pre_handler":      c.MustGet("phase_prehandler"),
			"request_number":   c.MustGet("request_number"),
			"request_id":       c.RequestID(),
			"handler_executed": true,
		}, nil
	}))

	app.Get("/error", func(c *aarv.Context) error {
		return aarv.ErrBadRequest("intentional error to trigger OnError hook")
	})

	app.Get("/panic", func(c *aarv.Context) error {
		panic("intentional panic — recovery middleware + OnError hook")
	})

	fmt.Println("Hooks Demo on :8080")
	fmt.Println("  GET /       — shows hook data (request number, overhead)")
	fmt.Println("  GET /stats  — total request count (tracked via OnRequest)")
	fmt.Println("  POST /lifecycle — exercises PreRouting, PreParsing, PreValidation, PreHandler")
	fmt.Println("  GET /error  — triggers OnError hook")
	fmt.Println("  GET /panic  — triggers recovery + OnError hook")
	fmt.Println()
	fmt.Println("  Try Ctrl+C to see OnShutdown hook in action")

	app.Listen(":8080")
}
