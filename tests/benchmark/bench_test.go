package benchmark

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	playgroundvalidator "github.com/go-playground/validator/v10"
	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/internal/benchutil"
	"github.com/nilshah80/aarv/plugins/encrypt"
	"github.com/nilshah80/aarv/plugins/logger"
	"github.com/nilshah80/aarv/plugins/verboselog"
)

// --- Router Benchmarks ---

func BenchmarkRouterStatic(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/hello", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest("GET", "/hello", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkRouterParam(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/users/{id}", func(c *aarv.Context) error {
		_ = c.Param("id")
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest("GET", "/users/123", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkRouterParamMulti(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/users/{userId}/posts/{postId}", func(c *aarv.Context) error {
		_ = c.Param("userId")
		_ = c.Param("postId")
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest("GET", "/users/123/posts/456", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

// --- JSON Response Benchmarks ---

func BenchmarkContextJSON_Small(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	type smallResp struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	app.Get("/json", func(c *aarv.Context) error {
		return c.JSON(200, smallResp{ID: 1, Name: "alice"})
	})

	req := httptest.NewRequest("GET", "/json", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkContextJSON_Medium(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	type medResp struct {
		ID      int      `json:"id"`
		Name    string   `json:"name"`
		Email   string   `json:"email"`
		Age     int      `json:"age"`
		Active  bool     `json:"active"`
		Tags    []string `json:"tags"`
		Address struct {
			Street string `json:"street"`
			City   string `json:"city"`
			Zip    string `json:"zip"`
		} `json:"address"`
	}
	resp := medResp{
		ID: 1, Name: "alice", Email: "alice@test.com", Age: 30, Active: true,
		Tags: []string{"admin", "user", "editor"},
	}
	resp.Address.Street = "123 Main St"
	resp.Address.City = "Springfield"
	resp.Address.Zip = "62704"

	app.Get("/json", func(c *aarv.Context) error {
		return c.JSON(200, resp)
	})

	req := httptest.NewRequest("GET", "/json", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkContextText(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/text", func(c *aarv.Context) error {
		return c.Text(200, "Hello, World!")
	})

	req := httptest.NewRequest("GET", "/text", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

// --- Bind Benchmarks ---

func BenchmarkBind_SmallStruct(b *testing.B) {
	type Req struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	type Res struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Post("/users", aarv.Bind(func(c *aarv.Context, req Req) (Res, error) {
		return Res{ID: "1", Name: req.Name}, nil
	}))

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkBind_LargeStruct(b *testing.B) {
	type Req struct {
		Name    string `json:"name"`
		Email   string `json:"email"`
		Age     int    `json:"age"`
		Phone   string `json:"phone"`
		Street  string `json:"street"`
		City    string `json:"city"`
		State   string `json:"state"`
		Zip     string `json:"zip"`
		Country string `json:"country"`
		Company string `json:"company"`
		Title   string `json:"title"`
		Bio     string `json:"bio"`
		Website string `json:"website"`
		Twitter string `json:"twitter"`
		Github  string `json:"github"`
	}
	type Res struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Post("/users", aarv.Bind(func(c *aarv.Context, req Req) (Res, error) {
		return Res{ID: "1", Name: req.Name}, nil
	}))

	body := []byte(`{"name":"alice","email":"a@t.com","age":30,"phone":"555","street":"123 Main","city":"NYC","state":"NY","zip":"10001","country":"US","company":"Acme","title":"Eng","bio":"dev","website":"https://a.com","twitter":"@a","github":"alice"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkBindReq_ParamQuery(b *testing.B) {
	type Req struct {
		ID    string `param:"id"`
		Page  int    `query:"page" default:"1"`
		Limit int    `query:"limit" default:"20"`
		Sort  string `query:"sort" default:"created_at"`
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Get("/users/{id}", aarv.BindReq(func(c *aarv.Context, req Req) error {
		return c.Text(200, "ok")
	}))

	req := httptest.NewRequest("GET", "/users/abc?page=2&limit=50&sort=name", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

// --- Validation Benchmarks ---

func BenchmarkValidation_Small(b *testing.B) {
	type Req struct {
		Name  string `json:"name" validate:"required,min=2,max=50"`
		Email string `json:"email" validate:"required,email"`
		Age   int    `json:"age" validate:"gte=0,lte=150"`
	}
	type Res struct {
		ID string `json:"id"`
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Post("/users", aarv.Bind(func(c *aarv.Context, req Req) (Res, error) {
		return Res{ID: "1"}, nil
	}))

	body := []byte(`{"name":"alice","email":"alice@test.com","age":30}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkValidation_10Fields(b *testing.B) {
	type Req struct {
		Name    string `json:"name" validate:"required,min=2,max=50"`
		Email   string `json:"email" validate:"required,email"`
		Age     int    `json:"age" validate:"gte=0,lte=150"`
		Phone   string `json:"phone" validate:"required,min=5"`
		Street  string `json:"street" validate:"required"`
		City    string `json:"city" validate:"required,min=2"`
		State   string `json:"state" validate:"required,len=2"`
		Zip     string `json:"zip" validate:"required,numeric,len=5"`
		Country string `json:"country" validate:"required,len=2"`
		Role    string `json:"role" validate:"required,oneof=admin user editor"`
	}
	type Res struct {
		ID string `json:"id"`
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Post("/users", aarv.Bind(func(c *aarv.Context, req Req) (Res, error) {
		return Res{ID: "1"}, nil
	}))

	body := []byte(`{"name":"alice","email":"a@t.com","age":30,"phone":"55555","street":"123 Main","city":"NYC","state":"NY","zip":"10001","country":"US","role":"admin"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

type validationComparePayload struct {
	Name    string `json:"name" validate:"required,min=2,max=50"`
	Email   string `json:"email" validate:"required,email"`
	Age     int    `json:"age" validate:"gte=0,lte=150"`
	Phone   string `json:"phone" validate:"required,min=5"`
	Street  string `json:"street" validate:"required"`
	City    string `json:"city" validate:"required,min=2"`
	State   string `json:"state" validate:"required,len=2"`
	Zip     string `json:"zip" validate:"required,numeric,len=5"`
	Country string `json:"country" validate:"required,len=2"`
	Role    string `json:"role" validate:"required,oneof=admin user editor"`
}

var validationCompareBody = []byte(`{"name":"alice","email":"a@t.com","age":30,"phone":"55555","street":"123 Main","city":"NYC","state":"NY","zip":"10001","country":"US","role":"admin"}`)

func newValidationCompareApp() *aarv.App {
	app := aarv.New(aarv.WithBanner(false))
	app.Post("/users", aarv.BindReq(func(c *aarv.Context, req validationComparePayload) error {
		return c.NoContent(http.StatusNoContent)
	}))
	return app
}

func newValidationCompareRequest() *http.Request {
	return &http.Request{
		Method:        http.MethodPost,
		URL:           &url.URL{Path: "/users"},
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(validationCompareBody)),
		ContentLength: int64(len(validationCompareBody)),
	}
}

func newValidationCompareAppWithMiddleware() *aarv.App {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(noopMiddleware)
	app.Use(noopMiddleware)
	app.AddHook(aarv.OnRequest, func(c *aarv.Context) error {
		c.Set("request_id", "bench-id")
		return nil
	})
	app.Post("/users", aarv.BindReq(func(c *aarv.Context, req validationComparePayload) error {
		return c.NoContent(http.StatusNoContent)
	}))
	return app
}

func benchmarkValidationCompareAarv(b *testing.B, app *aarv.App) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/users", bytes.NewReader(validationCompareBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			b.Fatalf("unexpected status %d", rec.Code)
		}
	}
}

func benchmarkValidationCompareAarvLight(b *testing.B, app *aarv.App) {
	var w benchutil.DiscardResponseWriter

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Reset()
		req := newValidationCompareRequest()
		app.ServeHTTP(&w, req)
		if w.Status != http.StatusNoContent {
			b.Fatalf("unexpected status %d", w.Status)
		}
	}
}

func benchmarkValidationCompareGoPlayground(b *testing.B, validate *playgroundvalidator.Validate) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var payload validationComparePayload
		if err := json.Unmarshal(validationCompareBody, &payload); err != nil {
			b.Fatal(err)
		}
		if err := validate.Struct(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidationCompare_10Fields(b *testing.B) {
	app := newValidationCompareApp()
	validate := playgroundvalidator.New()

	b.Run("aarv_bindreq", func(b *testing.B) {
		benchmarkValidationCompareAarv(b, app)
	})

	b.Run("json_unmarshal_plus_go_playground", func(b *testing.B) {
		benchmarkValidationCompareGoPlayground(b, validate)
	})
}

func BenchmarkValidationCompare10FieldsAarv(b *testing.B) {
	benchmarkValidationCompareAarv(b, newValidationCompareApp())
}

func BenchmarkValidationCompare10FieldsAarvWithMiddleware(b *testing.B) {
	benchmarkValidationCompareAarv(b, newValidationCompareAppWithMiddleware())
}

func BenchmarkValidationCompare10FieldsAarvLight(b *testing.B) {
	benchmarkValidationCompareAarvLight(b, newValidationCompareApp())
}

func BenchmarkValidationCompare10FieldsAarvWithMiddlewareLight(b *testing.B) {
	benchmarkValidationCompareAarvLight(b, newValidationCompareAppWithMiddleware())
}

func BenchmarkValidationCompare10FieldsGoPlayground(b *testing.B) {
	benchmarkValidationCompareGoPlayground(b, playgroundvalidator.New())
}

// --- Middleware Chain Benchmarks ---

func noopMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func BenchmarkMiddlewareChain_0(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})
	req := httptest.NewRequest("GET", "/test", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkMiddlewareChain_1(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(noopMiddleware)
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})
	req := httptest.NewRequest("GET", "/test", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkMiddlewareChain_5(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	for i := 0; i < 5; i++ {
		app.Use(noopMiddleware)
	}
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})
	req := httptest.NewRequest("GET", "/test", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkMiddlewareChain_10(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	for i := 0; i < 10; i++ {
		app.Use(noopMiddleware)
	}
	app.Get("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})
	req := httptest.NewRequest("GET", "/test", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

// --- Full Stack Benchmark ---

func BenchmarkFullStack(b *testing.B) {
	type Req struct {
		Name  string `json:"name" validate:"required,min=2"`
		Email string `json:"email" validate:"required,email"`
	}
	type Res struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(noopMiddleware) // simulate recovery
	app.Use(noopMiddleware) // simulate logger

	app.AddHook(aarv.OnRequest, func(c *aarv.Context) error {
		c.Set("requestId", "bench-id")
		return nil
	})

	app.Post("/users", aarv.Bind(func(c *aarv.Context, req Req) (Res, error) {
		return Res{ID: "1", Name: req.Name, Email: req.Email}, nil
	}))

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

// --- Baseline: Raw net/http for comparison ---

func BenchmarkRawNetHTTP(b *testing.B) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	req := httptest.NewRequest("GET", "/hello", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
	}
}

// --- Parallel Benchmarks ---

func BenchmarkRouterStatic_Parallel(b *testing.B) {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/hello", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/hello", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
		}
	})
}

func BenchmarkFullStack_Parallel(b *testing.B) {
	type Req struct {
		Name  string `json:"name" validate:"required,min=2"`
		Email string `json:"email" validate:"required,email"`
	}
	type Res struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	app := aarv.New(aarv.WithBanner(false))
	app.Use(noopMiddleware)
	app.Post("/users", aarv.Bind(func(c *aarv.Context, req Req) (Res, error) {
		return Res{ID: "1", Name: req.Name, Email: req.Email}, nil
	}))

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
		}
	})
}

// --- Encryption Benchmarks ---

func BenchmarkEncrypt_Response(b *testing.B) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	app := aarv.New(aarv.WithBanner(false))
	encMiddleware, _ := encrypt.New(key)
	app.Use(encMiddleware)

	app.Get("/users", func(c *aarv.Context) error {
		return c.JSON(200, map[string]any{
			"id": "1", "name": "alice", "email": "alice@test.com",
		})
	})

	req := httptest.NewRequest("GET", "/users", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkEncrypt_RequestResponse(b *testing.B) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	enc, _ := encrypt.NewEncryptor(key)
	plainBody := []byte(`{"name":"alice","email":"alice@test.com"}`)
	encryptedBody, _ := enc.Encrypt(plainBody)

	app := aarv.New(aarv.WithBanner(false))
	encMiddleware, _ := encrypt.New(key)
	app.Use(encMiddleware)

	app.Post("/users", func(c *aarv.Context) error {
		return c.JSON(201, map[string]string{"id": "1", "status": "created"})
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(encryptedBody))
		req.Header.Set("Content-Type", encrypt.EncryptedContentType)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkEncrypt_Disabled(b *testing.B) {
	// Baseline: same endpoint without encryption for comparison
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/users", func(c *aarv.Context) error {
		return c.JSON(200, map[string]any{
			"id": "1", "name": "alice", "email": "alice@test.com",
		})
	})

	req := httptest.NewRequest("GET", "/users", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

// --- Logger Benchmarks ---

func BenchmarkLogger_Standard(b *testing.B) {
	// Discard log output during benchmark
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(logger.New())

	app.Post("/users", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": "1", "name": "alice"})
	})

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkLogger_JSON(b *testing.B) {
	// JSON handler output
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(logger.New())

	app.Post("/users", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": "1", "name": "alice"})
	})

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkLogger_DumpFull(b *testing.B) {
	// Full dump logger with body capture
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(verboselog.New())

	app.Post("/users", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": "1", "name": "alice"})
	})

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkLogger_DumpMetadataOnly(b *testing.B) {
	// Dump logger with body logging disabled
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(verboselog.New(verboselog.Config{
		LogRequestBody:  false,
		LogResponseBody: false,
	}))

	app.Post("/users", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": "1", "name": "alice"})
	})

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkLogger_NoLogging(b *testing.B) {
	// Baseline: no logging middleware
	app := aarv.New(aarv.WithBanner(false))

	app.Post("/users", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": "1", "name": "alice"})
	})

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkLogger_VerboseMinimal(b *testing.B) {
	// Minimal verboselog config - maximum performance
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	app := aarv.New(aarv.WithBanner(false))
	app.Use(verboselog.New(verboselog.MinimalConfig()))

	app.Post("/users", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": "1", "name": "alice"})
	})

	body := []byte(`{"name":"alice","email":"alice@test.com"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}
