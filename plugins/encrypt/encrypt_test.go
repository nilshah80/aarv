package encrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilshah80/aarv"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("rand failure")
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) {
	return 0, errors.New("body failure")
}

func (failingBody) Close() error { return nil }

type failingCloseReadCloser struct {
	io.Reader
}

func (failingCloseReadCloser) Close() error { return errors.New("close failure") }

func TestGenerateKey(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	if len(key) != KeySize {
		t.Errorf("expected key length %d, got %d", KeySize, len(key))
	}

	oldRead := randRead
	randRead = failingReader{}.Read
	t.Cleanup(func() {
		randRead = oldRead
	})
	if _, err := GenerateKey(); err == nil {
		t.Fatal("expected GenerateKey error when rand reader fails")
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

func TestNewEncryptorInternalErrors(t *testing.T) {
	oldCipher := newAESCipher
	oldGCM := newGCM
	t.Cleanup(func() {
		newAESCipher = oldCipher
		newGCM = oldGCM
	})

	newAESCipher = func([]byte) (cipher.Block, error) {
		return nil, errors.New("cipher failure")
	}
	if _, err := NewEncryptor(bytes.Repeat([]byte{1}, KeySize)); err == nil {
		t.Fatal("expected cipher construction error")
	}

	newAESCipher = aes.NewCipher
	newGCM = func(cipher.Block) (cipher.AEAD, error) {
		return nil, errors.New("gcm failure")
	}
	if _, err := NewEncryptor(bytes.Repeat([]byte{1}, KeySize)); err == nil {
		t.Fatal("expected gcm construction error")
	}
}

func TestNewReturnsEncryptorConstructionError(t *testing.T) {
	oldCipher := newAESCipher
	t.Cleanup(func() {
		newAESCipher = oldCipher
	})

	newAESCipher = func([]byte) (cipher.Block, error) {
		return nil, errors.New("cipher failure")
	}
	if _, err := New(bytes.Repeat([]byte{1}, KeySize)); err == nil {
		t.Fatal("expected middleware constructor to return encryptor error")
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

func TestEncryptor_EncryptAndDecryptEdgeCases(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	decrypted, err := enc.Decrypt(nil)
	if err != nil || decrypted != nil {
		t.Fatalf("expected nil plaintext for empty input, got %v %v", decrypted, err)
	}

	oldRead := randRead
	randRead = failingReader{}.Read
	t.Cleanup(func() {
		randRead = oldRead
	})
	if _, err := enc.Encrypt([]byte("test")); err == nil {
		t.Fatal("expected encrypt error when rand reader fails")
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

func TestEncryptResponseWriterWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &encryptResponseWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	w.WriteHeader(http.StatusCreated)
	w.WriteHeader(http.StatusAccepted)

	if w.statusCode != http.StatusAccepted {
		t.Fatalf("expected latest buffered status to stick until body write, got %d", w.statusCode)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("write header should remain buffered until finish, got %d", rec.Code)
	}
}

func TestMiddleware_DecryptRequestLogsBodyCloseFailure(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	var receivedBody string
	app := aarv.New(aarv.WithBanner(false), aarv.WithLogger(logger))
	middleware, _ := New(key)
	app.Use(middleware)

	app.Post("/test", func(c *aarv.Context) error {
		body, _ := io.ReadAll(c.Request().Body)
		receivedBody = string(body)
		return c.Text(200, "ok")
	})

	plaintext := `{"name":"alice"}`
	encrypted, _ := enc.Encrypt([]byte(plaintext))

	req := httptest.NewRequest("POST", "/test", nil)
	req.Body = failingCloseReadCloser{Reader: bytes.NewReader(encrypted)}
	req.Header.Set("Content-Type", EncryptedContentType)
	req.ContentLength = int64(len(encrypted))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if receivedBody != plaintext {
		t.Fatalf("expected body %s, got %s", plaintext, receivedBody)
	}
	if !strings.Contains(logBuf.String(), "encrypt request body close failed") {
		t.Fatalf("expected close warning log, got %s", logBuf.String())
	}
}

func TestMiddleware_DecryptRequestLogsBodyCloseFailureWithoutAarvContext(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	old := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(old)
	})

	plaintext := []byte(`{"name":"alice"}`)
	encrypted, _ := enc.Encrypt(plaintext)

	middleware, err := New(key, Config{
		EncryptResponse: false,
		DecryptRequest:  true,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Body = failingCloseReadCloser{Reader: bytes.NewReader(encrypted)}
	req.Header.Set("Content-Type", EncryptedContentType)
	req.ContentLength = int64(len(encrypted))

	rec := httptest.NewRecorder()
	middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rec.Code)
	}
	if !strings.Contains(logBuf.String(), "encrypt request body close failed") {
		t.Fatalf("expected fallback close warning log, got %s", logBuf.String())
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

func TestMiddleware_ExcludedContentTypePrefix(t *testing.T) {
	key, _ := GenerateKey()

	app := aarv.New(aarv.WithBanner(false))
	middleware, _ := New(key, Config{
		EncryptResponse: true,
		ExcludedTypes:   []string{"image/"},
	})
	app.Use(middleware)

	app.Get("/img", func(c *aarv.Context) error {
		c.Response().Header().Set("Content-Type", "image/png")
		_, _ = c.Response().Write([]byte("png"))
		return nil
	})

	req := httptest.NewRequest("GET", "/img", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected prefix-excluded content type, got %q", got)
	}
}

func TestMiddleware_NoExcludedTypesStillEncrypts(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	app := aarv.New(aarv.WithBanner(false))
	middleware, _ := New(key, Config{
		EncryptResponse: true,
		ExcludedTypes:   nil,
	})
	app.Use(middleware)

	app.Get("/text", func(c *aarv.Context) error {
		c.Response().Header().Set("Content-Type", "text/plain")
		return c.Text(http.StatusOK, "hello")
	})

	req := httptest.NewRequest("GET", "/text", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Type"); got != EncryptedContentType {
		t.Fatalf("expected encrypted content type, got %q", got)
	}
	decrypted, err := enc.Decrypt(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("expected decryptable response, got %v", err)
	}
	if strings.TrimSpace(string(decrypted)) != "hello" {
		t.Fatalf("expected encrypted plaintext hello, got %q", string(decrypted))
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

func TestEncryptResponseWriterHelpers(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	releaseEncryptResponseWriter(nil)

	large := &encryptResponseWriter{}
	large.buf.Write(bytes.Repeat([]byte("x"), maxReusableEncryptedBody+1))
	releaseEncryptResponseWriter(large)
	if large.buf.Cap() > maxReusableEncryptedBody {
		t.Fatalf("expected oversized encrypt buffer to be dropped, got cap=%d", large.buf.Cap())
	}

	rec := httptest.NewRecorder()
	w := &encryptResponseWriter{
		ResponseWriter: rec,
		encryptor:      enc,
		statusCode:     http.StatusAccepted,
	}
	if w.Unwrap() != rec {
		t.Fatal("unwrap should return underlying writer")
	}
	if err := w.finish(); err != nil {
		t.Fatalf("finish on empty body failed: %v", err)
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	w = &encryptResponseWriter{
		ResponseWriter: rec,
		encryptor:      enc,
		skipEncrypt:    true,
	}
	if err := w.finish(); err != nil {
		t.Fatalf("finish on skipped encryption failed: %v", err)
	}

	rec = httptest.NewRecorder()
	w = &encryptResponseWriter{
		ResponseWriter: rec,
		encryptor:      enc,
	}
	_, _ = w.Write([]byte("body"))
	oldRead := randRead
	randRead = failingReader{}.Read
	defer func() {
		randRead = oldRead
	}()
	if err := w.finish(); err == nil {
		t.Fatal("expected finish to propagate encryption error")
	}
}

func TestMiddleware_DecryptReadErrorWithoutHandler(t *testing.T) {
	key, _ := GenerateKey()

	called := false
	handler, err := New(key, Config{
		DecryptRequest:  true,
		EncryptResponse: false,
		OnDecryptError:  nil,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	req := httptest.NewRequest("POST", "/test", nil)
	req.Body = failingBody{}
	req.ContentLength = 10
	req.Header.Set("Content-Type", EncryptedContentType)
	rec := httptest.NewRecorder()
	handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if called {
		t.Fatal("handler should not run when decrypt read fails")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty response body, got %q", rec.Body.String())
	}
}

func TestMiddleware_DecryptReadErrorInvokesCallback(t *testing.T) {
	key, _ := GenerateKey()

	callbackCalled := false
	handler, err := New(key, Config{
		DecryptRequest:  true,
		EncryptResponse: false,
		OnDecryptError: func(w http.ResponseWriter, r *http.Request, err error) {
			callbackCalled = true
			http.Error(w, "bad decrypt", http.StatusBadRequest)
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	req := httptest.NewRequest("POST", "/test", nil)
	req.Body = failingBody{}
	req.ContentLength = 10
	req.Header.Set("Content-Type", EncryptedContentType)
	rec := httptest.NewRecorder()
	handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	})).ServeHTTP(rec, req)

	if !callbackCalled {
		t.Fatal("expected decrypt error callback")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestMiddleware_DecryptRestoresOriginalContentType(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	var receivedCT string
	var receivedBody string
	middleware, _ := New(key, Config{
		EncryptResponse: false,
		DecryptRequest:  true,
	})
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCT = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))

	payload := []byte(`hello`)
	encrypted, _ := enc.Encrypt(payload)
	req := httptest.NewRequest("POST", "/test", bytes.NewReader(encrypted))
	req.Header.Set("Content-Type", EncryptedContentType)
	req.Header.Set("X-Original-Content-Type", "text/plain")
	req.ContentLength = int64(len(encrypted))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if receivedCT != "text/plain" {
		t.Fatalf("expected restored content-type text/plain, got %q", receivedCT)
	}
	if receivedBody != "hello" {
		t.Fatalf("expected decrypted body hello, got %q", receivedBody)
	}
}

func TestMustNewSuccess(t *testing.T) {
	key, _ := GenerateKey()
	if MustNew(key) == nil {
		t.Fatal("expected middleware instance")
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
