// Example: REST CRUD API with typed handlers, validation, path params,
// query binding, error handling, route groups, and middleware.
package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/health"
	"github.com/nilshah80/aarv/plugins/requestid"
)

// --- Domain types ---

type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Age       int    `json:"age"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

// --- Request / Response types ---

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
	Page     int    `query:"page"     default:"1"`
	PageSize int    `query:"page_size" default:"20"`
	Sort     string `query:"sort"     default:"created_at"`
	Order    string `query:"order"    default:"desc"`
}

type ListUsersRes struct {
	Users []User `json:"users"`
	Total int    `json:"total"`
	Page  int    `json:"page"`
}

// --- In-memory store ---

var (
	store   = make(map[string]User)
	storeMu sync.RWMutex
	nextID  int
)

func genID() string {
	nextID++
	return fmt.Sprintf("usr_%03d", nextID)
}

// --- Handlers ---

func createUser(c *aarv.Context, req CreateUserReq) (User, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	// Check duplicate email
	for _, u := range store {
		if u.Email == req.Email {
			return User{}, aarv.ErrConflict("email already registered")
		}
	}

	user := User{
		ID:        genID(),
		Name:      req.Name,
		Email:     req.Email,
		Age:       req.Age,
		Role:      req.Role,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	store[user.ID] = user

	c.Logger().Info("user created", "user_id", user.ID)
	return user, nil
}

func getUser(c *aarv.Context, req GetUserReq) error {
	storeMu.RLock()
	user, ok := store[req.ID]
	storeMu.RUnlock()

	if !ok {
		return aarv.ErrNotFound("user not found").WithDetail("id: " + req.ID)
	}

	return c.JSON(http.StatusOK, user)
}

func listUsers(c *aarv.Context, req ListUsersReq) (ListUsersRes, error) {
	storeMu.RLock()
	defer storeMu.RUnlock()

	users := make([]User, 0, len(store))
	for _, u := range store {
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

	return ListUsersRes{
		Users: users[start:end],
		Total: total,
		Page:  req.Page,
	}, nil
}

func updateUser(c *aarv.Context, req UpdateUserReq) (User, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	user, ok := store[req.ID]
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
	store[user.ID] = user
	return user, nil
}

func deleteUser(c *aarv.Context) error {
	id := c.Param("id")

	storeMu.Lock()
	defer storeMu.Unlock()

	if _, ok := store[id]; !ok {
		return aarv.ErrNotFound("user not found")
	}
	delete(store, id)

	return c.NoContent(http.StatusNoContent)
}

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
	)

	// Global middleware
	app.Use(
		aarv.Recovery(),
		requestid.New(),
		aarv.Logger(),
		health.New(),
	)

	// API v1 group
	app.Group("/api/v1", func(g *aarv.RouteGroup) {
		// Users CRUD
		g.Get("/users", aarv.Bind(listUsers))
		g.Post("/users", aarv.Bind(createUser))
		g.Get("/users/{id}", aarv.BindReq(getUser))
		g.Put("/users/{id}", aarv.Bind(updateUser))
		g.Delete("/users/{id}", deleteUser)

		// Misc endpoints
		g.Get("/ping", func(c *aarv.Context) error {
			return c.Text(http.StatusOK, "pong")
		})

		g.Get("/time", aarv.BindRes(func(c *aarv.Context) (map[string]string, error) {
			return map[string]string{
				"time":       time.Now().Format(time.RFC3339),
				"request_id": c.RequestID(),
			}, nil
		}))
	})

	fmt.Println("REST CRUD API on :8080")
	fmt.Println("  POST   /api/v1/users       — create user")
	fmt.Println("  GET    /api/v1/users        — list users (?page=1&page_size=20)")
	fmt.Println("  GET    /api/v1/users/{id}   — get user")
	fmt.Println("  PUT    /api/v1/users/{id}   — update user")
	fmt.Println("  DELETE /api/v1/users/{id}   — delete user")
	fmt.Println("  GET    /health              — health check")

	app.Listen(":8080")
}
