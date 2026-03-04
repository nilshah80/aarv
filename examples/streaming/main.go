// Example: Server-Sent Events (SSE) and streaming responses.
// Demonstrates how to use c.Stream() to bypass response buffering
// for real-time data streaming to clients.
package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nilshah80/aarv"
)

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
	)

	app.Use(aarv.Recovery(), aarv.Logger())

	// SSE endpoint - sends events every second
	app.Get("/events", func(c *aarv.Context) error {
		// Set SSE headers
		c.SetHeader("Content-Type", "text/event-stream")
		c.SetHeader("Cache-Control", "no-cache")
		c.SetHeader("Connection", "keep-alive")
		c.SetHeader("Access-Control-Allow-Origin", "*")

		// Create a reader that generates SSE events
		reader := &sseReader{
			events: make(chan string, 10),
			done:   make(chan struct{}),
		}

		// Start event generator in background
		go func() {
			defer close(reader.events)
			for i := 1; i <= 10; i++ {
				select {
				case <-reader.done:
					return
				case <-time.After(1 * time.Second):
					event := fmt.Sprintf("data: {\"count\": %d, \"time\": \"%s\"}\n\n",
						i, time.Now().Format(time.RFC3339))
					reader.events <- event
				}
			}
			// Send final event
			reader.events <- "data: {\"done\": true}\n\n"
		}()

		// Stream bypasses buffered writer for real-time output
		return c.Stream(http.StatusOK, "text/event-stream", reader)
	})

	// Chunked transfer example - streams a large response in chunks
	app.Get("/download", func(c *aarv.Context) error {
		c.SetHeader("Content-Disposition", "attachment; filename=\"data.txt\"")

		// Create a reader that generates chunks
		reader := &chunkReader{
			chunks: 10,
			delay:  200 * time.Millisecond,
		}

		return c.Stream(http.StatusOK, "text/plain", reader)
	})

	// Progress streaming - simulates a long-running operation with progress updates
	app.Get("/progress", func(c *aarv.Context) error {
		c.SetHeader("Content-Type", "text/event-stream")
		c.SetHeader("Cache-Control", "no-cache")

		reader := &progressReader{
			total: 100,
			step:  10,
		}

		return c.Stream(http.StatusOK, "text/event-stream", reader)
	})

	// Proxy streaming - demonstrates forwarding a stream
	app.Get("/proxy", func(c *aarv.Context) error {
		// In a real app, this would fetch from another service
		// For demo, we create a simulated upstream response
		upstream := strings.NewReader(`{"message": "proxied response", "timestamp": "` +
			time.Now().Format(time.RFC3339) + `"}`)

		return c.Stream(http.StatusOK, "application/json", upstream)
	})

	// Regular buffered response for comparison
	app.Get("/buffered", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"message": "This response is buffered (default behavior)",
			"note":    "OnSend hooks can inspect/modify this before sending",
		})
	})

	fmt.Println("Streaming Demo on :8080")
	fmt.Println("  GET /events    — SSE stream (10 events, 1/sec)")
	fmt.Println("  GET /download  — Chunked file download")
	fmt.Println("  GET /progress  — Progress updates via SSE")
	fmt.Println("  GET /proxy     — Proxy/forward a stream")
	fmt.Println("  GET /buffered  — Normal buffered response")
	fmt.Println()
	fmt.Println("  Try: curl -N http://localhost:8080/events")
	fmt.Println("  Try: curl http://localhost:8080/download")

	app.Listen(":8080")
}

// sseReader implements io.Reader for SSE events
type sseReader struct {
	events chan string
	done   chan struct{}
	buf    []byte
}

func (r *sseReader) Read(p []byte) (n int, err error) {
	// If we have buffered data, return it first
	if len(r.buf) > 0 {
		n = copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}

	// Wait for next event
	event, ok := <-r.events
	if !ok {
		return 0, io.EOF
	}

	// Copy event to output buffer
	n = copy(p, event)
	if n < len(event) {
		r.buf = []byte(event[n:])
	}
	return n, nil
}

// chunkReader generates chunked data
type chunkReader struct {
	chunks int
	delay  time.Duration
	sent   int
}

func (r *chunkReader) Read(p []byte) (n int, err error) {
	if r.sent >= r.chunks {
		return 0, io.EOF
	}

	time.Sleep(r.delay)
	r.sent++

	chunk := fmt.Sprintf("Chunk %d of %d: %s\n",
		r.sent, r.chunks, time.Now().Format(time.RFC3339))
	n = copy(p, chunk)
	return n, nil
}

// progressReader sends progress updates
type progressReader struct {
	total   int
	step    int
	current int
	buf     []byte
}

func (r *progressReader) Read(p []byte) (n int, err error) {
	if len(r.buf) > 0 {
		n = copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}

	if r.current >= r.total {
		return 0, io.EOF
	}

	time.Sleep(100 * time.Millisecond)
	r.current += r.step

	var event string
	if r.current >= r.total {
		event = "data: {\"progress\": 100, \"status\": \"complete\"}\n\n"
	} else {
		event = fmt.Sprintf("data: {\"progress\": %d, \"status\": \"processing\"}\n\n", r.current)
	}

	n = copy(p, event)
	if n < len(event) {
		r.buf = []byte(event[n:])
	}
	return n, nil
}
