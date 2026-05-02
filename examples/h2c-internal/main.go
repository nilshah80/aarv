// Example: serve aarv as an HTTP/2-cleartext (h2c) listener for an
// internal mesh / sidecar where TLS is terminated upstream.
//
// h2c is INTERNAL ONLY. Never expose this listener to the public
// internet — it has no confidentiality, no integrity, and no
// authentication. Run only behind a trusted TLS terminator.
//
// Run the server:
//
//	go run . -addr 127.0.0.1:8080
//
// Talk to it from a Go client (see client_snippet.go) or curl --http2-prior-knowledge:
//
//	curl --http2-prior-knowledge http://127.0.0.1:8080/
package main

import (
	"flag"
	"log"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/h2c"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "h2c bind address (internal only)")
	flag.Parse()

	app := aarv.New(aarv.WithBanner(true))
	app.Use(aarv.Recovery(), aarv.Logger())

	app.Get("/", func(c *aarv.Context) error {
		return c.JSON(200, map[string]any{
			"hello": "from aarv over h2c",
			"proto": c.Request().Proto,
			"time":  time.Now().UTC(),
		})
	})

	cfg := h2c.Config{
		MaxConcurrentStreams: 100,
		ReadTimeout:          15 * time.Second,
		ReadHeaderTimeout:    5 * time.Second,
		// WriteTimeout intentionally left zero — long-lived
		// gRPC-style streams would otherwise be terminated by the
		// timer.
	}
	log.Printf("h2c listener on %s (cleartext — internal mesh only)", *addr)
	if err := h2c.Listen(app, *addr, cfg); err != nil {
		log.Fatal(err)
	}
}
