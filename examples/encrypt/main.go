// Example: AES-GCM Encrypted API
//
// This example demonstrates how to use the encrypt middleware
// to automatically encrypt responses and decrypt requests.
//
// Run: go run main.go
// Test: See the client example below
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/nilshah80/aarv"
	"github.com/nilshah80/aarv/plugins/encrypt"
)

func main() {
	// Generate a 256-bit key (in production, load from secure storage)
	key, err := encrypt.GenerateKey()
	if err != nil {
		log.Fatal(err)
	}

	// Print the key in base64 for client use
	fmt.Printf("Encryption Key (base64): %s\n\n", base64.StdEncoding.EncodeToString(key))

	app := aarv.New()

	// Create encryption middleware
	encryptMiddleware, err := encrypt.New(key, encrypt.Config{
		EncryptResponse: true,
		DecryptRequest:  true,
		ExcludedPaths:   []string{"/health", "/public"},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Apply encryption to API routes
	app.Group("/api", func(api *aarv.RouteGroup) {
		api.Use(encryptMiddleware)

		// Encrypted endpoint - responses are auto-encrypted
		api.Get("/users/{id}", func(c *aarv.Context) error {
			userID := c.Param("id")
			return c.JSON(200, map[string]any{
				"id":    userID,
				"name":  "Alice Smith",
				"email": "alice@example.com",
				"role":  "admin",
			})
		})

		// Encrypted POST - request body is auto-decrypted
		api.Post("/users", func(c *aarv.Context) error {
			// At this point, the request body has been decrypted
			var user struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			}
			if err := c.BindJSON(&user); err != nil {
				return c.JSON(400, map[string]string{"error": err.Error()})
			}
			return c.JSON(201, map[string]any{
				"id":    "new-user-123",
				"name":  user.Name,
				"email": user.Email,
			})
		})
	})

	// Public endpoints - no encryption
	app.Get("/health", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	app.Get("/public", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"message": "This is public, not encrypted"})
	})

	fmt.Println("Server starting on :8080")
	fmt.Println("Endpoints:")
	fmt.Println("  GET  /api/users/:id  - Encrypted response")
	fmt.Println("  POST /api/users      - Encrypted request/response")
	fmt.Println("  GET  /health         - Public (no encryption)")
	fmt.Println("  GET  /public         - Public (no encryption)")
	fmt.Println()
	fmt.Println("Example client code to decrypt response:")
	fmt.Print(`
  // Decode base64 response
  ciphertext, _ := base64.StdEncoding.DecodeString(responseBody)

  // nonce is first 12 bytes
  nonce := ciphertext[:12]
  ciphertextData := ciphertext[12:]

  // Decrypt using AES-GCM
  block, _ := aes.NewCipher(key)
  gcm, _ := cipher.NewGCM(block)
  plaintext, _ := gcm.Open(nil, nonce, ciphertextData, nil)
`)

	app.Listen(":8080")
}

// ExampleClient demonstrates how to call encrypted endpoints
func ExampleClient(serverURL string, key []byte) {
	enc, _ := encrypt.NewEncryptor(key)

	// GET request - decrypt response
	resp, err := http.Get(serverURL + "/api/users/123")
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	encryptedBody, _ := io.ReadAll(resp.Body)
	decrypted, err := enc.Decrypt(encryptedBody)
	if err != nil {
		log.Fatal("Failed to decrypt:", err)
	}
	fmt.Println("Decrypted response:", string(decrypted))

	// POST request - encrypt request, decrypt response
	requestBody := []byte(`{"name":"Bob","email":"bob@example.com"}`)
	encryptedRequest, _ := enc.Encrypt(requestBody)

	req, _ := http.NewRequest("POST", serverURL+"/api/users", bytes.NewReader(encryptedRequest))
	req.Header.Set("Content-Type", encrypt.EncryptedContentType)
	req.Header.Set("X-Original-Content-Type", "application/json")

	client := &http.Client{}
	resp, err = client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	encryptedResp, _ := io.ReadAll(resp.Body)
	decryptedResp, _ := enc.Decrypt(encryptedResp)
	fmt.Println("Decrypted POST response:", string(decryptedResp))
}
