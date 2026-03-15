package benchmark

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	playgroundvalidator "github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/labstack/echo/v4"
	"github.com/mrshabel/mach"
	"github.com/nilshah80/aarv"
	jsonv2codec "github.com/nilshah80/aarv/codec/jsonv2"
	segmentiocodec "github.com/nilshah80/aarv/codec/segmentio"
	soniccodec "github.com/nilshah80/aarv/codec/sonic"
	"github.com/nilshah80/aarv/internal/benchutil"
	"github.com/nilshah80/aarv/plugins/encrypt"
	"github.com/nilshah80/aarv/plugins/logger"
	"github.com/nilshah80/aarv/plugins/verboselog"
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

// Larger realistic response (e.g., user profile with nested data)
type UserProfile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Email     string   `json:"email"`
	Age       int      `json:"age"`
	Active    bool     `json:"active"`
	Role      string   `json:"role"`
	Tags      []string `json:"tags"`
	Address   Address  `json:"address"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type Address struct {
	Street  string `json:"street"`
	City    string `json:"city"`
	State   string `json:"state"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
}

// List response (paginated)
type UserListResponse struct {
	Users      []UserProfile `json:"users"`
	Total      int           `json:"total"`
	Page       int           `json:"page"`
	PerPage    int           `json:"per_page"`
	TotalPages int           `json:"total_pages"`
}

var sampleUser = UserProfile{
	ID: "usr_123", Name: "Alice Smith", Email: "alice@example.com",
	Age: 28, Active: true, Role: "admin", Tags: []string{"vip", "beta", "early-adopter"},
	Address:   Address{Street: "123 Main St", City: "San Francisco", State: "CA", Zip: "94105", Country: "US"},
	CreatedAt: "2024-01-15T10:30:00Z", UpdatedAt: "2024-06-20T14:45:00Z",
}

var sampleUserList = UserListResponse{
	Users:      []UserProfile{sampleUser, sampleUser, sampleUser, sampleUser, sampleUser},
	Total:      100,
	Page:       1,
	PerPage:    5,
	TotalPages: 20,
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

func newAarv_BindWithCodec(codec aarv.Codec) http.Handler {
	app := aarv.New(aarv.WithBanner(false), aarv.WithCodec(codec))
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
	validate := playgroundvalidator.New()
	app := mach.New()
	app.POST("/users", func(c *mach.Context) {
		var req BindReq
		if err := c.DecodeJSON(&req); err != nil {
			c.JSON(400, map[string]string{"error": err.Error()})
			return
		}
		if err := validate.Struct(req); err != nil {
			c.JSON(422, map[string]string{"error": err.Error()})
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

// --- Echo ---

func newEcho_Static() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/hello", func(c echo.Context) error { return c.String(200, "ok") })
	return e
}

func newEcho_JSON() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/json", func(c echo.Context) error { return c.JSON(200, map[string]string{"message": "hello"}) })
	return e
}

func newEcho_Param() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/users/:id", func(c echo.Context) error { return c.JSON(200, map[string]string{"id": c.Param("id")}) })
	return e
}

func newEcho_Bind() http.Handler {
	validate := playgroundvalidator.New()
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.POST("/users", func(c echo.Context) error {
		var req BindReq
		if err := c.Bind(&req); err != nil {
			return c.JSON(422, map[string]string{"error": err.Error()})
		}
		if err := validate.Struct(req); err != nil {
			return c.JSON(422, map[string]string{"error": err.Error()})
		}
		return c.JSON(200, BindRes{ID: "1", Name: req.Name, Email: req.Email})
	})
	return e
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
	validate := playgroundvalidator.New()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/users", func(c *fiber.Ctx) error {
		var req BindReq
		if err := c.BodyParser(&req); err != nil {
			return c.Status(422).JSON(fiber.Map{"error": err.Error()})
		}
		if err := validate.Struct(req); err != nil {
			return c.Status(422).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(BindRes{ID: "1", Name: req.Name, Email: req.Email})
	})
	return app
}

// ========== Real-World Scenario: Middleware Stack ==========

func noopMiddlewareHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func newAarv_Middleware() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(aarv.Recovery())
	app.Use(noopMiddlewareHTTP) // simulate additional middleware like request ID
	app.Get("/api/users", func(c *aarv.Context) error {
		return c.JSON(200, sampleUser)
	})
	return app
}

func newAarv_MiddlewareFastMode() http.Handler {
	app := aarv.New(aarv.WithBanner(false), aarv.WithRequestContextBridge(false))
	app.Use(aarv.Recovery())
	app.Use(noopMiddlewareHTTP)
	app.Get("/api/users", func(c *aarv.Context) error {
		return c.JSON(200, sampleUser)
	})
	return app
}

func newMach_Middleware() http.Handler {
	app := mach.New()
	app.Use(mach.Recovery())
	app.Use(noopMiddlewareHTTP)
	app.GET("/api/users", func(c *mach.Context) {
		c.JSON(200, sampleUser)
	})
	return app
}

func newGin_Middleware() http.Handler {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) { c.Next() })
	r.GET("/api/users", func(c *gin.Context) {
		c.JSON(200, sampleUser)
	})
	return r
}

func newFiber_Middleware() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(func(c *fiber.Ctx) error { return c.Next() }) // recovery equivalent
	app.Use(func(c *fiber.Ctx) error { return c.Next() })
	app.Get("/api/users", func(c *fiber.Ctx) error {
		return c.JSON(sampleUser)
	})
	return app
}

func newEcho_Middleware() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { return next(c) }
	})
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { return next(c) }
	})
	e.GET("/api/users", func(c echo.Context) error { return c.JSON(200, sampleUser) })
	return e
}

// ========== Real-World Scenario: Query Parameters ==========

func newAarv_Query() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/api/users", func(c *aarv.Context) error {
		page := c.QueryDefault("page", "1")
		limit := c.QueryDefault("limit", "10")
		sort := c.QueryDefault("sort", "created_at")
		order := c.QueryDefault("order", "desc")
		return c.JSON(200, map[string]any{
			"page": page, "limit": limit, "sort": sort, "order": order,
			"users": []UserProfile{sampleUser},
		})
	})
	return app
}

