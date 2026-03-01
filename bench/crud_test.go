package bench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/mrshabel/mach"
	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/health"
	"github.com/nilshah80/aarv/plugins/requestid"
)

// ---- Domain types (mirrors rest-crud example) ----

type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Age       int    `json:"age"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

type CreateUserReq struct {
	Name  string `json:"name"  validate:"required,min=2,max=100"`
	Email string `json:"email" validate:"required,email"`
	Age   int    `json:"age"   validate:"gte=0,lte=150"`
	Role  string `json:"role"  validate:"oneof=admin user moderator" default:"user"`
}

type UpdateUserReq struct {
	ID    string `param:"id"`
	Name  string `json:"name"  validate:"omitempty,min=2,max=100"`
	Email string `json:"email" validate:"omitempty,email"`
	Age   int    `json:"age"   validate:"omitempty,gte=0,lte=150"`
}

type GetUserReq struct {
	ID     string `param:"id"`
	Fields string `query:"fields" default:"*"`
}

type ListUsersReq struct {
	Page     int    `query:"page"      default:"1"`
	PageSize int    `query:"page_size" default:"20"`
	Sort     string `query:"sort"      default:"created_at"`
	Order    string `query:"order"     default:"desc"`
}

type ListUsersRes struct {
	Users []User `json:"users"`
	Total int    `json:"total"`
	Page  int    `json:"page"`
}

// ---- In-memory store ----

type userStore struct {
	mu     sync.RWMutex
	data   map[string]User
	emails map[string]string // email → id (secondary index)
	nextID int
}

func newUserStore() *userStore {
	return &userStore{
		data:   make(map[string]User),
		emails: make(map[string]string),
	}
}

func (s *userStore) genID() string {
	s.nextID++
	return fmt.Sprintf("usr_%03d", s.nextID)
}

func (s *userStore) reset() {
	s.mu.Lock()
	s.data = make(map[string]User)
	s.emails = make(map[string]string)
	s.nextID = 0
	s.mu.Unlock()
}

func (s *userStore) seed(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("usr_%03d", i+1)
		email := fmt.Sprintf("user%d@test.com", i)
		s.nextID = i + 1
		s.data[id] = User{
			ID:        id,
			Name:      fmt.Sprintf("User%d", i),
			Email:     email,
			Age:       20 + i%30,
			Role:      "user",
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		s.emails[email] = id
	}
}

// ---- Build the CRUD app ----

// newCRUDApp builds the app with full middleware (for load tests).
func newCRUDApp(st *userStore) *aarv.App {
	return buildCRUDApp(st, true)
}

// newCRUDAppQuiet builds the app without logger (for micro-benchmarks).
func newCRUDAppQuiet(st *userStore) *aarv.App {
	return buildCRUDApp(st, false)
}

func buildCRUDApp(st *userStore, withLogger bool) *aarv.App {
	app := aarv.New(aarv.WithBanner(false))

	mw := []aarv.Middleware{aarv.Recovery(), requestid.New()}
	if withLogger {
		mw = append(mw, aarv.Logger())
	}
	mw = append(mw, health.New())
	app.Use(mw...)

	app.Group("/api/v1", func(g *aarv.RouteGroup) {
		g.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (User, error) {
			st.mu.Lock()
			defer st.mu.Unlock()
			if _, dup := st.emails[req.Email]; dup {
				return User{}, aarv.ErrConflict("email already registered")
			}
			user := User{
				ID:        st.genID(),
				Name:      req.Name,
				Email:     req.Email,
				Age:       req.Age,
				Role:      req.Role,
				CreatedAt: time.Now().Format(time.RFC3339),
			}
			st.data[user.ID] = user
			st.emails[req.Email] = user.ID
			return user, nil
		}))

		g.Get("/users", aarv.Bind(func(c *aarv.Context, req ListUsersReq) (ListUsersRes, error) {
			st.mu.RLock()
			defer st.mu.RUnlock()
			users := make([]User, 0, len(st.data))
			for _, u := range st.data {
				users = append(users, u)
			}
			total := len(users)
			start := (req.Page - 1) * req.PageSize
			if start > total {
				start = total
			}
			end := start + req.PageSize
			if end > total {
				end = total
			}
			return ListUsersRes{Users: users[start:end], Total: total, Page: req.Page}, nil
		}))

		g.Get("/users/{id}", aarv.BindReq(func(c *aarv.Context, req GetUserReq) error {
			st.mu.RLock()
			user, ok := st.data[req.ID]
			st.mu.RUnlock()
			if !ok {
				return aarv.ErrNotFound("user not found")
			}
			return c.JSON(http.StatusOK, user)
		}))

		g.Put("/users/{id}", aarv.Bind(func(c *aarv.Context, req UpdateUserReq) (User, error) {
			st.mu.Lock()
			defer st.mu.Unlock()
			user, ok := st.data[req.ID]
			if !ok {
				return User{}, aarv.ErrNotFound("user not found")
			}
			if req.Name != "" {
				user.Name = req.Name
			}
			if req.Email != "" {
				user.Email = req.Email
			}
			if req.Age > 0 {
				user.Age = req.Age
			}
			st.data[user.ID] = user
			return user, nil
		}))

		g.Delete("/users/{id}", func(c *aarv.Context) error {
			id := c.Param("id")
			st.mu.Lock()
			defer st.mu.Unlock()
			user, ok := st.data[id]
			if !ok {
				return aarv.ErrNotFound("user not found")
			}
			delete(st.emails, user.Email)
			delete(st.data, id)
			return c.NoContent(http.StatusNoContent)
		})

		g.Get("/ping", func(c *aarv.Context) error {
			return c.Text(http.StatusOK, "pong")
		})
	})

	return app
}

// ========== Go Micro-benchmarks ==========

func BenchmarkCRUD_CreateUser(b *testing.B) {
	st := newUserStore()
	app := newCRUDAppQuiet(st)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body := fmt.Sprintf(`{"name":"User%d","email":"u%d@test.com","age":25}`, i, i)
		req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkCRUD_GetUser(b *testing.B) {
	st := newUserStore()
	st.seed(100)
	app := newCRUDAppQuiet(st)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/api/v1/users/usr_001", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkCRUD_ListUsers(b *testing.B) {
	st := newUserStore()
	st.seed(100)
	app := newCRUDAppQuiet(st)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/api/v1/users?page=1&page_size=20", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkCRUD_UpdateUser(b *testing.B) {
	st := newUserStore()
	st.seed(100)
	app := newCRUDAppQuiet(st)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("PUT", "/api/v1/users/usr_001", bytes.NewBufferString(`{"name":"Updated"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkCRUD_DeleteUser(b *testing.B) {
	st := newUserStore()
	app := newCRUDAppQuiet(st)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Re-seed a user to delete each iteration
		id := fmt.Sprintf("usr_%03d", i+1)
		st.mu.Lock()
		st.nextID = i + 1
		st.data[id] = User{ID: id, Name: "X", Email: "x@test.com", Role: "user"}
		st.mu.Unlock()

		req := httptest.NewRequest("DELETE", "/api/v1/users/"+id, nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkCRUD_ValidationError(b *testing.B) {
	st := newUserStore()
	app := newCRUDAppQuiet(st)
	body := `{"name":"A","email":"bad","age":200}`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkCRUD_HealthCheck(b *testing.B) {
	st := newUserStore()
	app := newCRUDAppQuiet(st)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

// --- Parallel ---

func BenchmarkCRUD_GetUserParallel(b *testing.B) {
	st := newUserStore()
	st.seed(100)
	app := newCRUDAppQuiet(st)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/api/v1/users/usr_050", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
		}
	})
}

func BenchmarkCRUD_ListUsersParallel(b *testing.B) {
	st := newUserStore()
	st.seed(100)
	app := newCRUDAppQuiet(st)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/api/v1/users?page=1&page_size=20", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
		}
	})
}

