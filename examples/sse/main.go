package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
)

func main() {
	app := aarv.New(aarv.WithBanner(true))
	app.Use(aarv.Recovery(), aarv.Logger())

	app.Get("/events", func(c *aarv.Context) error {
		sse, err := c.SSE()
		if err != nil {
			return err
		}
		defer func() { _ = sse.Close() }()

		for i := 1; i <= 10; i++ {
			select {
			case <-sse.Done():
				return nil
			case <-time.After(time.Second):
			}

			err := sse.Send(aarv.SSEEvent{
				Event: "tick",
				ID:    fmt.Sprintf("%d", i),
				Data:  fmt.Sprintf(`{"count":%d,"time":%q}`, i, time.Now().Format(time.RFC3339)),
			})
			if err != nil {
				if errors.Is(err, http.ErrAbortHandler) {
					return nil
				}
				return err
			}
		}

		return sse.Send(aarv.SSEEvent{Event: "done", Data: `{"done":true}`})
	})

	app.Get("/", func(c *aarv.Context) error {
		return c.HTML(http.StatusOK, `<html><body><pre id="out"></pre><script>
const out = document.getElementById("out");
const es = new EventSource("/events");
es.onmessage = ev => out.textContent += ev.data + "\n";
es.addEventListener("tick", ev => out.textContent += ev.data + "\n");
es.addEventListener("done", ev => { out.textContent += ev.data + "\n"; es.close(); });
</script></body></html>`)
	})

	fmt.Println("SSE example on :8080")
	fmt.Println("  curl -N http://localhost:8080/events")

	_ = app.Listen(":8080")
}