func newMach_Query() http.Handler {
	app := mach.New()
	app.GET("/api/users", func(c *mach.Context) {
		page := c.Query("page")
		if page == "" {
			page = "1"
		}
		limit := c.Query("limit")
		if limit == "" {
			limit = "10"
		}
		sort := c.Query("sort")
		if sort == "" {
			sort = "created_at"
		}
		order := c.Query("order")
		if order == "" {
			order = "desc"
		}
		c.JSON(200, map[string]any{
			"page": page, "limit": limit, "sort": sort, "order": order,
			"users": []UserProfile{sampleUser},
		})
	})
	return app
}

func newGin_Query() http.Handler {
	r := gin.New()
	r.GET("/api/users", func(c *gin.Context) {
		page := c.DefaultQuery("page", "1")
		limit := c.DefaultQuery("limit", "10")
		sort := c.DefaultQuery("sort", "created_at")
		order := c.DefaultQuery("order", "desc")
		c.JSON(200, gin.H{
			"page": page, "limit": limit, "sort": sort, "order": order,
			"users": []UserProfile{sampleUser},
		})
	})
	return r
}

func newFiber_Query() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/api/users", func(c *fiber.Ctx) error {
		page := c.Query("page", "1")
		limit := c.Query("limit", "10")
		sort := c.Query("sort", "created_at")
		order := c.Query("order", "desc")
		return c.JSON(fiber.Map{
			"page": page, "limit": limit, "sort": sort, "order": order,
			"users": []UserProfile{sampleUser},
		})
	})
	return app
}

func newEcho_Query() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/api/users", func(c echo.Context) error {
		page := c.QueryParam("page")
		if page == "" {
			page = "1"
		}
		limit := c.QueryParam("limit")
		if limit == "" {
			limit = "10"
		}
		sort := c.QueryParam("sort")
		if sort == "" {
			sort = "created_at"
		}
		order := c.QueryParam("order")
		if order == "" {
			order = "desc"
		}
		return c.JSON(200, map[string]any{
			"page": page, "limit": limit, "sort": sort, "order": order,
			"users": []UserProfile{sampleUser},
		})
	})
	return e
}

// ========== Real-World Scenario: Multiple Path Params ==========

func newAarv_MultiParam() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/api/orgs/{orgId}/teams/{teamId}/members/{memberId}", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{
			"org_id": c.Param("orgId"), "team_id": c.Param("teamId"), "member_id": c.Param("memberId"),
		})
	})
	return app
}

func newMach_MultiParam() http.Handler {
	app := mach.New()
	app.GET("/api/orgs/{orgId}/teams/{teamId}/members/{memberId}", func(c *mach.Context) {
		c.JSON(200, map[string]string{
			"org_id": c.Param("orgId"), "team_id": c.Param("teamId"), "member_id": c.Param("memberId"),
		})
	})
	return app
}

func newGin_MultiParam() http.Handler {
	r := gin.New()
	r.GET("/api/orgs/:orgId/teams/:teamId/members/:memberId", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"org_id": c.Param("orgId"), "team_id": c.Param("teamId"), "member_id": c.Param("memberId"),
		})
	})
	return r
}

func newFiber_MultiParam() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/api/orgs/:orgId/teams/:teamId/members/:memberId", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"org_id": c.Params("orgId"), "team_id": c.Params("teamId"), "member_id": c.Params("memberId"),
		})
	})
	return app
}

func newEcho_MultiParam() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/api/orgs/:orgId/teams/:teamId/members/:memberId", func(c echo.Context) error {
		return c.JSON(200, map[string]string{
			"org_id": c.Param("orgId"), "team_id": c.Param("teamId"), "member_id": c.Param("memberId"),
		})
	})
	return e
}

// ========== Real-World Scenario: Headers + Auth-like ==========

func newAarv_Headers() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/api/protected", func(c *aarv.Context) error {
		auth := c.Header("Authorization")
		requestID := c.Header("X-Request-ID")
		userAgent := c.Header("User-Agent")
		if auth == "" {
			return c.JSON(401, map[string]string{"error": "unauthorized"})
		}
		c.SetHeader("X-Request-ID", requestID)
		return c.JSON(200, map[string]string{
			"auth": "valid", "request_id": requestID, "user_agent": userAgent,
		})
	})
	return app
}

func newMach_Headers() http.Handler {
	app := mach.New()
	app.GET("/api/protected", func(c *mach.Context) {
		auth := c.GetHeader("Authorization")
		requestID := c.GetHeader("X-Request-ID")
		userAgent := c.GetHeader("User-Agent")
		if auth == "" {
			c.JSON(401, map[string]string{"error": "unauthorized"})
			return
		}
		c.SetHeader("X-Request-ID", requestID)
		c.JSON(200, map[string]string{
			"auth": "valid", "request_id": requestID, "user_agent": userAgent,
		})
	})
	return app
}

func newGin_Headers() http.Handler {
	r := gin.New()
	r.GET("/api/protected", func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		requestID := c.GetHeader("X-Request-ID")
		userAgent := c.GetHeader("User-Agent")
		if auth == "" {
			c.JSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.Header("X-Request-ID", requestID)
		c.JSON(200, gin.H{
			"auth": "valid", "request_id": requestID, "user_agent": userAgent,
		})
	})
	return r
}

func newFiber_Headers() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/api/protected", func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		requestID := c.Get("X-Request-ID")
		userAgent := c.Get("User-Agent")
		if auth == "" {
			return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
		}
		c.Set("X-Request-ID", requestID)
		return c.JSON(fiber.Map{
			"auth": "valid", "request_id": requestID, "user_agent": userAgent,
		})
	})
	return app
}

func newEcho_Headers() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/api/protected", func(c echo.Context) error {
		auth := c.Request().Header.Get("Authorization")
		requestID := c.Request().Header.Get("X-Request-ID")
		userAgent := c.Request().Header.Get("User-Agent")
		if auth == "" {
			return c.JSON(401, map[string]string{"error": "unauthorized"})
		}
		c.Response().Header().Set("X-Request-ID", requestID)
		return c.JSON(200, map[string]string{
			"auth": "valid", "request_id": requestID, "user_agent": userAgent,
		})
	})
	return e
}

