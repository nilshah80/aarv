// Example: Authentication patterns — JWT, API keys, and session-based auth.
// Demonstrates various authentication strategies using aarv middleware.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/requestid"
)

// =============================================================================
// JWT Authentication (simplified implementation for demo)
// =============================================================================

const jwtSecret = "your-256-bit-secret" // In production, use env var

type JWTClaims struct {
	UserID   int      `json:"user_id"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	Exp      int64    `json:"exp"`
}

// createJWT creates a simple JWT token (HS256)
func createJWT(claims JWTClaims) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claimsJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)

	h := hmac.New(sha256.New, []byte(jwtSecret))
	h.Write([]byte(header + "." + payload))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return header + "." + payload + "." + signature
}

// parseJWT parses and validates a JWT token
func parseJWT(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	// Verify signature
	h := hmac.New(sha256.New, []byte(jwtSecret))
	h.Write([]byte(parts[0] + "." + parts[1]))
	expectedSig := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	if parts[2] != expectedSig {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode claims
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid payload")
	}

	var claims JWTClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid claims")
	}

	// Check expiration
	if claims.Exp < time.Now().Unix() {
		return nil, fmt.Errorf("token expired")
	}

	return &claims, nil
}

// JWTMiddleware validates JWT tokens from Authorization header
func JWTMiddleware() aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"missing or invalid authorization header"}`, http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(auth, "Bearer ")
			claims, err := parseJWT(token)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusUnauthorized)
				return
			}

			// Store claims in request context for handler access
			ctx := context.WithValue(r.Context(), "user_id", claims.UserID)
			ctx = context.WithValue(ctx, "username", claims.Username)
			ctx = context.WithValue(ctx, "roles", claims.Roles)
			r = r.WithContext(ctx)

			next.ServeHTTP(w, r)
		})
	}
}

// =============================================================================
// API Key Authentication
// =============================================================================

var apiKeys = map[string]string{
	"key_prod_abc123": "production-service",
	"key_dev_xyz789":  "development-client",
}

// APIKeyMiddleware validates API keys from X-API-Key header
func APIKeyMiddleware() aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				http.Error(w, `{"error":"missing X-API-Key header"}`, http.StatusUnauthorized)
				return
			}

			clientName, ok := apiKeys[apiKey]
			if !ok {
				http.Error(w, `{"error":"invalid API key"}`, http.StatusUnauthorized)
				return
			}

			// Store client info in request context
			ctx := context.WithValue(r.Context(), "api_client", clientName)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// =============================================================================
// Session-based Authentication (secure signed cookies)
// =============================================================================

// sessionSecret is the HMAC signing key for session cookies.
// In production, load from environment or secret manager.
var sessionSecret = []byte("session-signing-secret-change-me!")

// encryptionKey is the 32-byte master key for encrypted cookies.
// In production, load from environment or secret manager.
var encryptionKey = []byte("0123456789abcdef0123456789abcdef")

var (
	sessions   = make(map[string]*Session)
	sessionsMu sync.RWMutex
)

type Session struct {
	ID        string
	UserID    int
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func generateSessionID() string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return fmt.Sprintf("sess_%x", h.Sum(nil)[:16])
}

func createSession(userID int, username string) *Session {
	sess := &Session{
		ID:        generateSessionID(),
		UserID:    userID,
		Username:  username,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	sessionsMu.Lock()
	sessions[sess.ID] = sess
	sessionsMu.Unlock()

	return sess
}

func getSession(id string) *Session {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()

	sess, ok := sessions[id]
	if !ok || time.Now().After(sess.ExpiresAt) {
		return nil
	}
	return sess
}

// SecureSessionMiddleware validates signed session cookies using aarv's
// SecureCookie API. The session ID is HMAC-signed so it cannot be forged
// or tampered with. Server-side expiry is set to 24 hours.
func SecureSessionMiddleware() aarv.Middleware {
	return aarv.WrapMiddleware(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			sessionID, err := c.SecureCookie("session_id", sessionSecret, 86400)
			if err != nil {
				return aarv.ErrUnauthorized("invalid or missing session")
			}

			sess := getSession(sessionID)
			if sess == nil {
				return aarv.ErrUnauthorized("invalid or expired session")
			}

			c.Set("session", sess)
			c.Set("user_id", sess.UserID)
			c.Set("username", sess.Username)
			return next(c)
		}
	})
}

// =============================================================================
// Role-based Access Control (RBAC)
// =============================================================================

// RequireRoles middleware checks if user has any of the required roles
func RequireRoles(requiredRoles ...string) aarv.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userRoles, _ := r.Context().Value("roles").([]string)
			for _, required := range requiredRoles {
				for _, have := range userRoles {
					if have == required {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
			http.Error(w, `{"error":"insufficient permissions"}`, http.StatusForbidden)
		})
	}
}