func BenchmarkCRUD_CreateUserParallel(b *testing.B) {
	st := newUserStore()
	app := newCRUDAppQuiet(st)
	var counter atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := counter.Add(1)
			body := fmt.Sprintf(`{"name":"User%d","email":"u%d@test.com","age":25}`, n, n)
			req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
		}
	})
}

// ========== Full CRUD Cycle Benchmark ==========

func BenchmarkCRUD_FullCycle(b *testing.B) {
	st := newUserStore()
	app := newCRUDAppQuiet(st)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create
		body := fmt.Sprintf(`{"name":"User%d","email":"u%d@test.com","age":25}`, i, i)
		req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		var created User
		json.NewDecoder(rec.Body).Decode(&created)

		// Read
		req = httptest.NewRequest("GET", "/api/v1/users/"+created.ID, nil)
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		// Update
		req = httptest.NewRequest("PUT", "/api/v1/users/"+created.ID, bytes.NewBufferString(`{"name":"Updated"}`))
		req.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		// List
		req = httptest.NewRequest("GET", "/api/v1/users?page=1&page_size=20", nil)
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		// Delete
		req = httptest.NewRequest("DELETE", "/api/v1/users/"+created.ID, nil)
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

// ========== TCP Load Test for REST CRUD ==========

const (
	crudRequests    = 200_000
	crudConcurrency = 100
	crudPerWorker   = crudRequests / crudConcurrency
)

func TestCRUDLoadTest(t *testing.T) {
	st := newUserStore()
	app := newCRUDApp(st)
	srv := httptest.NewServer(app)
	defer srv.Close()

	tr := &http.Transport{
		MaxIdleConns:        crudConcurrency,
		MaxIdleConnsPerHost: crudConcurrency,
		MaxConnsPerHost:     crudConcurrency,
		DisableKeepAlives:   false,
	}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()

	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                      Aarv REST CRUD — Load Test (Real TCP)                                       ║")
	fmt.Printf("║  Total: %dK requests/endpoint | Concurrency: %d VCs | CPU: %d cores                             ║\n",
		crudRequests/1_000, crudConcurrency, runtime.NumCPU())
	fmt.Printf("║  Platform: %s/%s | Go %s                                                  ║\n",
		runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Println("║  Middleware: recovery + requestid + logger + health                                               ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════════════════════════════════════════╝")

	// Collect goroutines before all tests
	runtime.GC()
	goroutinesBefore := runtime.NumGoroutine()

	type endpoint struct {
		name   string
		method string
		path   string
		body   func(int64) []byte // nil for no body, func for dynamic body
		setup  func()             // optional setup before load
	}

	endpoints := []endpoint{
		{
			name:   "GET /health",
			method: "GET",
			path:   "/health",
		},
		{
			name:   "GET /api/v1/ping",
			method: "GET",
			path:   "/api/v1/ping",
		},
		{
			name:   "GET /api/v1/users/{id}",
			method: "GET",
			path:   "/api/v1/users/usr_050",
			setup: func() {
				st.reset()
				st.seed(100)
			},
		},
		{
			name:   "GET /api/v1/users (list 100)",
			method: "GET",
			path:   "/api/v1/users?page=1&page_size=20",
			setup: func() {
				st.reset()
				st.seed(100)
			},
		},
		{
			name:   "POST /api/v1/users (create+validate)",
			method: "POST",
			path:   "/api/v1/users",
			body:   func(n int64) []byte { return []byte(fmt.Sprintf(`{"name":"User%d","email":"u%d@test.com","age":25}`, n, n)) },
			setup:  func() { st.reset() },
		},
		{
			name:   "PUT /api/v1/users/{id} (update)",
			method: "PUT",
			path:   "/api/v1/users/usr_050",
			body:   func(_ int64) []byte { return []byte(`{"name":"LoadTest"}`) },
			setup: func() {
				st.reset()
				st.seed(100)
			},
		},
		{
			name:   "POST /api/v1/users (validation error)",
			method: "POST",
			path:   "/api/v1/users",
			body:   func(_ int64) []byte { return []byte(`{"name":"A","email":"bad","age":200}`) },
		},
	}

	var results []loadResult

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			if ep.setup != nil {
				ep.setup()
			}

			workerLats := make([][]time.Duration, crudConcurrency)
			for i := range workerLats {
				workerLats[i] = make([]time.Duration, 0, crudPerWorker)
			}

			var counter atomic.Int64
			var statusOK, status4xx, status5xx, errs atomic.Int64

			memBefore := captureMemStats()
			cpuBefore := getProcessCPUTime()
			start := time.Now()

			var wg sync.WaitGroup
			for w := 0; w < crudConcurrency; w++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					lats := workerLats[id]
					for i := 0; i < crudPerWorker; i++ {
						var req *http.Request
						if ep.body != nil {
							n := counter.Add(1)
							req, _ = http.NewRequest(ep.method, srv.URL+ep.path, bytes.NewReader(ep.body(n)))
							req.Header.Set("Content-Type", "application/json")
						} else {
							req, _ = http.NewRequest(ep.method, srv.URL+ep.path, nil)
						}

						t0 := time.Now()
						resp, err := client.Do(req)
						lat := time.Since(t0)

						if err != nil {
							errs.Add(1)
							lats = append(lats, lat)
							continue
						}
						io.Copy(io.Discard, resp.Body)
						resp.Body.Close()

						switch {
						case resp.StatusCode >= 200 && resp.StatusCode < 400:
							statusOK.Add(1)
						case resp.StatusCode >= 400 && resp.StatusCode < 500:
							status4xx.Add(1)
						default:
							status5xx.Add(1)
						}
						lats = append(lats, lat)
					}
					workerLats[id] = lats
				}(w)
			}
			wg.Wait()
			elapsed := time.Since(start)
			cpuAfter := getProcessCPUTime()
			memAfter := captureMemStats()

			r := buildResult(ep.name, workerLats, elapsed, memBefore, memAfter, cpuBefore, cpuAfter)
			results = append(results, r)

			t.Logf("  2xx=%d  4xx=%d  5xx=%d  errs=%d",
				statusOK.Load(), status4xx.Load(), status5xx.Load(), errs.Load())
		})
	}

	// Print tables
	fmt.Println()
	fmt.Println("  Throughput & Latency")
	fmt.Println("  ┌────────────────────────────────────────┬──────────┬────────────┬──────────┬──────────┬──────────┬──────────┬──────────┬──────────┐")
	fmt.Println("  │ Endpoint                               │  Time    │    RPS     │   Avg    │   p50    │   p90    │   p95    │   p99    │   Max    │")
	fmt.Println("  ├────────────────────────────────────────┼──────────┼────────────┼──────────┼──────────┼──────────┼──────────┼──────────┼──────────┤")
	for _, r := range results {
		fmt.Printf("  │ %-38s │ %8s │ %10s │ %8s │ %8s │ %8s │ %8s │ %8s │ %8s │\n",
			r.name, fmtElapsed(r.elapsed), fmtRPS(r.rps),
			fmtLat(r.avgLat), fmtLat(r.p50), fmtLat(r.p90),
			fmtLat(r.p95), fmtLat(r.p99), fmtLat(r.maxLat))
	}
	fmt.Println("  └────────────────────────────────────────┴──────────┴────────────┴──────────┴──────────┴──────────┴──────────┴──────────┴──────────┘")

	fmt.Println()
	fmt.Println("  Memory, Allocations & CPU")
	fmt.Println("  ┌────────────────────────────────────────┬──────────┬───────────┬──────────┬───────────┬────────┬──────────┬──────────┬────────┐")
	fmt.Println("  │ Endpoint                               │  B/op    │ allocs/op │ TotalMB  │  HeapMB   │ GC#    │ GC Pause │ CPU Time │ CPU%   │")
	fmt.Println("  ├────────────────────────────────────────┼──────────┼───────────┼──────────┼───────────┼────────┼──────────┼──────────┼────────┤")
	for _, r := range results {
		fmt.Printf("  │ %-38s │ %8s │ %9d │ %8s │ %9s │ %6d │ %8s │ %8s │ %5.1f%% │\n",
			r.name,
			fmtBytes(r.bytesPerOp), r.allocPerOp,
			fmtMB(r.totalAlloc), fmtMB(r.heapInUse),
			r.gcCycles, fmtLat(time.Duration(r.gcPauseNs)),
			fmtElapsed(r.cpuTime), r.cpuPercent)
	}
	fmt.Println("  └────────────────────────────────────────┴──────────┴───────────┴──────────┴───────────┴────────┴──────────┴──────────┴────────┘")

	// Runtime summary
	runtime.GC()
	goroutinesAfter := runtime.NumGoroutine()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	fmt.Println()
	fmt.Println("  Runtime Summary")
	fmt.Printf("  %-25s %d → %d\n", "Goroutines:", goroutinesBefore, goroutinesAfter)
	fmt.Printf("  %-25s %.2f MB\n", "Final Heap:", float64(m.HeapInuse)/1024/1024)
	fmt.Printf("  %-25s %d\n", "Heap Objects:", m.HeapObjects)
	fmt.Printf("  %-25s %.2f MB\n", "Stack In Use:", float64(m.StackInuse)/1024/1024)
	fmt.Printf("  %-25s %d\n", "Total GC Cycles:", m.NumGC)
	fmt.Printf("  %-25s %d cores / GOMAXPROCS=%d\n", "CPU:", runtime.NumCPU(), runtime.GOMAXPROCS(0))
	fmt.Println()
}

