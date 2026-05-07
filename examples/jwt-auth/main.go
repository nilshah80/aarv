package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/jwt"
	"github.com/nilshah80/aarv/plugins/problem"
	"github.com/nilshah80/aarv/plugins/requestid"
	"github.com/nilshah80/aarv/plugins/secure"
)

var hmacSecret = []byte("0123456789abcdef0123456789abcdef")

// LoginReq is the credentials payload accepted by /login.
type LoginReq struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
}

// LoginRes carries the signed JWT issued on successful login.
type LoginRes struct {
	Token string `json:"token"`
}

func main() {
	app := aarv.New(
		aarv.WithBanner(true),
		aarv.WithErrorHandler(problem.Handler(problem.Config{
			Type: "https://example.com/problems",
		})),
	)

	app.Use(
		aarv.Recovery(),
		requestid.New(),
		secure.New(),
	)

	app.Post("/login", aarv.Bind(func(c *aarv.Context, req LoginReq) (LoginRes, error) {
		if req.Username != "admin" || req.Password != "password" {
			return LoginRes{}, aarv.ErrUnauthorized("invalid credentials")
		}

		now := time.Now()
		token, err := jwt.SignToken(jwt.HS256, hmacSecret, map[string]any{
			"sub":   "usr_admin",
			"name":  req.Username,
			"roles": []string{"admin"},
			"iat":   now.Unix(),
			"exp":   now.Add(15 * time.Minute).Unix(),
			"iss":   "aarv-example",
			"aud":   "aarv-api",
		})
		if err != nil {
			return LoginRes{}, aarv.ErrInternal(err)
		}
		return LoginRes{Token: token}, nil
	}))

	api := jwt.New(jwt.Config{
		HMACSecret: hmacSecret,
		Issuer:     "aarv-example",
		Audience:   "aarv-api",
		SkipPaths:  []string{"/login", "/health"},
	})

	app.Use(api)

	app.Get("/me", func(c *aarv.Context) error {
		claims, ok := jwt.From(c)
		if !ok {
			return aarv.ErrUnauthorized("missing claims")
		}
		return c.JSON(http.StatusOK, map[string]any{
			"subject": claims["sub"],
			"name":    claims["name"],
			"roles":   claims["roles"],
		})
	})

	app.Get("/health", func(c *aarv.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	fmt.Println("JWT Auth example on :8080")
	fmt.Println(`  TOKEN=$(curl -s -X POST http://localhost:8080/login -d '{"username":"admin","password":"password"}' -H 'Content-Type: application/json')`)
	fmt.Println("  GET /me with Authorization: Bearer <token>")

	_ = app.Listen(":8080")
}
