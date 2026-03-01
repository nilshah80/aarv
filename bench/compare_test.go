package bench

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v2"
	"github.com/mrshabel/mach"
	"github.com/nilshah80/aarv"
	"github.com/valyala/fasthttp"
)

// ========== Types ==========

type BindReq struct {
	Name  string `json:"name" validate:"required,min=2" binding:"required,min=2"`
	Email string `json:"email" validate:"required,email" binding:"required,email"`
}

type BindRes struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// ========== Setup helpers ==========

func init() { gin.SetMode(gin.ReleaseMode) }

// --- net/http ---

func newRawHTTP_Static() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})
	return mux
}

// --- Aarv ---

func newAarv_Static() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/hello", func(c *aarv.Context) error { return c.Text(200, "ok") })
	return app
}

func newAarv_JSON() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/json", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"message": "hello"})
	})
	return app
}

func newAarv_Param() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/users/{id}", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": c.Param("id")})
	})
	return app
}

func newAarv_Bind() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Post("/users", aarv.Bind(func(c *aarv.Context, req BindReq) (BindRes, error) {
		return BindRes{ID: "1", Name: req.Name, Email: req.Email}, nil
	}))
	return app
}

// --- Mach ---

func newMach_Static() http.Handler {
	app := mach.New()
	app.GET("/hello", func(c *mach.Context) { c.Text(200, "ok") })
	return app
}

func newMach_JSON() http.Handler {
	app := mach.New()
	app.GET("/json", func(c *mach.Context) { c.JSON(200, map[string]string{"message": "hello"}) })
	return app
}

func newMach_Param() http.Handler {
	app := mach.New()
	app.GET("/users/{id}", func(c *mach.Context) {
		c.JSON(200, map[string]string{"id": c.Param("id")})
	})
	return app
}

func newMach_Bind() http.Handler {
	app := mach.New()
	app.POST("/users", func(c *mach.Context) {
		var req BindReq
		if err := c.DecodeJSON(&req); err != nil {
			c.JSON(400, map[string]string{"error": err.Error()})
			return
		}
		c.JSON(200, BindRes{ID: "1", Name: req.Name, Email: req.Email})
	})
	return app
}

// --- Gin ---

func newGin_Static() http.Handler {
	r := gin.New()
	r.GET("/hello", func(c *gin.Context) { c.String(200, "ok") })
	return r
}

func newGin_JSON() http.Handler {
	r := gin.New()
	r.GET("/json", func(c *gin.Context) { c.JSON(200, gin.H{"message": "hello"}) })
	return r
}

func newGin_Param() http.Handler {
	r := gin.New()
	r.GET("/users/:id", func(c *gin.Context) { c.JSON(200, gin.H{"id": c.Param("id")}) })
	return r
}

func newGin_Bind() http.Handler {
	r := gin.New()
	r.POST("/users", func(c *gin.Context) {
		var req BindReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(422, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"id": "1", "name": req.Name, "email": req.Email})
	})
	return r
}

// --- Fiber ---

func newFiber_Static() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/hello", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func newFiber_JSON() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/json", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"message": "hello"}) })
	return app
}

func newFiber_Param() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/users/:id", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"id": c.Params("id")}) })
	return app
}

func newFiber_Bind() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/users", func(c *fiber.Ctx) error {
		var req BindReq
		if err := c.BodyParser(&req); err != nil {
			return c.Status(422).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(BindRes{ID: "1", Name: req.Name, Email: req.Email})
	})
	return app
}

// ========== Go Benchmark functions (allocs, B/op, ns/op) ==========

// --- Static ---
func BenchmarkStatic_RawHTTP(b *testing.B) { benchHTTP(b, newRawHTTP_Static(), "GET", "/hello", nil) }
func BenchmarkStatic_Aarv(b *testing.B)    { benchHTTP(b, newAarv_Static(), "GET", "/hello", nil) }
func BenchmarkStatic_Mach(b *testing.B)    { benchHTTP(b, newMach_Static(), "GET", "/hello", nil) }
func BenchmarkStatic_Gin(b *testing.B)     { benchHTTP(b, newGin_Static(), "GET", "/hello", nil) }
func BenchmarkStatic_Fiber(b *testing.B)   { benchFiber(b, newFiber_Static(), "GET", "/hello", nil) }