// ========== Cross-Framework CRUD Benchmark ==========

// ---- Aarv CRUD bare (typed binding + validation, no middleware) ----

func newAarvCRUDCompare(st *userStore) http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Group("/api/v1", func(g *aarv.RouteGroup) {
		g.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (User, error) {
			st.mu.Lock()
			defer st.mu.Unlock()
			if _, dup := st.emails[req.Email]; dup {
				return User{}, aarv.ErrConflict("email already registered")
			}
			user := User{
				ID:        st.genID(),
				Name:      req.Name,
				Email:     req.Email,
				Age:       req.Age,
				Role:      req.Role,
				CreatedAt: time.Now().Format(time.RFC3339),
			}
			st.data[user.ID] = user
			st.emails[req.Email] = user.ID
			return user, nil
		}))

		g.Get("/users", aarv.Bind(func(c *aarv.Context, req ListUsersReq) (ListUsersRes, error) {
			st.mu.RLock()
			defer st.mu.RUnlock()
			users := make([]User, 0, len(st.data))
			for _, u := range st.data {
				users = append(users, u)
			}
			total := len(users)
			start := (req.Page - 1) * req.PageSize
			if start > total {
				start = total
			}
			end := start + req.PageSize
			if end > total {
				end = total
			}
			return ListUsersRes{Users: users[start:end], Total: total, Page: req.Page}, nil
		}))

		g.Get("/users/{id}", aarv.BindReq(func(c *aarv.Context, req GetUserReq) error {
			st.mu.RLock()
			user, ok := st.data[req.ID]
			st.mu.RUnlock()
			if !ok {
				return aarv.ErrNotFound("user not found")
			}
			return c.JSON(http.StatusOK, user)
		}))

		g.Put("/users/{id}", aarv.Bind(func(c *aarv.Context, req UpdateUserReq) (User, error) {
			st.mu.Lock()
			defer st.mu.Unlock()
			user, ok := st.data[req.ID]
			if !ok {
				return User{}, aarv.ErrNotFound("user not found")
			}
			if req.Name != "" {
				user.Name = req.Name
			}
			if req.Email != "" {
				user.Email = req.Email
			}
			if req.Age > 0 {
				user.Age = req.Age
			}
			st.data[user.ID] = user
			return user, nil
		}))

		g.Delete("/users/{id}", func(c *aarv.Context) error {
			id := c.Param("id")
			st.mu.Lock()
			defer st.mu.Unlock()
			user, ok := st.data[id]
			if !ok {
				return aarv.ErrNotFound("user not found")
			}
			delete(st.emails, user.Email)
			delete(st.data, id)
			return c.NoContent(http.StatusNoContent)
		})
	})
	return app
}

