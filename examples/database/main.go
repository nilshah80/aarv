// Example: Database integration patterns — demonstrates common patterns
// for integrating databases with aarv, using an in-memory store that
// mimics real database patterns (connection pooling, transactions, etc.).
//
// In a real application, replace the InMemoryDB with your database driver:
// - PostgreSQL: database/sql + github.com/lib/pq
// - MySQL: database/sql + github.com/go-sql-driver/mysql
// - SQLite: database/sql + github.com/mattn/go-sqlite3
// - MongoDB: go.mongodb.org/mongo-driver
// - Redis: github.com/go-redis/redis
package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/health"
	"github.com/nilshah80/aarv/plugins/requestid"
)

// =============================================================================
// Database Interface (abstraction layer)
// =============================================================================

// DB defines the database interface. In production, implement this
// with your actual database driver.
type DB interface {
	// User operations
	CreateUser(ctx context.Context, user *User) error
	GetUser(ctx context.Context, id int) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	ListUsers(ctx context.Context, limit, offset int) ([]*User, int, error)
	UpdateUser(ctx context.Context, user *User) error
	DeleteUser(ctx context.Context, id int) error

	// Transaction support
	BeginTx(ctx context.Context) (Tx, error)

	// Health check
	Ping(ctx context.Context) error
	Close() error
}

// Tx represents a database transaction
type Tx interface {
	Commit() error
	Rollback() error
	CreateUser(ctx context.Context, user *User) error
	UpdateUser(ctx context.Context, user *User) error
}

// =============================================================================
// Domain Models
// =============================================================================

type User struct {
	ID        int       `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// =============================================================================
// In-Memory Database Implementation (for demo purposes)
// =============================================================================

type InMemoryDB struct {
	mu      sync.RWMutex
	users   map[int]*User
	emails  map[string]int // email -> id index
	nextID  int
	healthy bool
}

func NewInMemoryDB() *InMemoryDB {
	return &InMemoryDB{
		users:   make(map[int]*User),
		emails:  make(map[string]int),
		nextID:  1,
		healthy: true,
	}
}

func (db *InMemoryDB) CreateUser(ctx context.Context, user *User) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, exists := db.emails[user.Email]; exists {
		return fmt.Errorf("email already exists")
	}

	user.ID = db.nextID
	db.nextID++
	user.CreatedAt = time.Now()
	user.UpdatedAt = user.CreatedAt

	db.users[user.ID] = user
	db.emails[user.Email] = user.ID
	return nil
}

func (db *InMemoryDB) GetUser(ctx context.Context, id int) (*User, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	user, ok := db.users[id]
	if !ok {
		return nil, fmt.Errorf("user not found")
	}
	return user, nil
}

func (db *InMemoryDB) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	id, ok := db.emails[email]
	if !ok {
		return nil, fmt.Errorf("user not found")
	}
	return db.users[id], nil
}

func (db *InMemoryDB) ListUsers(ctx context.Context, limit, offset int) ([]*User, int, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	total := len(db.users)
	users := make([]*User, 0, limit)

	i := 0
	for _, user := range db.users {
		if i >= offset && len(users) < limit {
			users = append(users, user)
		}
		i++
	}

	return users, total, nil
}

func (db *InMemoryDB) UpdateUser(ctx context.Context, user *User) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	existing, ok := db.users[user.ID]
	if !ok {
		return fmt.Errorf("user not found")
	}

	// Handle email change
	if existing.Email != user.Email {
		if _, exists := db.emails[user.Email]; exists {
			return fmt.Errorf("email already exists")
		}
		delete(db.emails, existing.Email)
		db.emails[user.Email] = user.ID
	}

	user.UpdatedAt = time.Now()
	user.CreatedAt = existing.CreatedAt
	db.users[user.ID] = user
	return nil
}

func (db *InMemoryDB) DeleteUser(ctx context.Context, id int) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	user, ok := db.users[id]
	if !ok {
		return fmt.Errorf("user not found")
	}

	delete(db.emails, user.Email)
	delete(db.users, id)
	return nil
}

func (db *InMemoryDB) BeginTx(ctx context.Context) (Tx, error) {
	return &InMemoryTx{db: db}, nil
}

func (db *InMemoryDB) Ping(ctx context.Context) error {
	if !db.healthy {
		return fmt.Errorf("database unhealthy")
	}
	return nil
}

func (db *InMemoryDB) Close() error {
	db.healthy = false
	return nil
}

// InMemoryTx implements a simple transaction (not fully ACID for demo)
type InMemoryTx struct {
	db       *InMemoryDB
	ops      []func() error
	rollback []func()
}

func (tx *InMemoryTx) CreateUser(ctx context.Context, user *User) error {
	return tx.db.CreateUser(ctx, user)
}

func (tx *InMemoryTx) UpdateUser(ctx context.Context, user *User) error {
	return tx.db.UpdateUser(ctx, user)
}

func (tx *InMemoryTx) Commit() error {
	// In a real DB, this would commit the transaction
	return nil
}

func (tx *InMemoryTx) Rollback() error {
	// In a real DB, this would rollback the transaction
	return nil
}

// =============================================================================
// Repository Pattern (optional layer for complex queries)
// =============================================================================

type UserRepository struct {
	db DB
}

func NewUserRepository(db DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(ctx context.Context, user *User) error {
	return r.db.CreateUser(ctx, user)
}

func (r *UserRepository) FindByID(ctx context.Context, id int) (*User, error) {
	return r.db.GetUser(ctx, id)
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	return r.db.GetUserByEmail(ctx, email)
}

func (r *UserRepository) FindAll(ctx context.Context, limit, offset int) ([]*User, int, error) {
	return r.db.ListUsers(ctx, limit, offset)
}

func (r *UserRepository) Update(ctx context.Context, user *User) error {
	return r.db.UpdateUser(ctx, user)
}

func (r *UserRepository) Delete(ctx context.Context, id int) error {
	return r.db.DeleteUser(ctx, id)
}

// =============================================================================
// Request/Response Types
// =============================================================================

type CreateUserReq struct {
	Email string `json:"email" validate:"required,email"`
	Name  string `json:"name" validate:"required,min=2"`
}

type UpdateUserReq struct {
	ID    int    `param:"id"`
	Email string `json:"email" validate:"omitempty,email"`
	Name  string `json:"name" validate:"omitempty,min=2"`
}

type GetUserReq struct {
	ID int `param:"id"`
}

type ListUsersReq struct {
	Limit  int `query:"limit" default:"10"`
	Offset int `query:"offset" default:"0"`
}

type ListUsersRes struct {
	Users  []*User `json:"users"`
	Total  int     `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}