// ========== Real-World Scenario: Large JSON List Response ==========

func newAarv_LargeJSON() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Get("/api/users/list", func(c *aarv.Context) error {
		return c.JSON(200, sampleUserList)
	})
	return app
}

func newMach_LargeJSON() http.Handler {
	app := mach.New()
	app.GET("/api/users/list", func(c *mach.Context) {
		c.JSON(200, sampleUserList)
	})
	return app
}

func newGin_LargeJSON() http.Handler {
	r := gin.New()
	r.GET("/api/users/list", func(c *gin.Context) {
		c.JSON(200, sampleUserList)
	})
	return r
}

func newFiber_LargeJSON() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/api/users/list", func(c *fiber.Ctx) error {
		return c.JSON(sampleUserList)
	})
	return app
}

func newEcho_LargeJSON() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/api/users/list", func(c echo.Context) error { return c.JSON(200, sampleUserList) })
	return e
}

// ========== Go Benchmark functions (allocs, B/op, ns/op) ==========

// --- Static ---
func BenchmarkStatic_RawHTTP(b *testing.B) { benchHTTP(b, newRawHTTP_Static(), "GET", "/hello", nil) }
func BenchmarkStatic_Aarv(b *testing.B)    { benchHTTP(b, newAarv_Static(), "GET", "/hello", nil) }
func BenchmarkStatic_Mach(b *testing.B)    { benchHTTP(b, newMach_Static(), "GET", "/hello", nil) }
func BenchmarkStatic_Gin(b *testing.B)     { benchHTTP(b, newGin_Static(), "GET", "/hello", nil) }
func BenchmarkStatic_Echo(b *testing.B)    { benchHTTP(b, newEcho_Static(), "GET", "/hello", nil) }
func BenchmarkStatic_Fiber(b *testing.B)   { benchFiber(b, newFiber_Static(), "GET", "/hello", nil) }

// --- JSON ---
func BenchmarkJSON_Aarv(b *testing.B)  { benchHTTP(b, newAarv_JSON(), "GET", "/json", nil) }
func BenchmarkJSON_Mach(b *testing.B)  { benchHTTP(b, newMach_JSON(), "GET", "/json", nil) }
func BenchmarkJSON_Gin(b *testing.B)   { benchHTTP(b, newGin_JSON(), "GET", "/json", nil) }
func BenchmarkJSON_Echo(b *testing.B)  { benchHTTP(b, newEcho_JSON(), "GET", "/json", nil) }
func BenchmarkJSON_Fiber(b *testing.B) { benchFiber(b, newFiber_JSON(), "GET", "/json", nil) }

// --- Param ---
func BenchmarkParam_Aarv(b *testing.B)  { benchHTTP(b, newAarv_Param(), "GET", "/users/123", nil) }
func BenchmarkParam_Mach(b *testing.B)  { benchHTTP(b, newMach_Param(), "GET", "/users/123", nil) }
func BenchmarkParam_Gin(b *testing.B)   { benchHTTP(b, newGin_Param(), "GET", "/users/123", nil) }
func BenchmarkParam_Echo(b *testing.B)  { benchHTTP(b, newEcho_Param(), "GET", "/users/123", nil) }
func BenchmarkParam_Fiber(b *testing.B) { benchFiber(b, newFiber_Param(), "GET", "/users/123", nil) }

// --- Bind ---
var jsonBody = []byte(`{"name":"alice","email":"alice@test.com"}`)