// ---- Mach CRUD (DecodeJSON + go-playground/validator) ----

var machValidate = validator.New()

func newMachCRUD(st *userStore) http.Handler {
	app := mach.New()
	g := app.Group("/api/v1")

	type machCreateReq struct {
		Name  string `json:"name"  validate:"required,min=2,max=100"`
		Email string `json:"email" validate:"required,email"`
		Age   int    `json:"age"   validate:"gte=0,lte=150"`
		Role  string `json:"role"  validate:"omitempty,oneof=admin user moderator"`
	}
	type machUpdateReq struct {
		Name  string `json:"name"  validate:"omitempty,min=2,max=100"`
		Email string `json:"email" validate:"omitempty,email"`
		Age   int    `json:"age"   validate:"omitempty,gte=0,lte=150"`
	}

	g.POST("/users", func(c *mach.Context) {
		var req machCreateReq
		if err := c.DecodeJSON(&req); err != nil {
			c.JSON(400, map[string]string{"error": err.Error()})
			return
		}
		if err := machValidate.Struct(req); err != nil {
			c.JSON(422, map[string]string{"error": err.Error()})
			return
		}
		if req.Role == "" {
			req.Role = "user"
		}

		st.mu.Lock()
		defer st.mu.Unlock()
		if _, dup := st.emails[req.Email]; dup {
			c.JSON(409, map[string]string{"error": "email already registered"})
			return
		}
		user := User{
			ID:        st.genID(),
			Name:      req.Name,
			Email:     req.Email,
			Age:       req.Age,
			Role:      req.Role,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		st.data[user.ID] = user
		st.emails[req.Email] = user.ID
		c.JSON(200, user)
	})

	g.GET("/users", func(c *mach.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

		st.mu.RLock()
		defer st.mu.RUnlock()
		users := make([]User, 0, len(st.data))
		for _, u := range st.data {
			users = append(users, u)
		}
		total := len(users)
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		c.JSON(200, ListUsersRes{Users: users[start:end], Total: total, Page: page})
	})

	g.GET("/users/{id}", func(c *mach.Context) {
		st.mu.RLock()
		user, ok := st.data[c.Param("id")]
		st.mu.RUnlock()
		if !ok {
			c.JSON(404, map[string]string{"error": "user not found"})
			return
		}
		c.JSON(200, user)
	})

	g.PUT("/users/{id}", func(c *mach.Context) {
		var req machUpdateReq
		if err := c.DecodeJSON(&req); err != nil {
			c.JSON(400, map[string]string{"error": err.Error()})
			return
		}
		if err := machValidate.Struct(req); err != nil {
			c.JSON(422, map[string]string{"error": err.Error()})
			return
		}

		st.mu.Lock()
		defer st.mu.Unlock()
		user, ok := st.data[c.Param("id")]
		if !ok {
			c.JSON(404, map[string]string{"error": "user not found"})
			return
		}
		if req.Name != "" {
			user.Name = req.Name
		}
		if req.Email != "" {
			user.Email = req.Email
		}
		if req.Age > 0 {
			user.Age = req.Age
		}
		st.data[user.ID] = user
		c.JSON(200, user)
	})

	g.DELETE("/users/{id}", func(c *mach.Context) {
		st.mu.Lock()
		defer st.mu.Unlock()
		user, ok := st.data[c.Param("id")]
		if !ok {
			c.JSON(404, map[string]string{"error": "user not found"})
			return
		}
		delete(st.emails, user.Email)
		delete(st.data, c.Param("id"))
		c.NoContent(204)
	})

	return app
}

// ---- Gin CRUD (go-playground/validator) ----

func newGinCRUD(st *userStore) http.Handler {
	r := gin.New()
	v1 := r.Group("/api/v1")

	type ginCreateReq struct {
		Name  string `json:"name"  binding:"required,min=2,max=100"`
		Email string `json:"email" binding:"required,email"`
		Age   int    `json:"age"   binding:"gte=0,lte=150"`
		Role  string `json:"role"  binding:"omitempty,oneof=admin user moderator"`
	}
	type ginUpdateReq struct {
		Name  string `json:"name"  binding:"omitempty,min=2,max=100"`
		Email string `json:"email" binding:"omitempty,email"`
		Age   int    `json:"age"   binding:"omitempty,gte=0,lte=150"`
	}

	v1.POST("/users", func(c *gin.Context) {
		var req ginCreateReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(422, gin.H{"error": err.Error()})
			return
		}
		if req.Role == "" {
			req.Role = "user"
		}

		st.mu.Lock()
		defer st.mu.Unlock()
		if _, dup := st.emails[req.Email]; dup {
			c.JSON(409, gin.H{"error": "email already registered"})
			return
		}
		user := User{
			ID:        st.genID(),
			Name:      req.Name,
			Email:     req.Email,
			Age:       req.Age,
			Role:      req.Role,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		st.data[user.ID] = user
		st.emails[req.Email] = user.ID
		c.JSON(200, user)
	})

	v1.GET("/users", func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

		st.mu.RLock()
		defer st.mu.RUnlock()
		users := make([]User, 0, len(st.data))
		for _, u := range st.data {
			users = append(users, u)
		}
		total := len(users)
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		c.JSON(200, ListUsersRes{Users: users[start:end], Total: total, Page: page})
	})

	v1.GET("/users/:id", func(c *gin.Context) {
		st.mu.RLock()
		user, ok := st.data[c.Param("id")]
		st.mu.RUnlock()
		if !ok {
			c.JSON(404, gin.H{"error": "user not found"})
			return
		}
		c.JSON(200, user)
	})

	v1.PUT("/users/:id", func(c *gin.Context) {
		var req ginUpdateReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(422, gin.H{"error": err.Error()})
			return
		}

		st.mu.Lock()
		defer st.mu.Unlock()
		user, ok := st.data[c.Param("id")]
		if !ok {
			c.JSON(404, gin.H{"error": "user not found"})
			return
		}
		if req.Name != "" {
			user.Name = req.Name
		}
		if req.Email != "" {
			user.Email = req.Email
		}
		if req.Age > 0 {
			user.Age = req.Age
		}
		st.data[user.ID] = user
		c.JSON(200, user)
	})

	v1.DELETE("/users/:id", func(c *gin.Context) {
		st.mu.Lock()
		defer st.mu.Unlock()
		user, ok := st.data[c.Param("id")]
		if !ok {
			c.JSON(404, gin.H{"error": "user not found"})
			return
		}
		delete(st.emails, user.Email)
		delete(st.data, c.Param("id"))
		c.Status(204)
	})

	return r
}