// =============================================================================
// Request/Response Types
// =============================================================================

type LoginReq struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
}

type LoginRes struct {
	Token     string `json:"token,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	ExpiresIn int    `json:"expires_in"`
}

// Demo user store
var users = map[string]struct {
	ID       int
	Password string
	Roles    []string
}{
	"admin": {ID: 1, Password: "admin123", Roles: []string{"admin", "user"}},
	"user":  {ID: 2, Password: "user123", Roles: []string{"user"}},
}

// =============================================================================
// Main
// =============================================================================

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
	)

	app.Use(aarv.Recovery(), requestid.New(), aarv.Logger())

	// JWT authentication
	app.Post("/auth/jwt/login", aarv.Bind(func(c *aarv.Context, req LoginReq) (LoginRes, error) {
		user, ok := users[req.Username]
		if !ok || user.Password != req.Password {
			return LoginRes{}, aarv.ErrUnauthorized("invalid credentials")
		}

		claims := JWTClaims{
			UserID:   user.ID,
			Username: req.Username,
			Roles:    user.Roles,
			Exp:      time.Now().Add(1 * time.Hour).Unix(),
		}

		return LoginRes{
			Token:     createJWT(claims),
			ExpiresIn: 3600,
		}, nil
	}))

	// Session authentication — uses signed cookies
	app.Post("/auth/session/login", aarv.Bind(func(c *aarv.Context, req LoginReq) (LoginRes, error) {
		user, ok := users[req.Username]
		if !ok || user.Password != req.Password {
			return LoginRes{}, aarv.ErrUnauthorized("invalid credentials")
		}

		sess := createSession(user.ID, req.Username)

		// Set signed session cookie — HMAC-SHA256 prevents forgery.
		// HttpOnly=true and SameSite=Lax are applied by default.
		if err := c.SetSecureCookie("session_id", sess.ID, sessionSecret, aarv.CookieOptions{
			MaxAge: 86400, // 24 hours
		}); err != nil {
			return LoginRes{}, aarv.ErrInternal(err)
		}

		return LoginRes{
			SessionID: sess.ID,
			ExpiresIn: 86400,
		}, nil
	}))

	// JWT-protected routes
	app.Group("/api/jwt", func(g *aarv.RouteGroup) {
		g.Use(JWTMiddleware())

		g.Get("/protected", func(c *aarv.Context) error {
			ctx := c.Request().Context()
			userID := ctx.Value("user_id")
			username := ctx.Value("username")
			return c.JSON(http.StatusOK, map[string]any{
				"message":  "JWT-protected endpoint",
				"user_id":  userID,
				"username": username,
			})
		})

		g.Get("/admin", func(c *aarv.Context) error {
			// Additional role check
			ctx := c.Request().Context()
			roles, _ := ctx.Value("roles").([]string)
			isAdmin := false
			for _, r := range roles {
				if r == "admin" {
					isAdmin = true
					break
				}
			}
			if !isAdmin {
				return aarv.ErrForbidden("admin access required")
			}

			return c.JSON(http.StatusOK, map[string]any{
				"message": "Admin-only endpoint",
				"roles":   roles,
			})
		})
	})

	// API key-protected routes
	app.Group("/api/key", func(g *aarv.RouteGroup) {
		g.Use(APIKeyMiddleware())

		g.Get("/protected", func(c *aarv.Context) error {
			client := c.Request().Context().Value("api_client")
			return c.JSON(http.StatusOK, map[string]any{
				"message": "API-key-protected endpoint",
				"client":  client,
			})
		})
	})

	// Session-protected routes
	app.Group("/api/session", func(g *aarv.RouteGroup) {
		g.Use(SecureSessionMiddleware())

		g.Get("/me", func(c *aarv.Context) error {
			session := c.MustGet("session").(*Session)
			return c.JSON(http.StatusOK, map[string]any{
				"message":    "Session-protected endpoint",
				"user_id":    session.UserID,
				"username":   session.Username,
				"session_id": session.ID,
				"expires_at": session.ExpiresAt,
			})
		})

		g.Post("/logout", func(c *aarv.Context) error {
			session := c.MustGet("session").(*Session)

			// Delete session
			sessionsMu.Lock()
			delete(sessions, session.ID)
			sessionsMu.Unlock()

			// Clear signed cookie using negative MaxAge
			_ = c.SetSecureCookie("session_id", "", sessionSecret, aarv.CookieOptions{
				MaxAge: -1,
			})

			return c.JSON(http.StatusOK, map[string]string{
				"message": "Logged out successfully",
			})
		})
	})

	// -----------------------------------------------------------------
	// Encrypted cookie endpoints — store sensitive data in cookies
	// using AES-256-GCM encryption + HMAC-SHA256 signing.
	// -----------------------------------------------------------------

	// Save user preferences in an encrypted cookie
	app.Post("/preferences", func(c *aarv.Context) error {
		type PrefsReq struct {
			Theme    string `json:"theme"`
			Language string `json:"language"`
			Timezone string `json:"timezone"`
		}
		var prefs PrefsReq
		if err := c.BindJSON(&prefs); err != nil {
			return aarv.ErrBadRequest("invalid JSON")
		}

		// Serialize preferences as JSON and encrypt into a cookie
		data, _ := json.Marshal(prefs)
		if err := c.SetEncryptedCookie("prefs", string(data), encryptionKey, aarv.CookieOptions{
			MaxAge: 30 * 86400, // 30 days
		}); err != nil {
			return aarv.ErrInternal(err)
		}

		return c.JSON(http.StatusOK, map[string]any{
			"message": "Preferences saved in encrypted cookie",
			"prefs":   prefs,
		})
	})

	// Read user preferences from encrypted cookie
	app.Get("/preferences", func(c *aarv.Context) error {
		data, err := c.EncryptedCookie("prefs", encryptionKey)
		if err != nil {
			return c.JSON(http.StatusOK, map[string]any{
				"message": "No preferences set",
				"prefs":   nil,
			})
		}

		var prefs map[string]string
		if err := json.Unmarshal([]byte(data), &prefs); err != nil {
			return aarv.ErrBadRequest("corrupted preferences")
		}

		return c.JSON(http.StatusOK, map[string]any{
			"message": "Preferences loaded from encrypted cookie",
			"prefs":   prefs,
		})
	})

	// Clear preferences cookie
	app.Delete("/preferences", func(c *aarv.Context) error {
		_ = c.SetEncryptedCookie("prefs", "", encryptionKey, aarv.CookieOptions{
			MaxAge: -1,
		})
		return c.JSON(http.StatusOK, map[string]string{
			"message": "Preferences cleared",
		})
	})

	// Public endpoints
	app.Get("/info", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"message": "Authentication Patterns Demo",
			"endpoints": map[string]string{
				"GET /info":                "This endpoint",
				"POST /auth/jwt/login":     "Get JWT token",
				"POST /auth/session/login": "Create session (signed cookie)",
				"GET /public":              "Public endpoint",
				"GET /api/jwt/protected":   "JWT-protected endpoint",
				"GET /api/key/protected":   "API-key-protected endpoint",
				"GET /api/session/me":      "Session-protected (signed cookie)",
				"GET /api/jwt/admin":       "Admin-only endpoint (JWT + role)",
				"POST /preferences":        "Save encrypted preferences cookie",
				"GET /preferences":         "Read encrypted preferences cookie",
				"DELETE /preferences":      "Clear preferences cookie",
			},
		})
	})

	app.Get("/public", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"message": "This is a public endpoint",
		})
	})

	fmt.Println("Authentication Demo on :8080")
	fmt.Println()
	fmt.Println("  === JWT Authentication ===")
	fmt.Println("  Login:  curl -X POST http://localhost:8080/auth/jwt/login \\")
	fmt.Println("            -H 'Content-Type: application/json' \\")
	fmt.Println("            -d '{\"username\":\"admin\",\"password\":\"admin123\"}'")
	fmt.Println("  Access: curl http://localhost:8080/api/jwt/protected \\")
	fmt.Println("            -H 'Authorization: Bearer <token>'")
	fmt.Println()
	fmt.Println("  === API Key Authentication ===")
	fmt.Println("  Access: curl http://localhost:8080/api/key/protected \\")
	fmt.Println("            -H 'X-API-Key: key_prod_abc123'")
	fmt.Println()
	fmt.Println("  === Session Authentication (signed cookies) ===")
	fmt.Println("  Login:  curl -X POST http://localhost:8080/auth/session/login \\")
	fmt.Println("            -c cookies.txt -H 'Content-Type: application/json' \\")
	fmt.Println("            -d '{\"username\":\"user\",\"password\":\"user123\"}'")
	fmt.Println("  Access: curl http://localhost:8080/api/session/me -b cookies.txt")
	fmt.Println()
	fmt.Println("  === Encrypted Cookies ===")
	fmt.Println("  Save:   curl -X POST http://localhost:8080/preferences \\")
	fmt.Println("            -c cookies.txt -H 'Content-Type: application/json' \\")
	fmt.Println("            -d '{\"theme\":\"dark\",\"language\":\"en\",\"timezone\":\"UTC\"}'")
	fmt.Println("  Read:   curl http://localhost:8080/preferences -b cookies.txt")
	fmt.Println("  Clear:  curl -X DELETE http://localhost:8080/preferences -b cookies.txt -c cookies.txt")
	fmt.Println()
	fmt.Println("  Demo users: admin/admin123 (admin role), user/user123 (user role)")

	_ = app.Listen(":8080")
}
