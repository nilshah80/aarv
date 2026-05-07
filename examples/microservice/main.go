package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/health"
	"github.com/nilshah80/aarv/plugins/logger"
	prom "github.com/nilshah80/aarv/plugins/prometheus"
	"github.com/nilshah80/aarv/plugins/requestid"
	"github.com/nilshah80/aarv/plugins/secure"
)

var started = time.Now()

func main() {
	app := aarv.New(aarv.WithBanner(true))

	metrics := prom.Config{
		Namespace: "aarv_example",
		SkipPaths: []string{"/metrics", "/health", "/ready", "/live"},
	}

	app.Use(
		aarv.Recovery(),
		requestid.New(),
		logger.New(logger.Config{SkipPaths: []string{"/health", "/ready", "/live", "/metrics"}}),
		secure.New(),
		health.New(health.Config{
			Info: map[string]any{
				"service": "orders",
				"version": "dev",
			},
			ReadyCheck: func() bool { return time.Since(started) > time.Second },
		}),
		prom.New(metrics),
	)

	scrape := prom.Handler(metrics)
	app.Get("/metrics", func(c *aarv.Context) error {
		scrape.ServeHTTP(c.Response(), c.Request())
		return nil
	})

	app.Get("/orders/{id}", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"id":         c.Param("id"),
			"status":     "created",
			"request_id": c.RequestID(),
		})
	})

	fmt.Println("Microservice example on :8080")
	fmt.Println("  GET /health /ready /live")
	fmt.Println("  GET /metrics")
	fmt.Println("  GET /orders/ord_123")

	_ = app.Listen(":8080")
}