func BenchmarkBind_Aarv(b *testing.B)  { benchHTTP(b, newAarv_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBind_Mach(b *testing.B)  { benchHTTP(b, newMach_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBind_Gin(b *testing.B)   { benchHTTP(b, newGin_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBind_Echo(b *testing.B)  { benchHTTP(b, newEcho_Bind(), "POST", "/users", jsonBody) }
func BenchmarkBind_Fiber(b *testing.B) { benchFiber(b, newFiber_Bind(), "POST", "/users", jsonBody) }

// --- Light net/http variants (reduced httptest noise) ---
func BenchmarkStaticLight_Aarv(b *testing.B) {
	benchHTTPLight(b, newAarv_Static(), "GET", "/hello", nil)
}
func BenchmarkStaticLight_Mach(b *testing.B) {
	benchHTTPLight(b, newMach_Static(), "GET", "/hello", nil)
}
func BenchmarkStaticLight_Gin(b *testing.B)  { benchHTTPLight(b, newGin_Static(), "GET", "/hello", nil) }
func BenchmarkStaticLight_Echo(b *testing.B) { benchHTTPLight(b, newEcho_Static(), "GET", "/hello", nil) }

func BenchmarkJSONLight_Aarv(b *testing.B)  { benchHTTPLight(b, newAarv_JSON(), "GET", "/json", nil) }
func BenchmarkJSONLight_Mach(b *testing.B)  { benchHTTPLight(b, newMach_JSON(), "GET", "/json", nil) }
func BenchmarkJSONLight_Gin(b *testing.B)   { benchHTTPLight(b, newGin_JSON(), "GET", "/json", nil) }
func BenchmarkJSONLight_Echo(b *testing.B)  { benchHTTPLight(b, newEcho_JSON(), "GET", "/json", nil) }

func BenchmarkParamLight_Aarv(b *testing.B) {
	benchHTTPLight(b, newAarv_Param(), "GET", "/users/123", nil)
}
func BenchmarkParamLight_Mach(b *testing.B) {
	benchHTTPLight(b, newMach_Param(), "GET", "/users/123", nil)
}
func BenchmarkParamLight_Gin(b *testing.B) {
	benchHTTPLight(b, newGin_Param(), "GET", "/users/123", nil)
}
func BenchmarkParamLight_Echo(b *testing.B) {
	benchHTTPLight(b, newEcho_Param(), "GET", "/users/123", nil)
}

func BenchmarkBindLight_Aarv(b *testing.B) {
	benchHTTPLight(b, newAarv_Bind(), "POST", "/users", jsonBody)
}
func BenchmarkBindLight_AarvSegmentio(b *testing.B) {
	benchHTTPLight(b, newAarv_BindWithCodec(segmentiocodec.New()), "POST", "/users", jsonBody)
}
func BenchmarkBindLight_AarvJSONv2(b *testing.B) {
	benchHTTPLight(b, newAarv_BindWithCodec(jsonv2codec.New()), "POST", "/users", jsonBody)
}
func BenchmarkBindLight_AarvSonic(b *testing.B) {
	benchHTTPLight(b, newAarv_BindWithCodec(soniccodec.New()), "POST", "/users", jsonBody)
}
func BenchmarkBindLight_AarvSonicFastest(b *testing.B) {
	benchHTTPLight(b, newAarv_BindWithCodec(soniccodec.NewFastest()), "POST", "/users", jsonBody)
}
func BenchmarkBindLight_Mach(b *testing.B) {
	benchHTTPLight(b, newMach_Bind(), "POST", "/users", jsonBody)
}
func BenchmarkBindLight_Gin(b *testing.B) {
	benchHTTPLight(b, newGin_Bind(), "POST", "/users", jsonBody)
}
func BenchmarkBindLight_Echo(b *testing.B) {
	benchHTTPLight(b, newEcho_Bind(), "POST", "/users", jsonBody)
}

// --- Parallel variants ---
func BenchmarkStaticParallel_Aarv(b *testing.B) {
	benchHTTPParallel(b, newAarv_Static(), "GET", "/hello", nil)
}
func BenchmarkStaticParallel_Mach(b *testing.B) {
	benchHTTPParallel(b, newMach_Static(), "GET", "/hello", nil)
}
func BenchmarkStaticParallel_Gin(b *testing.B) {
	benchHTTPParallel(b, newGin_Static(), "GET", "/hello", nil)
}
func BenchmarkStaticParallel_Echo(b *testing.B) {
	benchHTTPParallel(b, newEcho_Static(), "GET", "/hello", nil)
}
func BenchmarkStaticParallel_Fiber(b *testing.B) {
	benchFiberParallel(b, newFiber_Static(), "GET", "/hello", nil)
}

func BenchmarkBindParallel_Aarv(b *testing.B) {
	benchHTTPParallel(b, newAarv_Bind(), "POST", "/users", jsonBody)
}
func BenchmarkBindParallel_Mach(b *testing.B) {
	benchHTTPParallel(b, newMach_Bind(), "POST", "/users", jsonBody)
}
func BenchmarkBindParallel_Gin(b *testing.B) {
	benchHTTPParallel(b, newGin_Bind(), "POST", "/users", jsonBody)
}
func BenchmarkBindParallel_Echo(b *testing.B) {
	benchHTTPParallel(b, newEcho_Bind(), "POST", "/users", jsonBody)
}
func BenchmarkBindParallel_Fiber(b *testing.B) {
	benchFiberParallel(b, newFiber_Bind(), "POST", "/users", jsonBody)
}

// ========== Encryption Benchmarks ==========

// Shared encryption key for all frameworks
var encryptionKey = func() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}()

// AES-GCM encryptor for non-Aarv frameworks (with excluded paths/types for fair comparison)
type aesGCMEncryptor struct {
	enc              *encrypt.Encryptor
	excludedPaths    map[string]struct{}
	excludedPrefixes []string
}

func newAESGCMEncryptor(key []byte) *aesGCMEncryptor {
	enc, _ := encrypt.NewEncryptor(key)
	cfg := encrypt.DefaultConfig()
	excludedTypes := cfg.ExcludedTypes
	return &aesGCMEncryptor{
		enc:              enc,
		excludedPaths:    map[string]struct{}{},
		excludedPrefixes: append([]string(nil), excludedTypes...),
	}
}

func (e *aesGCMEncryptor) Encrypt(plaintext []byte) []byte {
	encoded, _ := e.enc.Encrypt(plaintext)
	return encoded
}

func (e *aesGCMEncryptor) Decrypt(encoded []byte) ([]byte, error) {
	return e.enc.Decrypt(encoded)
}

func (e *aesGCMEncryptor) isExcludedPath(path string) bool {
	_, ok := e.excludedPaths[path]
	return ok
}

func (e *aesGCMEncryptor) isExcludedType(contentType string) bool {
	if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	for _, prefix := range e.excludedPrefixes {
		if strings.HasPrefix(contentType, prefix) {
			return true
		}
	}
	return false
}

// Encryption response writer for Gin/Mach (with content-type checking for fair comparison)
type encryptingResponseWriter struct {
	http.ResponseWriter
	enc         *aesGCMEncryptor
	buf         bytes.Buffer
	skipEncrypt bool
}

func (w *encryptingResponseWriter) Write(b []byte) (int, error) {
	// Check content type on first write (same as Aarv)
	if !w.skipEncrypt {
		ct := w.ResponseWriter.Header().Get("Content-Type")
		if ct != "" && w.enc.isExcludedType(ct) {
			w.skipEncrypt = true
		}
	}
	if w.skipEncrypt {
		return w.ResponseWriter.Write(b)
	}
	return w.buf.Write(b)
}

func (w *encryptingResponseWriter) finish() {
	if w.skipEncrypt || w.buf.Len() == 0 {
		return
	}
	encrypted := w.enc.Encrypt(w.buf.Bytes())
	w.ResponseWriter.Header().Set("Content-Type", encrypt.EncryptedContentType)
	w.ResponseWriter.Header().Del("Content-Length")
	w.ResponseWriter.Write(encrypted)
}

func newAarv_Encrypt() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	encMiddleware, _ := encrypt.New(encryptionKey)
	app.Use(encMiddleware)
	app.Get("/api/users", func(c *aarv.Context) error {
		return c.JSON(200, sampleUser)
	})
	return app
}

func newMach_Encrypt() http.Handler {
	enc := newAESGCMEncryptor(encryptionKey)
	app := mach.New()
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check excluded paths (same as Aarv)
			if enc.isExcludedPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			ew := &encryptingResponseWriter{ResponseWriter: w, enc: enc}
			next.ServeHTTP(ew, r)
			ew.finish()
		})
	})
	app.GET("/api/users", func(c *mach.Context) {
		c.JSON(200, sampleUser)
	})
	return app
}

