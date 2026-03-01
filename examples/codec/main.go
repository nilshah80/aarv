// Example: Custom codec — demonstrates the pluggable Codec interface.
// Shows how to swap the default encoding/json codec for a custom one,
// and how to write your own Codec from scratch.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
)

// =============================================================================
// PrettyJSONCodec — a custom Codec that always pretty-prints JSON responses
// with indentation. Useful for developer-facing APIs.
// =============================================================================

type PrettyJSONCodec struct {
	Prefix string
	Indent string
}

func (c *PrettyJSONCodec) Decode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

func (c *PrettyJSONCodec) Encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent(c.Prefix, c.Indent)
	return enc.Encode(v)
}

func (c *PrettyJSONCodec) UnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (c *PrettyJSONCodec) MarshalBytes(v any) ([]byte, error) {
	return json.MarshalIndent(v, c.Prefix, c.Indent)
}

func (c *PrettyJSONCodec) ContentType() string {
	return "application/json; charset=utf-8"
}

// =============================================================================
// Request/Response types for the Bind demo
// =============================================================================

type EchoReq struct {
	Message string `json:"message" validate:"required,min=1"`
	Count   int    `json:"count"   validate:"gte=1,lte=100" default:"1"`
}

type EchoRes struct {
	Messages  []string `json:"messages"`
	Timestamp string   `json:"timestamp"`
}

// =============================================================================
// main
// =============================================================================

func main() {
	// Swap in the PrettyJSONCodec via WithCodec option.
	// All JSON serialization (c.JSON, c.BindJSON, Bind[Req,Res])
	// automatically uses this codec.
	app := aarv.New(
		aarv.WithCodec(&PrettyJSONCodec{
			Prefix: "",
			Indent: "  ",
		}),
		aarv.WithBanner(true),
	)

	app.Use(aarv.Recovery(), aarv.Logger())

	// Simple JSON response — uses the pretty codec
	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"message": "Codec example — all JSON is pretty-printed",
			"codec":   "PrettyJSONCodec",
			"note":    "Swap WithCodec(...) to use segmentio, sonic, or jsonv2",
		})
	})

	// Typed handler — codec is used for both request decoding and response encoding
	app.Post("/echo", aarv.Bind(func(c *aarv.Context, req EchoReq) (EchoRes, error) {
		messages := make([]string, req.Count)
		for i := range messages {
			messages[i] = fmt.Sprintf("[%d] %s", i+1, req.Message)
		}
		return EchoRes{
			Messages:  messages,
			Timestamp: time.Now().Format(time.RFC3339),
		}, nil
	}))

	// Demonstrates that the codec handles nested structures, slices, etc.
	app.Get("/complex", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"users": []map[string]any{
				{"id": 1, "name": "Alice", "tags": []string{"admin", "go"}},
				{"id": 2, "name": "Bob", "tags": []string{"user"}},
			},
			"pagination": map[string]int{
				"page":       1,
				"page_size":  20,
				"total":      2,
			},
		})
	})

	// Show which codec is active (demonstrates that the Codec interface
	// can be introspected via ContentType)
	app.Get("/codec-info", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"content_type": "application/json; charset=utf-8",
			"description":  "PrettyJSONCodec with 2-space indent",
			"alternatives": "segmentio, sonic, jsonv2 (see codec/ packages)",
		})
	})

	fmt.Println("Codec Demo on :8080")
	fmt.Println("  GET  /           — pretty-printed JSON response")
	fmt.Println("  POST /echo       — typed handler (decode + encode via codec)")
	fmt.Println("  GET  /complex    — nested/slice structures")
	fmt.Println("  GET  /codec-info — active codec metadata")
	fmt.Println()
	fmt.Println("  Try: curl -s http://localhost:8080/ | head")
	fmt.Println("  Try: curl -s -X POST http://localhost:8080/echo \\")
	fmt.Println("         -H 'Content-Type: application/json' \\")
	fmt.Println("         -d '{\"message\":\"hello\",\"count\":3}'")
	fmt.Println()
	fmt.Println("  To use a different codec, change WithCodec(...):")
	fmt.Println("    segmentio: aarv.WithCodec(segmentio.New())")
	fmt.Println("    sonic:     aarv.WithCodec(sonic.New())")
	fmt.Println("    jsonv2:    aarv.WithCodec(jsonv2.New())")

	app.Listen(":8080")
}