// ---- Fiber CRUD (no validation, BodyParser) ----

func newFiberCRUD(st *userStore) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	v1 := app.Group("/api/v1")

	v1.Post("/users", func(c *fiber.Ctx) error {
		var req struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Age   int    `json:"age"`
			Role  string `json:"role"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
		if req.Role == "" {
			req.Role = "user"
		}

		st.mu.Lock()
		defer st.mu.Unlock()
		if _, dup := st.emails[req.Email]; dup {
			return c.Status(409).JSON(fiber.Map{"error": "email already registered"})
		}
		user := User{
			ID:        st.genID(),
			Name:      req.Name,
			Email:     req.Email,
			Age:       req.Age,
			Role:      req.Role,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		st.data[user.ID] = user
		st.emails[req.Email] = user.ID
		return c.JSON(user)
	})

	v1.Get("/users", func(c *fiber.Ctx) error {
		page, _ := strconv.Atoi(c.Query("page", "1"))
		pageSize, _ := strconv.Atoi(c.Query("page_size", "20"))

		st.mu.RLock()
		defer st.mu.RUnlock()
		users := make([]User, 0, len(st.data))
		for _, u := range st.data {
			users = append(users, u)
		}
		total := len(users)
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		return c.JSON(ListUsersRes{Users: users[start:end], Total: total, Page: page})
	})

	v1.Get("/users/:id", func(c *fiber.Ctx) error {
		st.mu.RLock()
		user, ok := st.data[c.Params("id")]
		st.mu.RUnlock()
		if !ok {
			return c.Status(404).JSON(fiber.Map{"error": "user not found"})
		}
		return c.JSON(user)
	})

	v1.Put("/users/:id", func(c *fiber.Ctx) error {
		var req struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Age   int    `json:"age"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}

		st.mu.Lock()
		defer st.mu.Unlock()
		user, ok := st.data[c.Params("id")]
		if !ok {
			return c.Status(404).JSON(fiber.Map{"error": "user not found"})
		}
		if req.Name != "" {
			user.Name = req.Name
		}
		if req.Email != "" {
			user.Email = req.Email
		}
		if req.Age > 0 {
			user.Age = req.Age
		}
		st.data[user.ID] = user
		return c.JSON(user)
	})

	v1.Delete("/users/:id", func(c *fiber.Ctx) error {
		st.mu.Lock()
		defer st.mu.Unlock()
		user, ok := st.data[c.Params("id")]
		if !ok {
			return c.Status(404).JSON(fiber.Map{"error": "user not found"})
		}
		delete(st.emails, user.Email)
		delete(st.data, c.Params("id"))
		return c.SendStatus(204)
	})

	return app
}