func newGin_Encrypt() http.Handler {
	enc := newAESGCMEncryptor(encryptionKey)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Check excluded paths (same as Aarv)
		if enc.isExcludedPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		ew := &encryptingResponseWriter{ResponseWriter: c.Writer, enc: enc}
		c.Writer = &ginEncryptWriter{ew, c.Writer}
		c.Next()
		ew.finish()
	})
	r.GET("/api/users", func(c *gin.Context) {
		c.JSON(200, sampleUser)
	})
	return r
}

// Gin requires a special wrapper
type ginEncryptWriter struct {
	*encryptingResponseWriter
	gin.ResponseWriter
}

func (w *ginEncryptWriter) Write(b []byte) (int, error) {
	return w.encryptingResponseWriter.Write(b)
}

func newFiber_Encrypt() *fiber.App {
	enc := newAESGCMEncryptor(encryptionKey)
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(func(c *fiber.Ctx) error {
		// Check excluded paths (same as Aarv)
		if enc.isExcludedPath(c.Path()) {
			return c.Next()
		}
		err := c.Next()
		if err == nil && len(c.Response().Body()) > 0 {
			// Check excluded content types (same as Aarv)
			ct := string(c.Response().Header.ContentType())
			if enc.isExcludedType(ct) {
				return nil
			}
			encrypted := enc.Encrypt(c.Response().Body())
			c.Response().Header.Set("Content-Type", "application/encrypted")
			c.Response().Header.Del("Content-Length")
			c.Response().SetBody(encrypted)
		}
		return err
	})
	app.Get("/api/users", func(c *fiber.Ctx) error {
		return c.JSON(sampleUser)
	})
	return app
}

func newEcho_Encrypt() http.Handler {
	enc := newAESGCMEncryptor(encryptionKey)
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if enc.isExcludedPath(c.Request().URL.Path) {
				return next(c)
			}
			ew := &encryptingResponseWriter{ResponseWriter: c.Response().Writer, enc: enc}
			c.Response().Writer = &echoEncryptWriter{encryptingResponseWriter: ew, ResponseWriter: c.Response().Writer}
			err := next(c)
			ew.finish()
			return err
		}
	})
	e.GET("/api/users", func(c echo.Context) error { return c.JSON(200, sampleUser) })
	return e
}

// --- Encryption Benchmarks ---
func BenchmarkEncrypt_Aarv(b *testing.B) { benchHTTP(b, newAarv_Encrypt(), "GET", "/api/users", nil) }
func BenchmarkEncrypt_Mach(b *testing.B) { benchHTTP(b, newMach_Encrypt(), "GET", "/api/users", nil) }
func BenchmarkEncrypt_Gin(b *testing.B)  { benchHTTP(b, newGin_Encrypt(), "GET", "/api/users", nil) }
func BenchmarkEncrypt_Echo(b *testing.B) { benchHTTP(b, newEcho_Encrypt(), "GET", "/api/users", nil) }
func BenchmarkEncrypt_Fiber(b *testing.B) {
	benchFiber(b, newFiber_Encrypt(), "GET", "/api/users", nil)
}

// ========== Logging Benchmarks ==========

// Discard logger for fair comparison
var discardLogger = slog.New(slog.NewJSONHandler(io.Discard, nil))

func init() {
	slog.SetDefault(discardLogger)
}

// statusCapturingWriter captures status code and bytes written (same as Aarv's logger)
type statusCapturingWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
	written      bool
}

func newStatusCapturingWriter(w http.ResponseWriter) *statusCapturingWriter {
	return &statusCapturingWriter{ResponseWriter: w, statusCode: 200}
}

func (w *statusCapturingWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusCapturingWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.written = true
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += int64(n)
	return n, err
}

// Logging middleware for other frameworks (same fields and logic as Aarv's logger)
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := newStatusCapturingWriter(w) // Same allocation pattern as Aarv
		next.ServeHTTP(sw, r)

		// Get request ID (same pattern as Aarv - check context)
		requestID := ""
		if c, ok := aarv.FromRequest(r); ok {
			requestID = c.RequestID()
		}

		// Log same fields as Aarv
		slog.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.statusCode),
			slog.Duration("latency", time.Since(start)),
			slog.String("client_ip", clientIPFromRequest(r)),
			slog.String("user_agent", r.UserAgent()),
			slog.Int64("bytes_out", sw.bytesWritten),
			slog.String("request_id", requestID),
		)
	})
}

// clientIPFromRequest extracts client IP (same logic as Aarv's logger)
func clientIPFromRequest(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return xff
	}
	addr := r.RemoteAddr
	if addr == "" {
		return ""
	}
	if i := strings.LastIndexByte(addr, ':'); i > 0 && strings.IndexByte(addr, ']') == -1 {
		return addr[:i]
	}
	return addr
}

// Sensitive headers and fields for redaction (same as Aarv's verboselog)
var sensitiveHeaders = map[string]struct{}{
	"authorization": {},
	"cookie":        {},
	"set-cookie":    {},
	"x-api-key":     {},
	"x-auth-token":  {},
}

var sensitiveFields = []string{"password", "token", "secret", "api_key", "apikey"}

// redactSensitiveFields replaces sensitive field values (same algorithm as Aarv)
func redactSensitiveFields(body string) string {
	for _, field := range sensitiveFields {
		patterns := []string{
			`"` + field + `":"`,
			`"` + field + `": "`,
			`"` + field + `" : "`,
		}
		for _, pattern := range patterns {
			if idx := strings.Index(strings.ToLower(body), strings.ToLower(pattern)); idx >= 0 {
				start := idx + len(pattern)
				end := strings.Index(body[start:], `"`)
				if end > 0 {
					body = body[:start] + "[REDACTED]" + body[start+end:]
				}
			}
		}
	}
	return body
}

