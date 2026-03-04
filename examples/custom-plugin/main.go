// Example: Writing a custom plugin — demonstrates the Plugin interface,
// PluginContext methods, lifecycle hooks, decorated services, and
// scoped route registration.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
)

// =============================================================================
// RateLimiter Plugin — demonstrates a full custom plugin
// =============================================================================

// RateLimiterPlugin implements aarv.Plugin.
type RateLimiterPlugin struct {
	maxRequests int
	window      time.Duration
}

func NewRateLimiter(maxRequests int, window time.Duration) *RateLimiterPlugin {
	return &RateLimiterPlugin{
		maxRequests: maxRequests,
		window:      window,
	}
}

func (p *RateLimiterPlugin) Name() string { return "rate-limiter" }

// Version makes this a PluginWithVersion.
func (p *RateLimiterPlugin) Version() string { return "1.0.0" }

func (p *RateLimiterPlugin) Register(pc *aarv.PluginContext) error {
	logger := pc.Logger()
	logger.Info("registering rate limiter", "max", p.maxRequests, "window", p.window)

	// Shared state for rate limiting
	type clientEntry struct {
		count    int
		windowAt time.Time
	}
	var (
		clients = make(map[string]*clientEntry)
		mu      sync.Mutex
	)

	// Decorate the rate limiter so other plugins can inspect it
	pc.Decorate("rateLimiter.maxRequests", p.maxRequests)

	// Register a middleware that enforces rate limits
	pc.Use(aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			ip := c.Request().RemoteAddr

			mu.Lock()
			entry, ok := clients[ip]
			now := time.Now()
			if !ok || now.After(entry.windowAt) {
				entry = &clientEntry{count: 0, windowAt: now.Add(p.window)}
				clients[ip] = entry
			}
			entry.count++
			count := entry.count
			mu.Unlock()

			if count > p.maxRequests {
				logger.Warn("rate limit exceeded", "ip", ip, "count", count)
				return aarv.ErrTooManyRequests("rate limit exceeded")
			}

			c.SetHeader("X-RateLimit-Limit", fmt.Sprintf("%d", p.maxRequests))
			c.SetHeader("X-RateLimit-Remaining", fmt.Sprintf("%d", p.maxRequests-count))

			return next(c)
		}
	}))

	// Register plugin-scoped routes
	pc.Get("/rate-limit/status", func(c *aarv.Context) error {
		mu.Lock()
		active := len(clients)
		mu.Unlock()

		return c.JSON(http.StatusOK, map[string]any{
			"max_requests":   p.maxRequests,
			"window":         p.window.String(),
			"active_clients": active,
		})
	})

	// Lifecycle hook: log stats on shutdown
	pc.AddHook(aarv.OnShutdown, func(c *aarv.Context) error {
		mu.Lock()
		defer mu.Unlock()
		logger.Info("rate limiter shutting down", "tracked_clients", len(clients))
		return nil
	})

	return nil
}

// =============================================================================
// Metrics Plugin — demonstrates plugin dependencies and decorated services
// =============================================================================

// MetricsPlugin depends on the rate-limiter plugin.
type MetricsPlugin struct{}

func (p *MetricsPlugin) Name() string            { return "metrics" }
func (p *MetricsPlugin) Version() string         { return "1.0.0" }
func (p *MetricsPlugin) Dependencies() []string  { return []string{"rate-limiter"} }

func (p *MetricsPlugin) Register(pc *aarv.PluginContext) error {
	// Resolve a value decorated by the rate-limiter plugin
	maxReqs, ok := pc.Resolve("rateLimiter.maxRequests")
	if !ok {
		return fmt.Errorf("rate limiter not configured")
	}

	pc.Logger().Info("metrics plugin loaded", "rate_limit", maxReqs)

	pc.Get("/metrics", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"rate_limit_max": maxReqs,
			"uptime":         "demo",
		})
	})

	return nil
}

// =============================================================================
// PluginFunc — demonstrates the lightweight PluginFunc adapter
// =============================================================================

func debugRoutes(pc *aarv.PluginContext) error {
	pc.Get("/debug/routes", func(c *aarv.Context) error {
		routes := pc.App().Routes()
		out := make([]map[string]string, len(routes))
		for i, r := range routes {
			out[i] = map[string]string{
				"method":  r.Method,
				"pattern": r.Pattern,
			}
		}
		return c.JSON(http.StatusOK, map[string]any{"routes": out})
	})
	return nil
}

// =============================================================================
// main
// =============================================================================

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
		aarv.WithLogger(slog.Default()),
	)

	app.Use(aarv.Recovery(), aarv.Logger())

	// Register custom plugins
	app.Register(
		NewRateLimiter(10, 1*time.Minute),
		aarv.WithPrefix("/api"),
	)

	// Plugin with dependency on rate-limiter
	app.Register(&MetricsPlugin{}, aarv.WithPrefix("/api"))

	// Lightweight PluginFunc
	app.Register(aarv.PluginFunc(debugRoutes))

	// App routes
	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"message": "Custom plugin example",
		})
	})

	fmt.Println("Custom Plugin Demo on :8080")
	fmt.Println("  GET /                     — hello")
	fmt.Println("  GET /api/rate-limit/status — rate limiter stats")
	fmt.Println("  GET /api/metrics           — metrics (depends on rate-limiter)")
	fmt.Println("  GET /debug/routes          — list all registered routes")
	fmt.Println()
	fmt.Println("  Rate limit: 10 req/min per IP")

	app.Listen(":8080")
}
