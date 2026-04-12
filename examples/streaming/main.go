// Example: Server-Sent Events (SSE) and streaming responses.
// Demonstrates how to use c.Stream() to bypass response buffering
// for real-time data streaming to clients.
package main

import (
	"context"
	"errors"
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

	// SSE endpoint using the framework helper — clean, idiomatic version.
	// Shows Context.SSE() with typed events, Done() for disconnect handling,
	// and deferred Close().
	app.Get("/events-helper", func(c *aarv.Context) error {
		sse, err := c.SSE()
		if err != nil {
			return err
		}
		defer func() { _ = sse.Close() }()

		for i := 1; i <= 10; i++ {
			select {
			case <-sse.Done():
				return nil // client disconnected
			case <-time.After(1 * time.Second):
			}

			if err := sse.Send(aarv.SSEEvent{
				Event: "tick",
				ID:    fmt.Sprintf("%d", i),
				Data:  fmt.Sprintf(`{"count": %d, "time": %q}`, i, time.Now().Format(time.RFC3339)),
			}); err != nil {
				// Suppress disconnect-style errors, propagate real write errors.
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil
				}
				return err
			}
		}

		// Send final event — same error policy
		if err := sse.Send(aarv.SSEEvent{Event: "done", Data: `{"done": true}`}); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return err
		}
		return nil
	})

	// SSE endpoint using the low-level approach — shows manual SSE
	// formatting, explicit http.Flusher flushing, and direct use of
	// c.Response() for real-time output. Compare to /events-helper which
	// does all this automatically.
	//
	// Note: Connection: keep-alive is intentionally NOT set — it is a
	// hop-by-hop header, forbidden on HTTP/2, and Go's net/http server
	// manages connection persistence automatically.
	app.Get("/events", func(c *aarv.Context) error {
		c.SetHeader("Content-Type", "text/event-stream")
		c.SetHeader("Cache-Control", "no-cache")
		c.SetHeader("Access-Control-Allow-Origin", "*")

		// Bypass the framework's buffered response writer so writes go
		// directly to the underlying ResponseWriter.
		w := c.Response()
		if bw, ok := w.(interface{ Bypass() }); ok {
			bw.Bypass()
		}
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return fmt.Errorf("response writer does not support flushing")
		}

		ctx := c.Request().Context()
		for i := 1; i <= 10; i++ {
			select {
			case <-ctx.Done():
				return nil // client disconnected
			case <-time.After(1 * time.Second):
			}

			event := fmt.Sprintf("data: {\"count\": %d, \"time\": \"%s\"}\n\n",
				i, time.Now().Format(time.RFC3339))
			if _, err := w.Write([]byte(event)); err != nil {
				return nil
			}
			flusher.Flush() // critical: force bytes to the client now
		}

		// Final event
		_, _ = w.Write([]byte("data: {\"done\": true}\n\n"))
		flusher.Flush()
		return nil
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
	fmt.Println("  GET /events-helper — SSE stream via c.SSE() helper (recommended)")
	fmt.Println("  GET /events        — SSE stream via raw c.Stream() (low-level)")
	fmt.Println("  GET /download      — Chunked file download")
	fmt.Println("  GET /progress      — Progress updates via SSE")
	fmt.Println("  GET /proxy         — Proxy/forward a stream")
	fmt.Println("  GET /buffered      — Normal buffered response")
	fmt.Println()
	fmt.Println("  Try: curl -N http://localhost:8080/events-helper")
	fmt.Println("  Try: curl -N http://localhost:8080/events")
	fmt.Println("  Try: curl http://localhost:8080/download")

	_ = app.Listen(":8080")
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