// ========== Generic load runners (dynamic body support) ==========

func runCompareLoad(name string, handler http.Handler, method, path string,
	bodyFn func(int64) []byte, nReqs, conc int) loadResult {

	srv := httptest.NewServer(handler)
	defer srv.Close()

	url := srv.URL + path
	perWorker := nReqs / conc

	tr := &http.Transport{
		MaxIdleConns:        conc,
		MaxIdleConnsPerHost: conc,
		MaxConnsPerHost:     conc,
		DisableKeepAlives:   false,
	}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()

	workerLats := make([][]time.Duration, conc)
	for i := range workerLats {
		workerLats[i] = make([]time.Duration, 0, perWorker)
	}

	var counter atomic.Int64
	memBefore := captureMemStats()
	cpuBefore := getProcessCPUTime()
	start := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			lats := workerLats[id]
			for i := 0; i < perWorker; i++ {
				var req *http.Request
				if bodyFn != nil {
					n := counter.Add(1)
					req, _ = http.NewRequest(method, url, bytes.NewReader(bodyFn(n)))
					req.Header.Set("Content-Type", "application/json")
				} else {
					req, _ = http.NewRequest(method, url, nil)
				}
				t0 := time.Now()
				resp, err := client.Do(req)
				lat := time.Since(t0)
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				lats = append(lats, lat)
			}
			workerLats[id] = lats
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)
	cpuAfter := getProcessCPUTime()
	memAfter := captureMemStats()

	return buildResult(name, workerLats, elapsed, memBefore, memAfter, cpuBefore, cpuAfter)
}

