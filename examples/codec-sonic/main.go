// Example: Sonic codec — ByteDance's high-performance JSON library.
//
// Sonic uses JIT compilation and SIMD acceleration for ~5-10x faster
// JSON encoding/decoding compared to encoding/json on amd64/arm64 platforms.
//
// Features:
// - JIT-compiled encoder/decoder
// - SIMD acceleration for parsing
// - Pretouch API for startup optimization
// - ConfigDefault (validated) and ConfigFastest modes
//
// Run: go run main.go
// Test: curl http://localhost:8080/
package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/codec/sonic"
)

type Event struct {
	ID        string    `json:"id"`
	Type      string    `json:"type" validate:"required,oneof=click view purchase"`
	UserID    int       `json:"user_id" validate:"required"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data,omitempty"`
}

type TrackEventReq struct {
	Type   string `json:"type" validate:"required,oneof=click view purchase"`
	UserID int    `json:"user_id" validate:"required"`
	Data   any    `json:"data,omitempty"`
}

func main() {
	// Create the Sonic codec
	// Options:
	//   sonic.New()        - Standard config with validation (recommended)
	//   sonic.NewFastest() - Fastest config, skips some validation
	codec := sonic.New()

	// Pre-touch types at startup to avoid JIT overhead on first request
	// This is optional but recommended for latency-sensitive applications
	if err := codec.Pretouch(Event{}, TrackEventReq{}); err != nil {
		fmt.Printf("Warning: pretouch failed: %v\n", err)
	}

	app := aarv.New(
		aarv.WithCodec(codec),
		aarv.WithBanner(true),
	)

	app.Use(aarv.Recovery(), aarv.Logger())

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"codec":       "bytedance/sonic",
			"description": "JIT-compiled JSON encoder/decoder",
			"performance": "~5-10x faster than stdlib on amd64/arm64",
			"features": []string{
				"JIT compilation",
				"SIMD acceleration",
				"Pretouch for startup optimization",
				"ConfigDefault (validated) and ConfigFastest modes",
			},
		})
	})

	app.Post("/events", aarv.Bind(func(c *aarv.Context, req TrackEventReq) (Event, error) {
		return Event{
			ID:        fmt.Sprintf("evt_%d", time.Now().UnixNano()),
			Type:      req.Type,
			UserID:    req.UserID,
			Timestamp: time.Now(),
			Data:      req.Data,
		}, nil
	}))

	// High-volume endpoint to demonstrate Sonic's performance
	app.Get("/events/batch", func(c *aarv.Context) error {
		events := make([]Event, 1000)
		for i := range events {
			events[i] = Event{
				ID:        fmt.Sprintf("evt_%d", i),
				Type:      "view",
				UserID:    i % 100,
				Timestamp: time.Now(),
				Data:      map[string]any{"page": fmt.Sprintf("/page/%d", i)},
			}
		}
		return c.JSON(http.StatusOK, map[string]any{
			"events": events,
			"count":  len(events),
		})
	})

	fmt.Println("Sonic Codec Example on :8080")
	fmt.Println("  GET  /             — codec info")
	fmt.Println("  POST /events       — track event (typed handler)")
	fmt.Println("  GET  /events/batch — 1000 events (performance test)")
	fmt.Println()
	fmt.Println("  Sonic uses JIT compilation for extreme JSON performance.")
	fmt.Println("  Best for high-throughput APIs on amd64/arm64 platforms.")
	fmt.Println()
	fmt.Println("  Note: sonic.Pretouch() is called at startup to pre-compile")
	fmt.Println("  the encoder/decoder, avoiding JIT overhead on first request.")

	app.Listen(":8080")
}