// bodyCapturingWriter captures response body for logging (same as Aarv)
type bodyCapturingWriter struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (w *bodyCapturingWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *bodyCapturingWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// Verbose logging middleware for other frameworks (with response capture + redaction like Aarv)
func verboseLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var reqBody []byte
		if r.Body != nil && r.ContentLength > 0 {
			reqBody, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(reqBody))
		}
		// Build headers map with sensitive header redaction
		headers := make(map[string]string)
		for k, v := range r.Header {
			if _, sensitive := sensitiveHeaders[strings.ToLower(k)]; sensitive {
				headers[k] = "[REDACTED]"
			} else {
				headers[k] = strings.Join(v, ", ")
			}
		}
		// Capture response body
		bw := &bodyCapturingWriter{ResponseWriter: w, body: &bytes.Buffer{}, statusCode: 200}
		next.ServeHTTP(bw, r)

		// Redact sensitive fields in bodies
		reqBodyStr := redactSensitiveFields(string(reqBody))
		respBodyStr := redactSensitiveFields(bw.body.String())

		slog.Info("http_dump",
			"method", r.Method,
			"path", r.URL.Path,
			"request_headers", headers,
			"request_body", reqBodyStr,
			"response_body", respBodyStr,
			"status", bw.statusCode,
			"latency", time.Since(start).String(),
		)
	})
}

func newAarv_Logger() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(logger.New())
	app.Post("/api/users", func(c *aarv.Context) error {
		return c.JSON(200, sampleUser)
	})
	return app
}

func newMach_Logger() http.Handler {
	app := mach.New()
	app.Use(loggingMiddleware)
	app.POST("/api/users", func(c *mach.Context) {
		c.JSON(200, sampleUser)
	})
	return app
}

func newGin_Logger() http.Handler {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		start := time.Now()
		c.Next()
		// Get request ID (same pattern as Aarv)
		requestID := ""
		if ctx, ok := aarv.FromRequest(c.Request); ok {
			requestID = ctx.RequestID()
		}
		// Log same fields as Aarv
		slog.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency", time.Since(start).String(),
			"client_ip", clientIPFromRequest(c.Request),
			"user_agent", c.Request.UserAgent(),
			"bytes_out", c.Writer.Size(),
			"request_id", requestID,
		)
	})
	r.POST("/api/users", func(c *gin.Context) {
		c.JSON(200, sampleUser)
	})
	return r
}

func newFiber_Logger() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		// Fiber doesn't have aarv context, so just use empty (fair since Fiber won't find it either)
		requestID := ""
		// Log same fields as Aarv
		slog.Info("request",
			"method", c.Method(),
			"path", c.Path(),
			"status", c.Response().StatusCode(),
			"latency", time.Since(start).String(),
			"client_ip", c.IP(),
			"user_agent", c.Get("User-Agent"),
			"bytes_out", len(c.Response().Body()),
			"request_id", requestID,
		)
		return err
	})
	app.Post("/api/users", func(c *fiber.Ctx) error {
		return c.JSON(sampleUser)
	})
	return app
}

func newEcho_Logger() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			sw := newStatusCapturingWriter(c.Response().Writer)
			c.Response().Writer = sw
			err := next(c)
			requestID := ""
			if ctx, ok := aarv.FromRequest(c.Request()); ok {
				requestID = ctx.RequestID()
			}
			slog.Info("request",
				"method", c.Request().Method,
				"path", c.Request().URL.Path,
				"status", sw.statusCode,
				"latency", time.Since(start).String(),
				"client_ip", clientIPFromRequest(c.Request()),
				"user_agent", c.Request().UserAgent(),
				"bytes_out", sw.bytesWritten,
				"request_id", requestID,
			)
			return err
		}
	})
	e.POST("/api/users", func(c echo.Context) error { return c.JSON(200, sampleUser) })
	return e
}

// --- Standard Logger Benchmarks ---
func BenchmarkLogger_Aarv(b *testing.B) {
	benchHTTP(b, newAarv_Logger(), "POST", "/api/users", jsonBody)
}
func BenchmarkLogger_Mach(b *testing.B) {
	benchHTTP(b, newMach_Logger(), "POST", "/api/users", jsonBody)
}
func BenchmarkLogger_Gin(b *testing.B)  { benchHTTP(b, newGin_Logger(), "POST", "/api/users", jsonBody) }
func BenchmarkLogger_Echo(b *testing.B) { benchHTTP(b, newEcho_Logger(), "POST", "/api/users", jsonBody) }
func BenchmarkLogger_Fiber(b *testing.B) {
	benchFiber(b, newFiber_Logger(), "POST", "/api/users", jsonBody)
}

var loggerIsolatedResponse = []byte(`{"ok":true,"message":"logger benchmark"}`)

func newLoggerTerminalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(loggerIsolatedResponse)
	})
}

func newAarv_LoggerIsolated() http.Handler {
	return logger.New()(newLoggerTerminalHandler())
}

func newEquivalent_LoggerIsolated() http.Handler {
	return loggingMiddleware(newLoggerTerminalHandler())
}

func benchLoggerIsolated(b *testing.B, handler http.Handler) {
	var w benchutil.DiscardResponseWriter

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newLightRequest("GET", "/logger-only", nil)
		req.RemoteAddr = "192.0.2.10:1234"
		req.Header.Set("User-Agent", "bench/1.0")
		w.Reset()
		handler.ServeHTTP(&w, req)
	}
}

func BenchmarkLoggerIsolated_Aarv(b *testing.B) {
	benchLoggerIsolated(b, newAarv_LoggerIsolated())
}

func BenchmarkLoggerIsolated_Equivalent(b *testing.B) {
	benchLoggerIsolated(b, newEquivalent_LoggerIsolated())
}

var encryptIsolatedResponse = []byte(`{"ok":true,"message":"encrypt benchmark"}`)

func newEncryptTerminalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encryptIsolatedResponse)
	})
}

func newAarv_EncryptIsolated() http.Handler {
	m, _ := encrypt.New(encryptionKey)
	return m(newEncryptTerminalHandler())
}

func newEquivalent_EncryptIsolated() http.Handler {
	enc := newAESGCMEncryptor(encryptionKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if enc.isExcludedPath(r.URL.Path) {
			newEncryptTerminalHandler().ServeHTTP(w, r)
			return
		}
		ew := &encryptingResponseWriter{ResponseWriter: w, enc: enc}
		newEncryptTerminalHandler().ServeHTTP(ew, r)
		ew.finish()
	})
}

func benchEncryptIsolated(b *testing.B, handler http.Handler) {
	var w benchutil.DiscardResponseWriter

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newLightRequest("GET", "/encrypt-only", nil)
		w.Reset()
		handler.ServeHTTP(&w, req)
	}
}