func runFiberCompareLoad(name string, app *fiber.App, method, path string,
	bodyFn func(int64) []byte, nReqs, conc int) loadResult {

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go app.Listener(ln)
	defer app.Shutdown()

	baseURL := "http://" + ln.Addr().String()
	url := baseURL + path
	perWorker := nReqs / conc

	tr := &http.Transport{
		MaxIdleConns:        conc,
		MaxIdleConnsPerHost: conc,
		MaxConnsPerHost:     conc,
		DisableKeepAlives:   false,
	}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()

	// Warmup — wait for Fiber server to be ready
	for i := 0; i < 20; i++ {
		resp, err := client.Get(baseURL + "/api/v1/users")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	workerLats := make([][]time.Duration, conc)
	for i := range workerLats {
		workerLats[i] = make([]time.Duration, 0, perWorker)
	}

	var counter atomic.Int64
	memBefore := captureMemStats()
	cpuBefore := getProcessCPUTime()
	start := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			lats := workerLats[id]
			for i := 0; i < perWorker; i++ {
				var req *http.Request
				if bodyFn != nil {
					n := counter.Add(1)
					req, _ = http.NewRequest(method, url, bytes.NewReader(bodyFn(n)))
					req.Header.Set("Content-Type", "application/json")
				} else {
					req, _ = http.NewRequest(method, url, nil)
				}
				t0 := time.Now()
				resp, err := client.Do(req)
				lat := time.Since(t0)
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				lats = append(lats, lat)
			}
			workerLats[id] = lats
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)
	cpuAfter := getProcessCPUTime()
	memAfter := captureMemStats()

	return buildResult(name, workerLats, elapsed, memBefore, memAfter, cpuBefore, cpuAfter)
}

// ========== TestCRUDCompare — Cross-Framework CRUD Load Test ==========

const (
	compareReqs = 100_000
	compareConc = 100
)