// =============================================================================
// Middleware: Database Context Injection
// =============================================================================

type dbKey struct{}

// DBMiddleware injects the database connection into the request context
func DBMiddleware(db DB) aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), dbKey{}, db)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetDB retrieves the database from context
func GetDB(c *aarv.Context) DB {
	db, _ := c.Request().Context().Value(dbKey{}).(DB)
	return db
}

// =============================================================================
// Main
// =============================================================================

func main() {
	// Initialize database
	db := NewInMemoryDB()
	defer db.Close()

	// Create repository
	userRepo := NewUserRepository(db)

	app := aarv.New(
		aarv.WithBanner(true),
	)

	app.Use(
		aarv.Recovery(),
		requestid.New(),
		aarv.Logger(),
		DBMiddleware(db), // Inject DB into all requests
		health.New(health.Config{
			ReadyCheck: func() bool {
				return db.Ping(context.Background()) == nil
			},
		}),
	)

	// API routes
	app.Group("/api/v1", func(g *aarv.RouteGroup) {
		// Create user
		g.Post("/users", aarv.Bind(func(c *aarv.Context, req CreateUserReq) (*User, error) {
			user := &User{
				Email: req.Email,
				Name:  req.Name,
			}

			if err := userRepo.Create(c.Request().Context(), user); err != nil {
				if err.Error() == "email already exists" {
					return nil, aarv.ErrConflict("email already exists")
				}
				return nil, aarv.ErrInternal(err)
			}

			c.Logger().Info("user created", "user_id", user.ID)
			return user, nil
		}))

		// List users
		g.Get("/users", aarv.Bind(func(c *aarv.Context, req ListUsersReq) (ListUsersRes, error) {
			users, total, err := userRepo.FindAll(c.Request().Context(), req.Limit, req.Offset)
			if err != nil {
				return ListUsersRes{}, aarv.ErrInternal(err)
			}

			return ListUsersRes{
				Users:  users,
				Total:  total,
				Limit:  req.Limit,
				Offset: req.Offset,
			}, nil
		}))

		// Get user by ID
		g.Get("/users/{id}", aarv.BindReq(func(c *aarv.Context, req GetUserReq) error {
			user, err := userRepo.FindByID(c.Request().Context(), req.ID)
			if err != nil {
				return aarv.ErrNotFound("user not found")
			}
			return c.JSON(http.StatusOK, user)
		}))

		// Update user
		g.Put("/users/{id}", aarv.Bind(func(c *aarv.Context, req UpdateUserReq) (*User, error) {
			ctx := c.Request().Context()

			// Get existing user
			user, err := userRepo.FindByID(ctx, req.ID)
			if err != nil {
				return nil, aarv.ErrNotFound("user not found")
			}

			// Apply updates
			if req.Email != "" {
				user.Email = req.Email
			}
			if req.Name != "" {
				user.Name = req.Name
			}

			if err := userRepo.Update(ctx, user); err != nil {
				if err.Error() == "email already exists" {
					return nil, aarv.ErrConflict("email already exists")
				}
				return nil, aarv.ErrInternal(err)
			}

			return user, nil
		}))

		// Delete user
		g.Delete("/users/{id}", aarv.BindReq(func(c *aarv.Context, req GetUserReq) error {
			if err := userRepo.Delete(c.Request().Context(), req.ID); err != nil {
				return aarv.ErrNotFound("user not found")
			}

			c.Logger().Info("user deleted", "user_id", req.ID)
			return c.NoContent(http.StatusNoContent)
		}))

		// Transaction example
		g.Post("/users/batch", func(c *aarv.Context) error {
			ctx := c.Request().Context()

			// Start transaction
			tx, err := db.BeginTx(ctx)
			if err != nil {
				return aarv.ErrInternal(err)
			}

			// Create multiple users in a transaction
			users := []CreateUserReq{
				{Email: "batch1@example.com", Name: "Batch User 1"},
				{Email: "batch2@example.com", Name: "Batch User 2"},
			}

			created := make([]*User, 0, len(users))
			for _, req := range users {
				user := &User{Email: req.Email, Name: req.Name}
				if err := tx.CreateUser(ctx, user); err != nil {
					tx.Rollback()
					return aarv.ErrInternal(err)
				}
				created = append(created, user)
			}

			if err := tx.Commit(); err != nil {
				return aarv.ErrInternal(err)
			}

			return c.JSON(http.StatusCreated, map[string]any{
				"created": created,
				"count":   len(created),
			})
		})
	})

	// Database info endpoint (using /info to avoid pattern conflicts with groups)
	app.Get("/info", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"message": "Database Integration Patterns Demo",
			"patterns": []string{
				"Repository pattern for data access",
				"Middleware for DB injection",
				"Transaction support",
				"Health checks with DB ping",
			},
			"endpoints": map[string]string{
				"GET /info":                 "This endpoint",
				"POST /api/v1/users":        "Create user",
				"GET /api/v1/users":         "List users (?limit=10&offset=0)",
				"GET /api/v1/users/{id}":    "Get user by ID",
				"PUT /api/v1/users/{id}":    "Update user",
				"DELETE /api/v1/users/{id}": "Delete user",
				"POST /api/v1/users/batch":  "Create multiple users (transaction)",
				"GET /health":               "Health check (DB ping)",
			},
		})
	})

	fmt.Println("Database Demo on :8080")
	fmt.Println()
	fmt.Println("  This example demonstrates database integration patterns:")
	fmt.Println("  - Repository pattern for clean data access")
	fmt.Println("  - Middleware for injecting DB connections")
	fmt.Println("  - Transaction support for atomic operations")
	fmt.Println("  - Health checks with database ping")
	fmt.Println()
	fmt.Println("  Create: curl -X POST http://localhost:8080/api/v1/users \\")
	fmt.Println("            -H 'Content-Type: application/json' \\")
	fmt.Println("            -d '{\"email\":\"test@example.com\",\"name\":\"Test User\"}'")
	fmt.Println("  List:   curl http://localhost:8080/api/v1/users")
	fmt.Println("  Health: curl http://localhost:8080/health")

	app.Listen(":8080")
}