func BenchmarkEncryptIsolated_Aarv(b *testing.B) {
	benchEncryptIsolated(b, newAarv_EncryptIsolated())
}

func BenchmarkEncryptIsolated_Equivalent(b *testing.B) {
	benchEncryptIsolated(b, newEquivalent_EncryptIsolated())
}

// --- Verbose Logger Benchmarks ---

func newAarv_VerboseLog() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(verboselog.New())
	app.Post("/api/users", func(c *aarv.Context) error {
		return c.JSON(200, sampleUser)
	})
	return app
}

func newMach_VerboseLog() http.Handler {
	app := mach.New()
	app.Use(verboseLoggingMiddleware)
	app.POST("/api/users", func(c *mach.Context) {
		c.JSON(200, sampleUser)
	})
	return app
}

func newGin_VerboseLog() http.Handler {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		start := time.Now()
		var reqBody []byte
		if c.Request.Body != nil && c.Request.ContentLength > 0 {
			reqBody, _ = io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewReader(reqBody))
		}
		// Headers with sensitive redaction
		headers := make(map[string]string)
		for k, v := range c.Request.Header {
			if _, sensitive := sensitiveHeaders[strings.ToLower(k)]; sensitive {
				headers[k] = "[REDACTED]"
			} else {
				headers[k] = strings.Join(v, ", ")
			}
		}
		// Capture response body using gin's blw
		blw := &ginVerboseWriter{body: &bytes.Buffer{}, ResponseWriter: c.Writer}
		c.Writer = blw
		c.Next()

		// Redact sensitive fields
		reqBodyStr := redactSensitiveFields(string(reqBody))
		respBodyStr := redactSensitiveFields(blw.body.String())

		slog.Info("http_dump",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"request_headers", headers,
			"request_body", reqBodyStr,
			"response_body", respBodyStr,
			"status", c.Writer.Status(),
			"latency", time.Since(start).String(),
		)
	})
	r.POST("/api/users", func(c *gin.Context) {
		c.JSON(200, sampleUser)
	})
	return r
}

// ginVerboseWriter wraps gin.ResponseWriter to capture body
type ginVerboseWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *ginVerboseWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func newFiber_VerboseLog() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		reqBody := c.Body()
		// Headers with sensitive redaction
		headers := make(map[string]string)
		c.Request().Header.VisitAll(func(k, v []byte) {
			key := string(k)
			if _, sensitive := sensitiveHeaders[strings.ToLower(key)]; sensitive {
				headers[key] = "[REDACTED]"
			} else {
				headers[key] = string(v)
			}
		})
		err := c.Next()

		// Capture response body and redact sensitive fields
		reqBodyStr := redactSensitiveFields(string(reqBody))
		respBodyStr := redactSensitiveFields(string(c.Response().Body()))

		slog.Info("http_dump",
			"method", c.Method(),
			"path", c.Path(),
			"request_headers", headers,
			"request_body", reqBodyStr,
			"response_body", respBodyStr,
			"status", c.Response().StatusCode(),
			"latency", time.Since(start).String(),
		)
		return err
	})
	app.Post("/api/users", func(c *fiber.Ctx) error {
		return c.JSON(sampleUser)
	})
	return app
}

func newEcho_VerboseLog() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			var reqBody []byte
			if c.Request().Body != nil && c.Request().ContentLength > 0 {
				reqBody, _ = io.ReadAll(c.Request().Body)
				c.Request().Body = io.NopCloser(bytes.NewReader(reqBody))
			}
			headers := make(map[string]string)
			for k, v := range c.Request().Header {
				if _, sensitive := sensitiveHeaders[strings.ToLower(k)]; sensitive {
					headers[k] = "[REDACTED]"
				} else {
					headers[k] = strings.Join(v, ", ")
				}
			}
			bw := &bodyCapturingWriter{ResponseWriter: c.Response().Writer, body: &bytes.Buffer{}, statusCode: 200}
			c.Response().Writer = bw
			err := next(c)
			reqBodyStr := redactSensitiveFields(string(reqBody))
			respBodyStr := redactSensitiveFields(bw.body.String())
			slog.Info("http_dump",
				"method", c.Request().Method,
				"path", c.Request().URL.Path,
				"request_headers", headers,
				"request_body", reqBodyStr,
				"response_body", respBodyStr,
				"status", bw.statusCode,
				"latency", time.Since(start).String(),
			)
			return err
		}
	})
	e.POST("/api/users", func(c echo.Context) error { return c.JSON(200, sampleUser) })
	return e
}

func BenchmarkVerboseLog_Aarv(b *testing.B) {
	benchHTTP(b, newAarv_VerboseLog(), "POST", "/api/users", jsonBody)
}
func BenchmarkVerboseLog_Mach(b *testing.B) {
	benchHTTP(b, newMach_VerboseLog(), "POST", "/api/users", jsonBody)
}
func BenchmarkVerboseLog_Gin(b *testing.B) {
	benchHTTP(b, newGin_VerboseLog(), "POST", "/api/users", jsonBody)
}
func BenchmarkVerboseLog_Echo(b *testing.B) {
	benchHTTP(b, newEcho_VerboseLog(), "POST", "/api/users", jsonBody)
}
func BenchmarkVerboseLog_Fiber(b *testing.B) {
	benchFiber(b, newFiber_VerboseLog(), "POST", "/api/users", jsonBody)
}

// --- Verboselog with Minimal Config (Aarv only, for showing config impact) ---
func newAarv_VerboseLogMinimal() http.Handler {
	app := aarv.New(aarv.WithBanner(false))
	app.Use(verboselog.New(verboselog.MinimalConfig()))
	app.Post("/api/users", func(c *aarv.Context) error {
		return c.JSON(200, sampleUser)
	})
	return app
}

func BenchmarkVerboseLogMinimal_Aarv(b *testing.B) {
	benchHTTP(b, newAarv_VerboseLogMinimal(), "POST", "/api/users", jsonBody)
}

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

func benchHTTPLight(b *testing.B, handler http.Handler, method, path string, body []byte) {
	var w benchutil.DiscardResponseWriter

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := newLightRequest(method, path, body)
		w.Reset()
		handler.ServeHTTP(&w, req)
	}
}