func TestCRUDCompare(t *testing.T) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                  Aarv vs Mach vs Gin vs Fiber — CRUD Load Test (Real TCP)                      ║")
	fmt.Printf("║  Total: %dK requests/endpoint/framework | Concurrency: %d VCs | CPU: %d cores              ║\n",
		compareReqs/1_000, compareConc, runtime.NumCPU())
	fmt.Printf("║  Platform: %s/%s | Go %s                                                 ║\n",
		runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Println("╚══════════════════════════════════════════════════════════════════════════════════════════════════╝")

	type scenario struct {
		label  string
		method string
		path   string
		body   func(int64) []byte
		seed   int
	}

	scenarios := []scenario{
		{
			label:  "Create User (POST /api/v1/users — bind + validate + write)",
			method: "POST",
			path:   "/api/v1/users",
			body:   func(n int64) []byte { return []byte(fmt.Sprintf(`{"name":"User%d","email":"u%d@test.com","age":25}`, n, n)) },
		},
		{
			label:  "Get User (GET /api/v1/users/{id} — path param + read)",
			method: "GET",
			path:   "/api/v1/users/usr_050",
			seed:   100,
		},
		{
			label:  "List Users (GET /api/v1/users?page=1&page_size=20 — query + serialize)",
			method: "GET",
			path:   "/api/v1/users?page=1&page_size=20",
			seed:   100,
		},
		{
			label:  "Update User (PUT /api/v1/users/{id} — bind + read/write)",
			method: "PUT",
			path:   "/api/v1/users/usr_050",
			body:   func(_ int64) []byte { return []byte(`{"name":"Updated"}`) },
			seed:   100,
		},
	}

	for _, sc := range scenarios {
		fmt.Printf("\n  ━━ %s ━━\n", sc.label)

		var results []loadResult

		// Aarv (typed binding + auto-validation, no middleware)
		{
			st := newUserStore()
			if sc.seed > 0 {
				st.seed(sc.seed)
			}
			app := newAarvCRUDCompare(st)
			results = append(results, runCompareLoad("Aarv", app, sc.method, sc.path, sc.body, compareReqs, compareConc))
		}

		// Mach (manual DecodeJSON, no validation)
		{
			st := newUserStore()
			if sc.seed > 0 {
				st.seed(sc.seed)
			}
			app := newMachCRUD(st)
			results = append(results, runCompareLoad("Mach", app, sc.method, sc.path, sc.body, compareReqs, compareConc))
		}

		// Gin (ShouldBindJSON + go-playground/validator)
		{
			st := newUserStore()
			if sc.seed > 0 {
				st.seed(sc.seed)
			}
			app := newGinCRUD(st)
			results = append(results, runCompareLoad("Gin", app, sc.method, sc.path, sc.body, compareReqs, compareConc))
		}

		// Fiber (BodyParser, no validation, fasthttp transport)
		{
			st := newUserStore()
			if sc.seed > 0 {
				st.seed(sc.seed)
			}
			app := newFiberCRUD(st)
			results = append(results, runFiberCompareLoad("Fiber", app, sc.method, sc.path, sc.body, compareReqs, compareConc))
		}

		// Print tables
		printCRUDCompareTables(results)
	}

	fmt.Println()
	fmt.Println("  Notes:")
	fmt.Printf("  - %dK requests per framework per endpoint | %d concurrent connections\n",
		compareReqs/1_000, compareConc)
	fmt.Println("  - Real TCP (localhost) — includes connection reuse, kernel overhead")
	fmt.Println("  - All frameworks use same userStore with O(1) email index")
	fmt.Println("  - No middleware on any framework (bare framework overhead)")
	fmt.Println("  - Aarv: typed binding + auto-validation (required, min, email)")
	fmt.Println("  - Mach: DecodeJSON + go-playground/validator (manual wiring)")
	fmt.Println("  - Gin: ShouldBindJSON + go-playground/validator (built-in)")
	fmt.Println("  - Fiber: BodyParser (no validation), fasthttp transport")
	fmt.Println()
}

func printCRUDCompareTables(results []loadResult) {
	fmt.Println()
	fmt.Println("  Throughput & Latency")
	fmt.Println("  ┌────────────┬──────────┬────────────┬──────────┬──────────┬──────────┬──────────┬──────────┬──────────┐")
	fmt.Println("  │ Framework  │  Time    │    RPS     │   Avg    │   p50    │   p90    │   p95    │   p99    │   Max    │")
	fmt.Println("  ├────────────┼──────────┼────────────┼──────────┼──────────┼──────────┼──────────┼──────────┼──────────┤")
	for _, r := range results {
		fmt.Printf("  │ %-10s │ %8s │ %10s │ %8s │ %8s │ %8s │ %8s │ %8s │ %8s │\n",
			r.name, fmtElapsed(r.elapsed), fmtRPS(r.rps),
			fmtLat(r.avgLat), fmtLat(r.p50), fmtLat(r.p90),
			fmtLat(r.p95), fmtLat(r.p99), fmtLat(r.maxLat))
	}
	fmt.Println("  └────────────┴──────────┴────────────┴──────────┴──────────┴──────────┴──────────┴──────────┴──────────┘")

	fmt.Println()
	fmt.Println("  Memory, Allocations & CPU")
	fmt.Println("  ┌────────────┬──────────┬───────────┬──────────┬───────────┬────────┬──────────┬──────────┬────────┐")
	fmt.Println("  │ Framework  │  B/op    │ allocs/op │ TotalMB  │  HeapMB   │ GC#    │ GC Pause │ CPU Time │ CPU%   │")
	fmt.Println("  ├────────────┼──────────┼───────────┼──────────┼───────────┼────────┼──────────┼──────────┼────────┤")
	for _, r := range results {
		fmt.Printf("  │ %-10s │ %8s │ %9d │ %8s │ %9s │ %6d │ %8s │ %8s │ %5.1f%% │\n",
			r.name,
			fmtBytes(r.bytesPerOp), r.allocPerOp,
			fmtMB(r.totalAlloc), fmtMB(r.heapInUse),
			r.gcCycles, fmtLat(time.Duration(r.gcPauseNs)),
			fmtElapsed(r.cpuTime), r.cpuPercent)
	}
	fmt.Println("  └────────────┴──────────┴───────────┴──────────┴───────────┴────────┴──────────┴──────────┴────────┘")
}
