// Example: deliver every captured request/response to an in-memory audit
// log via verboselog's Sink callback.
//
// Run:
//
//	go run ./examples/verboselog-audit
//
// Then in another terminal:
//
//	curl -s -X POST -H 'Content-Type: application/json' \
//	    -d '{"name":"alice","password":"secret"}' \
//	    http://localhost:8080/echo
//	curl -s http://localhost:8080/audit
package main

import (
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/verboselog"
)

// auditEntry is one row in the in-memory audit log.
type auditEntry struct {
	At        time.Time `json:"at"`
	RequestID string    `json:"request_id"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	LatencyMS float64   `json:"latency_ms"`
	BytesOut  int       `json:"bytes_out"`
	ReqBody   string    `json:"req_body"`
	RespBody  string    `json:"resp_body"`
	UserAgent string    `json:"user_agent,omitempty"`
	ClientIP  string    `json:"client_ip,omitempty"`
}

// auditStore is a goroutine-safe append-only log. In production, this would
// be a database write, an object-store PUT, or a message-queue publish — all
// of which should be performed inside a goroutine launched from the sink to
// avoid stalling the request thread on slow I/O.
type auditStore struct {
	mu      sync.Mutex
	entries []auditEntry
}

func (s *auditStore) append(e auditEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
}

func (s *auditStore) snapshot() []auditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]auditEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

func main() {
	store := &auditStore{}

	cfg := verboselog.DefaultConfig()
	// SuppressSlog true would deliver only via Sink; we leave it false here so
	// the log is also visible on stdout.
	cfg.SkipPaths = []string{"/audit"} // do not audit the audit endpoint
	cfg.Sink = func(c *aarv.Context, reqBody, respBody []byte, meta verboselog.DumpMeta) {
		store.append(auditEntry{
			At:        time.Now(),
			RequestID: meta.RequestID,
			Method:    meta.Method,
			Path:      meta.Path,
			Status:    meta.Status,
			LatencyMS: float64(meta.Latency.Microseconds()) / 1000.0,
			BytesOut:  meta.BytesOut,
			ReqBody:   string(reqBody),
			RespBody:  string(respBody),
			UserAgent: meta.UserAgent,
			ClientIP:  meta.ClientIP,
		})
	}

	app := aarv.New()
	app.Use(verboselog.New(cfg))

	app.Post("/echo", func(c *aarv.Context) error {
		body, _ := io.ReadAll(c.Request().Body)
		return c.JSON(http.StatusOK, map[string]string{"echoed": string(body)})
	})

	// Audit endpoint: returns the captured entries as JSON. SkipPaths above
	// excludes this endpoint from being audited itself.
	app.Get("/audit", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, store.snapshot())
	})

	log.Println("listening on :8080  (POST /echo, GET /audit)")
	if err := app.Listen(":8080"); err != nil {
		log.Fatal(err)
	}
}
