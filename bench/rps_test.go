package bench

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nilshah80/aarv"
)

func TestRPS(t *testing.T) {
	type Req struct {
		Name  string `json:"name" validate:"required,min=2"`
		Email string `json:"email" validate:"required,email"`
	}
	type Res struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	scenarios := []struct {
		name   string
		setup  func() *aarv.App
		method string
		path   string
		body   []byte
	}{
		{
			name: "Static Text",
			setup: func() *aarv.App {
				app := aarv.New(aarv.WithBanner(false))
				app.Get("/hello", func(c *aarv.Context) error {
					return c.Text(200, "ok")
				})
				return app
			},
			method: "GET",
			path:   "/hello",
		},
		{
			name: "JSON Response",
			setup: func() *aarv.App {
				app := aarv.New(aarv.WithBanner(false))
				app.Get("/json", func(c *aarv.Context) error {
					return c.JSON(200, map[string]string{"message": "hello"})
				})
				return app
			},
			method: "GET",
			path:   "/json",
		},
		{
			name: "Path Param",
			setup: func() *aarv.App {
				app := aarv.New(aarv.WithBanner(false))
				app.Get("/users/{id}", func(c *aarv.Context) error {
					return c.JSON(200, map[string]string{"id": c.Param("id")})
				})
				return app
			},
			method: "GET",
			path:   "/users/123",
		},
		{
			name: "Bind+Validate+JSON",
			setup: func() *aarv.App {
				app := aarv.New(aarv.WithBanner(false))
				app.Post("/users", aarv.Bind(func(c *aarv.Context, req Req) (Res, error) {
					return Res{ID: "1", Name: req.Name, Email: req.Email}, nil
				}))
				return app
			},
			method: "POST",
			path:   "/users",
			body:   []byte(`{"name":"alice","email":"alice@test.com"}`),
		},
		{
			name: "Full Stack (MW+Hook+Bind+Validate)",
			setup: func() *aarv.App {
				app := aarv.New(aarv.WithBanner(false))
				app.Use(aarv.Recovery(), aarv.Logger())
				app.AddHook(aarv.OnRequest, func(c *aarv.Context) error {
					c.Set("requestId", "rid-123")
					return nil
				})
				app.Post("/users", aarv.Bind(func(c *aarv.Context, req Req) (Res, error) {
					return Res{ID: "1", Name: req.Name, Email: req.Email}, nil
				}))
				return app
			},
			method: "POST",
			path:   "/users",
			body:   []byte(`{"name":"alice","email":"alice@test.com"}`),
		},
		{
			name:   "Raw net/http (baseline)",
			setup:  func() *aarv.App { return nil },
			method: "GET",
			path:   "/hello",
		},
	}

	duration := 3 * time.Second
	numWorkers := 16

	fmt.Println()
	fmt.Println("=== Aarv RPS Benchmark ===")
	fmt.Printf("Duration: %s | Workers: %d\n", duration, numWorkers)
	fmt.Println("-------------------------------------------")
	fmt.Printf("%-38s %12s\n", "Scenario", "RPS")
	fmt.Println("-------------------------------------------")

	for _, sc := range scenarios {
		var handler http.Handler
		if sc.name == "Raw net/http (baseline)" {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(200)
				w.Write([]byte("ok"))
			})
			handler = mux
		} else {
			handler = sc.setup()
		}

		var count atomic.Int64
		stop := make(chan struct{})
		var wg sync.WaitGroup

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						var req *http.Request
						if sc.body != nil {
							req = httptest.NewRequest(sc.method, sc.path, bytes.NewReader(sc.body))
							req.Header.Set("Content-Type", "application/json")
						} else {
							req = httptest.NewRequest(sc.method, sc.path, nil)
						}
						rec := httptest.NewRecorder()
						handler.ServeHTTP(rec, req)
						count.Add(1)
					}
				}
			}()
		}

		time.Sleep(duration)
		close(stop)
		wg.Wait()

		total := count.Load()
		rps := float64(total) / duration.Seconds()
		fmt.Printf("%-38s %12s\n", sc.name, formatRPS(rps))
	}
	fmt.Println("-------------------------------------------")
	fmt.Println()
}

func formatRPS(rps float64) string {
	if rps >= 1_000_000 {
		return fmt.Sprintf("%.2fM", rps/1_000_000)
	}
	if rps >= 1_000 {
		return fmt.Sprintf("%.0fK", rps/1_000)
	}
	return fmt.Sprintf("%.0f", rps)
}