func newLightRequest(method, path string, body []byte) *http.Request {
	req := &http.Request{
		Method: method,
		URL:    &url.URL{Path: path},
		Header: make(http.Header, 1),
	}
	if body != nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/json")
	}
	return req
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
	gcCycles  uint32
	gcPauseNs uint64 // total GC STW pause
	heapInUse uint64 // heap bytes in use at end

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
		// ========== Real-World Scenarios ==========
		{
			label: "Middleware Stack (Recovery + Noop + JSON Response)",
			tests: []testFn{
				{"Aarv", func() loadResult { return runTCPLoad("Aarv", newAarv_Middleware(), "GET", "/api/users", nil) }},
				{"AarvFast", func() loadResult { return runTCPLoad("AarvFast", newAarv_MiddlewareFastMode(), "GET", "/api/users", nil) }},
				{"Mach", func() loadResult { return runTCPLoad("Mach", newMach_Middleware(), "GET", "/api/users", nil) }},
				{"Gin", func() loadResult { return runTCPLoad("Gin", newGin_Middleware(), "GET", "/api/users", nil) }},
				{"Fiber", func() loadResult { return runFiberTCPLoad("Fiber", newFiber_Middleware(), "GET", "/api/users", nil) }},
			},
		},
		{
			label: "Query Params (4 params + JSON list response)",
			tests: []testFn{
				{"Aarv", func() loadResult {
					return runTCPLoad("Aarv", newAarv_Query(), "GET", "/api/users?page=2&limit=20&sort=name&order=asc", nil)
				}},
				{"Mach", func() loadResult {
					return runTCPLoad("Mach", newMach_Query(), "GET", "/api/users?page=2&limit=20&sort=name&order=asc", nil)
				}},
				{"Gin", func() loadResult {
					return runTCPLoad("Gin", newGin_Query(), "GET", "/api/users?page=2&limit=20&sort=name&order=asc", nil)
				}},
				{"Fiber", func() loadResult {
					return runFiberTCPLoad("Fiber", newFiber_Query(), "GET", "/api/users?page=2&limit=20&sort=name&order=asc", nil)
				}},
			},
		},
		{
			label: "Multi Path Params (3 params: org/team/member)",
			tests: []testFn{
				{"Aarv", func() loadResult {
					return runTCPLoad("Aarv", newAarv_MultiParam(), "GET", "/api/orgs/org123/teams/team456/members/mem789", nil)
				}},
				{"Mach", func() loadResult {
					return runTCPLoad("Mach", newMach_MultiParam(), "GET", "/api/orgs/org123/teams/team456/members/mem789", nil)
				}},
				{"Gin", func() loadResult {
					return runTCPLoad("Gin", newGin_MultiParam(), "GET", "/api/orgs/org123/teams/team456/members/mem789", nil)
				}},
				{"Fiber", func() loadResult {
					return runFiberTCPLoad("Fiber", newFiber_MultiParam(), "GET", "/api/orgs/org123/teams/team456/members/mem789", nil)
				}},
			},
		},
		{
			label: "Headers + Auth Check (read 3 headers, write 1)",
			tests: []testFn{
				{"Aarv", func() loadResult { return runTCPLoadWithHeaders("Aarv", newAarv_Headers(), "GET", "/api/protected") }},
				{"Mach", func() loadResult { return runTCPLoadWithHeaders("Mach", newMach_Headers(), "GET", "/api/protected") }},
				{"Gin", func() loadResult { return runTCPLoadWithHeaders("Gin", newGin_Headers(), "GET", "/api/protected") }},
				{"Fiber", func() loadResult {
					return runFiberTCPLoadWithHeaders("Fiber", newFiber_Headers(), "GET", "/api/protected")
				}},
			},
		},
		{
			label: "Large JSON Response (5 users with nested address)",
			tests: []testFn{
				{"Aarv", func() loadResult { return runTCPLoad("Aarv", newAarv_LargeJSON(), "GET", "/api/users/list", nil) }},
				{"Mach", func() loadResult { return runTCPLoad("Mach", newMach_LargeJSON(), "GET", "/api/users/list", nil) }},
				{"Gin", func() loadResult { return runTCPLoad("Gin", newGin_LargeJSON(), "GET", "/api/users/list", nil) }},
				{"Fiber", func() loadResult {
					return runFiberTCPLoad("Fiber", newFiber_LargeJSON(), "GET", "/api/users/list", nil)
				}},
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
	fmt.Println("  - CPU% = process CPU time / (wall time x cores) — process utilization across all cores")
	fmt.Println("  - Fiber: fasthttp server (not net/http) — different transport layer")
	fmt.Println("  - Aarv Bind includes auto-validation (required, min, email)")
	fmt.Println("  - Gin Bind uses go-playground/validator")
	fmt.Println("  - Mach Bind uses DecodeJSON + go-playground/validator")
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
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	user := time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
	sys := time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond
	return user + sys
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

	// Warm up the server and connection pool so all frameworks start the
	// timed section under similar conditions.
	for i := 0; i < 20; i++ {
		var req *http.Request
		if body != nil {
			req, _ = http.NewRequest(method, url, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req, _ = http.NewRequest(method, url, nil)
		}
		resp, err := client.Do(req)
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

// runTCPLoadWithHeaders runs load test with custom headers (for auth-like scenarios)
func runTCPLoadWithHeaders(name string, handler http.Handler, method, path string) loadResult {
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
				req, _ := http.NewRequest(method, url, nil)
				req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test")
				req.Header.Set("X-Request-ID", "req-12345-abcde")
				req.Header.Set("User-Agent", "BenchmarkClient/1.0")
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

// runFiberTCPLoadWithHeaders runs Fiber load test with custom headers
func runFiberTCPLoadWithHeaders(name string, app *fiber.App, method, path string) loadResult {
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
		req, _ := http.NewRequest(method, url, nil)
		req.Header.Set("Authorization", "Bearer test")
		resp, err := client.Do(req)
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
				req, _ := http.NewRequest(method, url, nil)
				req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test")
				req.Header.Set("X-Request-ID", "req-12345-abcde")
				req.Header.Set("User-Agent", "BenchmarkClient/1.0")
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
