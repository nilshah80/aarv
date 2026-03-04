package encrypt

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

func TestGenerateKey(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	if len(key) != KeySize {
		t.Errorf("expected key length %d, got %d", KeySize, len(key))
	}
}

func TestEncryptor_EncryptDecrypt(t *testing.T) {
	key, _ := GenerateKey()
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{"empty", []byte{}},
		{"short", []byte("hello")},
		{"json", []byte(`{"id":1,"name":"alice","email":"alice@test.com"}`)},
		{"large", bytes.Repeat([]byte("x"), 10000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := enc.Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt failed: %v", err)
			}

			decrypted, err := enc.Decrypt(encrypted)
			if err != nil {
				t.Fatalf("Decrypt failed: %v", err)
			}

			if !bytes.Equal(decrypted, tt.plaintext) {
				t.Errorf("decrypted data doesn't match original")
			}
		})
	}
}

func TestEncryptor_InvalidKey(t *testing.T) {
	_, err := NewEncryptor([]byte("short"))
	if err != ErrInvalidKey {
		t.Errorf("expected ErrInvalidKey, got %v", err)
	}
}

func TestEncryptor_DecryptInvalidPayload(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	tests := []struct {
		name    string
		payload []byte
		wantErr error
	}{
		{"invalid base64", []byte("not-valid-base64!!!"), ErrInvalidPayload},
		{"too short", []byte("YWJj"), ErrInvalidPayload}, // "abc" base64 encoded
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := enc.Decrypt(tt.payload)
			if err != tt.wantErr {
				t.Errorf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestEncryptor_DecryptWrongKey(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()

	enc1, _ := NewEncryptor(key1)
	enc2, _ := NewEncryptor(key2)

	encrypted, _ := enc1.Encrypt([]byte("secret"))
	_, err := enc2.Decrypt(encrypted)
	if err != ErrDecryptionFailed {
		t.Errorf("expected ErrDecryptionFailed, got %v", err)
	}
}

func TestMiddleware_EncryptResponse(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	app := aarv.New(aarv.WithBanner(false))
	middleware, err := New(key)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	app.Use(middleware)

	app.Get("/test", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"message": "hello"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != EncryptedContentType {
		t.Errorf("expected Content-Type %s, got %s", EncryptedContentType, ct)
	}

	// Decrypt the response
	decrypted, err := enc.Decrypt(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("failed to decrypt response: %v", err)
	}

	expected := `{"message":"hello"}`
	got := strings.TrimSpace(string(decrypted))
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestMiddleware_DecryptRequest(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	var receivedBody string
	app := aarv.New(aarv.WithBanner(false))
	middleware, _ := New(key)
	app.Use(middleware)

	app.Post("/test", func(c *aarv.Context) error {
		body, _ := io.ReadAll(c.Request().Body)
		receivedBody = string(body)
		return c.Text(200, "ok")
	})

	// Encrypt the request body
	plaintext := `{"name":"alice"}`
	encrypted, _ := enc.Encrypt([]byte(plaintext))

	req := httptest.NewRequest("POST", "/test", bytes.NewReader(encrypted))
	req.Header.Set("Content-Type", EncryptedContentType)
	req.ContentLength = int64(len(encrypted))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	if receivedBody != plaintext {
		t.Errorf("expected body %s, got %s", plaintext, receivedBody)
	}
}

func TestMiddleware_InvalidEncryptedRequest(t *testing.T) {
	key, _ := GenerateKey()

	app := aarv.New(aarv.WithBanner(false))
	middleware, _ := New(key)
	app.Use(middleware)

	app.Post("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	req := httptest.NewRequest("POST", "/test", bytes.NewReader([]byte("invalid-encrypted-data")))
	req.Header.Set("Content-Type", EncryptedContentType)
	req.ContentLength = 22

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestMiddleware_ExcludedPath(t *testing.T) {
	key, _ := GenerateKey()

	app := aarv.New(aarv.WithBanner(false))
	middleware, _ := New(key, Config{
		EncryptResponse: true,
		DecryptRequest:  true,
		ExcludedPaths:   []string{"/health"},
	})
	app.Use(middleware)

	app.Get("/health", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct == EncryptedContentType {
		t.Errorf("expected non-encrypted response for excluded path")
	}
}

func TestMiddleware_ExcludedContentType(t *testing.T) {
	key, _ := GenerateKey()

	app := aarv.New(aarv.WithBanner(false))
	middleware, _ := New(key, Config{
		EncryptResponse: true,
		ExcludedTypes:   []string{"text/plain"},
	})
	app.Use(middleware)

	app.Get("/text", func(c *aarv.Context) error {
		return c.Text(200, "hello")
	})

	req := httptest.NewRequest("GET", "/text", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct == EncryptedContentType {
		t.Errorf("expected non-encrypted response for excluded content type")
	}
}

func TestMiddleware_DisableEncryption(t *testing.T) {
	key, _ := GenerateKey()

	app := aarv.New(aarv.WithBanner(false))
	middleware, _ := New(key, Config{
		EncryptResponse: false,
		DecryptRequest:  false,
	})
	app.Use(middleware)

	app.Get("/test", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"message": "hello"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct == EncryptedContentType {
		t.Errorf("expected non-encrypted response when encryption disabled")
	}
}

func TestMustNew_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid key")
		}
	}()

	MustNew([]byte("short"))
}

func BenchmarkEncryptor_Encrypt_Small(b *testing.B) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	data := []byte(`{"id":1,"name":"alice"}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.Encrypt(data)
	}
}

func BenchmarkEncryptor_Encrypt_Large(b *testing.B) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	data := bytes.Repeat([]byte("x"), 10000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.Encrypt(data)
	}
}

func BenchmarkEncryptor_Decrypt_Small(b *testing.B) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)
	data := []byte(`{"id":1,"name":"alice"}`)
	encrypted, _ := enc.Encrypt(data)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.Decrypt(encrypted)
	}
}

func BenchmarkMiddleware_EncryptResponse(b *testing.B) {
	key, _ := GenerateKey()

	app := aarv.New(aarv.WithBanner(false))
	middleware, _ := New(key)
	app.Use(middleware)

	app.Get("/test", func(c *aarv.Context) error {
		return c.JSON(200, map[string]string{"id": "1", "name": "alice"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}

func BenchmarkMiddleware_DecryptRequest(b *testing.B) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	app := aarv.New(aarv.WithBanner(false))
	middleware, _ := New(key)
	app.Use(middleware)

	app.Post("/test", func(c *aarv.Context) error {
		return c.Text(200, "ok")
	})

	plaintext := `{"name":"alice","email":"alice@test.com"}`
	encrypted, _ := enc.Encrypt([]byte(plaintext))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/test", bytes.NewReader(encrypted))
		req.Header.Set("Content-Type", EncryptedContentType)
		req.ContentLength = int64(len(encrypted))
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}