// --- JSON ---
func BenchmarkJSON_Aarv(b *testing.B)  { benchHTTP(b, newAarv_JSON(), "GET", "/json", nil) }
func BenchmarkJSON_Mach(b *testing.B)  { benchHTTP(b, newMach_JSON(), "GET", "/json", nil) }
func BenchmarkJSON_Gin(b *testing.B)   { benchHTTP(b, newGin_JSON(), "GET", "/json", nil) }
func BenchmarkJSON_Fiber(b *testing.B) { benchFiber(b, newFiber_JSON(), "GET", "/json", nil) }

// --- Param ---
func BenchmarkParam_Aarv(b *testing.B)  { benchHTTP(b, newAarv_Param(), "GET", "/users/123", nil) }
func BenchmarkParam_Mach(b *testing.B)  { benchHTTP(b, newMach_Param(), "GET", "/users/123", nil) }
func BenchmarkParam_Gin(b *testing.B)   { benchHTTP(b, newGin_Param(), "GET", "/users/123", nil) }
func BenchmarkParam_Fiber(b *testing.B) { benchFiber(b, newFiber_Param(), "GET", "/users/123", nil) }

// --- Bind ---
var jsonBody = []byte(`{"name":"alice","email":"alice@test.com"}`)

func BenchmarkBind_Aarv(b *testing.B)  { benchHTTP(b, newAarv_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBind_Mach(b *testing.B)  { benchHTTP(b, newMach_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBind_Gin(b *testing.B)   { benchHTTP(b, newGin_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBind_Fiber(b *testing.B) { benchFiber(b, newFiber_Bind(), "POST", "/users", jsonBody) }

// --- Parallel variants ---
func BenchmarkStaticParallel_Aarv(b *testing.B)  { benchHTTPParallel(b, newAarv_Static(), "GET", "/hello", nil) }
func BenchmarkStaticParallel_Mach(b *testing.B)  { benchHTTPParallel(b, newMach_Static(), "GET", "/hello", nil) }
func BenchmarkStaticParallel_Gin(b *testing.B)   { benchHTTPParallel(b, newGin_Static(), "GET", "/hello", nil) }
func BenchmarkStaticParallel_Fiber(b *testing.B) { benchFiberParallel(b, newFiber_Static(), "GET", "/hello", nil) }

func BenchmarkBindParallel_Aarv(b *testing.B)  { benchHTTPParallel(b, newAarv_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBindParallel_Mach(b *testing.B)  { benchHTTPParallel(b, newMach_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBindParallel_Gin(b *testing.B)   { benchHTTPParallel(b, newGin_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBindParallel_Fiber(b *testing.B) { benchFiberParallel(b, newFiber_Bind(), "POST", "/users", jsonBody) }

// ========== Bench helpers ==========

func benchHTTP(b *testing.B, handler http.Handler, method, path string, body []byte) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req *http.Request
		if body != nil {
			req = httptest.NewRequest(method, path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func benchHTTPParallel(b *testing.B, handler http.Handler, method, path string, body []byte) {
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var req *http.Request
			if body != nil {
				req = httptest.NewRequest(method, path, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(method, path, nil)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}
	})
}

func benchFiber(b *testing.B, app *fiber.App, method, path string, body []byte) {
	h := app.Handler()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod(method)
		ctx.Request.SetRequestURI(path)
		if body != nil {
			ctx.Request.Header.SetContentType("application/json")
			ctx.Request.SetBody(body)
		}
		h(ctx)
	}
}

func benchFiberParallel(b *testing.B, app *fiber.App, method, path string, body []byte) {
	h := app.Handler()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.Header.SetMethod(method)
			ctx.Request.SetRequestURI(path)
			if body != nil {
				ctx.Request.Header.SetContentType("application/json")
				ctx.Request.SetBody(body)
			}
			h(ctx)
		}
	})
}

// ========== Load Test: 100 VCs, 5M requests, real TCP, latency percentiles ==========

const (
	totalRequests = 500_000
	concurrency   = 100
	reqsPerWorker = totalRequests / concurrency // 5,000 each
)

// loadResult holds full metrics for a single framework run.
type loadResult struct {
	name    string
	total   int64
	elapsed time.Duration
	rps     float64

	// Latency
	avgLat time.Duration
	p50    time.Duration
	p90    time.Duration
	p95    time.Duration
	p99    time.Duration
	maxLat time.Duration

	// Memory (from runtime.MemStats delta)
	totalAlloc uint64 // cumulative bytes allocated
	mallocs    uint64 // cumulative heap alloc count
	bytesPerOp uint64 // totalAlloc / total
	allocPerOp uint64 // mallocs / total

	// GC
	gcCycles   uint32
	gcPauseNs  uint64 // total GC STW pause
	heapInUse  uint64 // heap bytes in use at end

	// CPU (process user+kernel time)
	cpuTime    time.Duration
	cpuPercent float64 // cpuTime / (elapsed * numCPU) * 100
}

func TestLoadTest(t *testing.T) {
	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    Aarv vs Mach vs Gin vs Fiber — Load Test (Real TCP)                           ║")
	fmt.Printf("║  Total: %dK requests | Concurrency: %d VCs | CPU: %d cores                                      ║\n",
		totalRequests/1_000, concurrency, runtime.NumCPU())
	fmt.Printf("║  Platform: %s/%s | Go %s                                                  ║\n",
		runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Println("╚═══════════════════════════════════════════════════════════════════════════════════════════════════╝")

	type testFn struct {
		name string
		fn   func() loadResult
	}
	type scenario struct {
		label string
		tests []testFn
	}

	scenarios := []scenario{
		{
			label: "Static Text (GET /hello -> \"ok\")",
			tests: []testFn{
				{"net/http", func() loadResult { return runTCPLoad("net/http", newRawHTTP_Static(), "GET", "/hello", nil) }},
				{"Aarv", func() loadResult { return runTCPLoad("Aarv", newAarv_Static(), "GET", "/hello", nil) }},
				{"Mach", func() loadResult { return runTCPLoad("Mach", newMach_Static(), "GET", "/hello", nil) }},
				{"Gin", func() loadResult { return runTCPLoad("Gin", newGin_Static(), "GET", "/hello", nil) }},
				{"Fiber", func() loadResult { return runFiberTCPLoad("Fiber", newFiber_Static(), "GET", "/hello", nil) }},
			},
		},
		{
			label: "JSON Response (GET /json)",
			tests: []testFn{
				{"Aarv", func() loadResult { return runTCPLoad("Aarv", newAarv_JSON(), "GET", "/json", nil) }},
				{"Mach", func() loadResult { return runTCPLoad("Mach", newMach_JSON(), "GET", "/json", nil) }},
				{"Gin", func() loadResult { return runTCPLoad("Gin", newGin_JSON(), "GET", "/json", nil) }},
				{"Fiber", func() loadResult { return runFiberTCPLoad("Fiber", newFiber_JSON(), "GET", "/json", nil) }},
			},
		},
		{
			label: "Path Param (GET /users/:id -> JSON)",
			tests: []testFn{
				{"Aarv", func() loadResult { return runTCPLoad("Aarv", newAarv_Param(), "GET", "/users/123", nil) }},
				{"Mach", func() loadResult { return runTCPLoad("Mach", newMach_Param(), "GET", "/users/123", nil) }},
				{"Gin", func() loadResult { return runTCPLoad("Gin", newGin_Param(), "GET", "/users/123", nil) }},
				{"Fiber", func() loadResult { return runFiberTCPLoad("Fiber", newFiber_Param(), "GET", "/users/123", nil) }},
			},
		},
		{
			label: "Bind JSON + Respond (POST /users)",
			tests: []testFn{
				{"Aarv", func() loadResult { return runTCPLoad("Aarv", newAarv_Bind(), "POST", "/users", jsonBody) }},
				{"Mach", func() loadResult { return runTCPLoad("Mach", newMach_Bind(), "POST", "/users", jsonBody) }},
				{"Gin", func() loadResult { return runTCPLoad("Gin", newGin_Bind(), "POST", "/users", jsonBody) }},
				{"Fiber", func() loadResult { return runFiberTCPLoad("Fiber", newFiber_Bind(), "POST", "/users", jsonBody) }},
			},
		},
	}

	for _, sc := range scenarios {
		fmt.Println()
		fmt.Printf("  ━━ %s ━━\n", sc.label)

		var results []loadResult
		for _, tf := range sc.tests {
			results = append(results, tf.fn())
		}

		// Table 1: Throughput & Latency
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

		// Table 2: Memory, Allocations & GC
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

	fmt.Println()
	fmt.Println("  Notes:")
	fmt.Printf("  - %dK requests per framework | %d concurrent connections | %d reqs/worker\n",
		totalRequests/1_000, concurrency, reqsPerWorker)
	fmt.Println("  - Real TCP (localhost) — includes connection reuse, kernel overhead")
	fmt.Println("  - B/op & allocs/op = server-side only (MemStats delta / total requests)")
	fmt.Println("  - CPU% = process CPU time / (wall time x cores) — higher = more CPU-efficient")
	fmt.Println("  - Fiber: fasthttp server (not net/http) — different transport layer")
	fmt.Println("  - Aarv Bind includes auto-validation (required, min, email)")
	fmt.Println("  - Gin Bind uses go-playground/validator")
	fmt.Println("  - Mach Bind is manual DecodeJSON only (no validation)")
	fmt.Println()
}

// ========== Snapshot helpers ==========

type memSnapshot struct {
	totalAlloc uint64
	mallocs    uint64
	numGC      uint32
	pauseNs    uint64
	heapInUse  uint64
}

func captureMemStats() memSnapshot {
	runtime.GC() // force GC to get clean baseline
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return memSnapshot{
		totalAlloc: m.TotalAlloc,
		mallocs:    m.Mallocs,
		numGC:      m.NumGC,
		pauseNs:    m.PauseTotalNs, // cumulative total, not ring buffer
		heapInUse:  m.HeapInuse,
	}
}

func getProcessCPUTime() time.Duration {
	var creation, exit, kernel, user syscall.Filetime
	h, _ := syscall.GetCurrentProcess()
	syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user)
	// Filetime is 100-nanosecond intervals
	k := int64(kernel.HighDateTime)<<32 | int64(kernel.LowDateTime)
	u := int64(user.HighDateTime)<<32 | int64(user.LowDateTime)
	return time.Duration((k + u) * 100) // convert 100ns units to ns
}

// ========== Real TCP load test runners ==========

func runTCPLoad(name string, handler http.Handler, method, path string, body []byte) loadResult {
	srv := httptest.NewServer(handler)
	defer srv.Close()

	url := srv.URL + path

	tr := &http.Transport{
		MaxIdleConns:        concurrency,
		MaxIdleConnsPerHost: concurrency,
		MaxConnsPerHost:     concurrency,
		DisableKeepAlives:   false,
	}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()

	workerLats := make([][]time.Duration, concurrency)
	for i := range workerLats {
		workerLats[i] = make([]time.Duration, 0, reqsPerWorker)
	}

	// Capture baseline
	memBefore := captureMemStats()
	cpuBefore := getProcessCPUTime()
	start := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			lats := workerLats[id]
			for i := 0; i < reqsPerWorker; i++ {
				var req *http.Request
				if body != nil {
					req, _ = http.NewRequest(method, url, bytes.NewReader(body))
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

func runFiberTCPLoad(name string, app *fiber.App, method, path string, body []byte) loadResult {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go app.Listener(ln)
	defer app.Shutdown()

	baseURL := "http://" + ln.Addr().String()
	url := baseURL + path

	tr := &http.Transport{
		MaxIdleConns:        concurrency,
		MaxIdleConnsPerHost: concurrency,
		MaxConnsPerHost:     concurrency,
		DisableKeepAlives:   false,
	}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()

	// Warmup
	for i := 0; i < 20; i++ {
		resp, err := client.Get(baseURL + "/hello")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	workerLats := make([][]time.Duration, concurrency)
	for i := range workerLats {
		workerLats[i] = make([]time.Duration, 0, reqsPerWorker)
	}

	memBefore := captureMemStats()
	cpuBefore := getProcessCPUTime()
	start := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			lats := workerLats[id]
			for i := 0; i < reqsPerWorker; i++ {
				var req *http.Request
				if body != nil {
					req, _ = http.NewRequest(method, url, bytes.NewReader(body))
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

// buildResult merges per-worker latencies and computes all metrics.
func buildResult(name string, workerLats [][]time.Duration, elapsed time.Duration,
	memBefore, memAfter memSnapshot, cpuBefore, cpuAfter time.Duration) loadResult {

	var totalCount int
	for _, lats := range workerLats {
		totalCount += len(lats)
	}

	all := make([]int64, 0, totalCount)
	var sumNs int64
	var maxLat time.Duration
	for _, lats := range workerLats {
		for _, d := range lats {
			ns := int64(d)
			all = append(all, ns)
			sumNs += ns
			if d > maxLat {
				maxLat = d
			}
		}
	}

	slices.Sort(all)

	n := int64(len(all))
	avgLat := time.Duration(sumNs / n)
	rps := float64(n) / elapsed.Seconds()

	// Memory deltas
	totalAlloc := memAfter.totalAlloc - memBefore.totalAlloc
	mallocs := memAfter.mallocs - memBefore.mallocs
	gcCycles := memAfter.numGC - memBefore.numGC
	gcPause := memAfter.pauseNs - memBefore.pauseNs

	// CPU
	cpuTime := cpuAfter - cpuBefore
	cpuPct := float64(cpuTime) / float64(elapsed) / float64(runtime.NumCPU()) * 100

	return loadResult{
		name:       name,
		total:      n,
		elapsed:    elapsed,
		rps:        rps,
		avgLat:     avgLat,
		p50:        time.Duration(all[n*50/100]),
		p90:        time.Duration(all[n*90/100]),
		p95:        time.Duration(all[n*95/100]),
		p99:        time.Duration(all[n*99/100]),
		maxLat:     maxLat,
		totalAlloc: totalAlloc,
		mallocs:    mallocs,
		bytesPerOp: totalAlloc / uint64(n),
		allocPerOp: mallocs / uint64(n),
		gcCycles:   gcCycles,
		gcPauseNs:  gcPause,
		heapInUse:  memAfter.heapInUse,
		cpuTime:    cpuTime,
		cpuPercent: cpuPct,
	}
}

// ========== Formatting helpers ==========

func fmtElapsed(d time.Duration) string {
	s := d.Seconds()
	if s < 60 {
		return fmt.Sprintf("%.1fs", s)
	}
	return fmt.Sprintf("%.0fm%.0fs", s/60, float64(int(s)%60))
}

func fmtRPS(v float64) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.2fM", v/1_000_000)
	}
	return fmt.Sprintf("%.0fK", v/1_000)
}

func fmtBytes(b uint64) string {
	if b >= 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	}
	if b >= 1024 {
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	}
	return fmt.Sprintf("%dB", b)
}

func fmtMB(b uint64) string {
	mb := float64(b) / (1024 * 1024)
	if mb >= 1000 {
		return fmt.Sprintf("%.1fGB", mb/1024)
	}
	return fmt.Sprintf("%.1fMB", mb)
}

func fmtLat(d time.Duration) string {
	ns := float64(d)
	if ns < 1000 {
		return fmt.Sprintf("%.0fns", ns)
	}
	us := ns / 1000
	if us < 1000 {
		return fmt.Sprintf("%.1fµs", us)
	}
	return fmt.Sprintf("%.2fms", us/1000)
}
