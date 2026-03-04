// Package encrypt provides AES-GCM encryption middleware for the aarv framework.
//
// It encrypts response bodies and decrypts request bodies using AES-256-GCM
// authenticated encryption. This provides both confidentiality and integrity.
//
// Usage:
//
//	key := encrypt.GenerateKey() // Generate a 256-bit key
//	app.Use(encrypt.New(key))
//
// The middleware uses the following protocol:
//   - Request: Base64(Nonce || Ciphertext || Tag)
//   - Response: Base64(Nonce || Ciphertext || Tag)
//
// Content-Type header "application/encrypted" indicates encrypted payloads.
// The middleware skips encryption for excluded content types (images, etc).
package encrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/nilshah80/aarv"
)

const (
	// NonceSize is the size of the GCM nonce (12 bytes)
	NonceSize = 12
	// KeySize is the required AES-256 key size (32 bytes)
	KeySize = 32
	// EncryptedContentType is the Content-Type for encrypted payloads
	EncryptedContentType = "application/encrypted"
)

var (
	// ErrInvalidKey is returned when the key length is not 32 bytes
	ErrInvalidKey = errors.New("encrypt: key must be 32 bytes for AES-256")
	// ErrInvalidPayload is returned when decryption fails
	ErrInvalidPayload = errors.New("encrypt: invalid encrypted payload")
	// ErrDecryptionFailed is returned when decryption fails due to authentication
	ErrDecryptionFailed = errors.New("encrypt: decryption failed - invalid ciphertext or key")
)

// Config holds configuration for the encryption middleware.
type Config struct {
	// Key is the 32-byte AES-256 encryption key (required)
	Key []byte

	// EncryptResponse enables response body encryption.
	// Default: true
	EncryptResponse bool

	// DecryptRequest enables request body decryption.
	// Default: true
	DecryptRequest bool

	// ExcludedPaths are URL paths that skip encryption/decryption.
	// Useful for health checks, metrics, etc.
	// Default: []
	ExcludedPaths []string

	// ExcludedTypes are MIME types that skip encryption.
	// Default: image/*, video/*, audio/*
	ExcludedTypes []string

	// OnDecryptError is called when request decryption fails.
	// Return nil to continue with empty body, or an error to abort.
	// Default: returns 400 Bad Request
	OnDecryptError func(w http.ResponseWriter, r *http.Request, err error)
}

// DefaultConfig returns the default encryption configuration.
func DefaultConfig() Config {
	return Config{
		EncryptResponse: true,
		DecryptRequest:  true,
		ExcludedPaths:   []string{},
		ExcludedTypes: []string{
			"image/",
			"video/",
			"audio/",
		},
		OnDecryptError: defaultDecryptErrorHandler,
	}
}

func defaultDecryptErrorHandler(w http.ResponseWriter, _ *http.Request, _ error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"error":"invalid encrypted payload"}`))
}

// GenerateKey generates a cryptographically secure 32-byte key for AES-256.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// Encryptor provides methods for encrypting and decrypting data.
// It is safe for concurrent use.
type Encryptor struct {
	gcm cipher.AEAD
}

// NewEncryptor creates a new Encryptor with the given key.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &Encryptor{gcm: gcm}, nil
}

// Encrypt encrypts plaintext using AES-GCM and returns base64-encoded result.
// Format: Base64(Nonce || Ciphertext || Tag)
func (e *Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	ciphertext := e.gcm.Seal(nonce, nonce, plaintext, nil)
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(ciphertext)))
	base64.StdEncoding.Encode(encoded, ciphertext)
	return encoded, nil
}

// Decrypt decrypts base64-encoded ciphertext using AES-GCM.
func (e *Encryptor) Decrypt(encoded []byte) ([]byte, error) {
	if len(encoded) == 0 {
		return nil, nil
	}

	ciphertext := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	n, err := base64.StdEncoding.Decode(ciphertext, encoded)
	if err != nil {
		return nil, ErrInvalidPayload
	}
	ciphertext = ciphertext[:n]

	if len(ciphertext) < NonceSize+e.gcm.Overhead() {
		return nil, ErrInvalidPayload
	}

	nonce := ciphertext[:NonceSize]
	ciphertextData := ciphertext[NonceSize:]

	plaintext, err := e.gcm.Open(nil, nonce, ciphertextData, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// encryptResponseWriter wraps http.ResponseWriter to encrypt the response body.
type encryptResponseWriter struct {
	http.ResponseWriter
	encryptor      *Encryptor
	buf            bytes.Buffer
	statusCode     int
	headerWritten  bool
	isExcludedFunc func(string) bool
	skipEncrypt    bool
}

func (w *encryptResponseWriter) WriteHeader(code int) {
	if !w.headerWritten {
		w.statusCode = code
	}
}

func (w *encryptResponseWriter) Write(b []byte) (int, error) {
	// Check if we should skip encryption based on content type
	if !w.skipEncrypt && w.isExcludedFunc != nil {
		ct := w.ResponseWriter.Header().Get("Content-Type")
		if ct != "" && w.isExcludedFunc(ct) {
			w.skipEncrypt = true
		}
	}

	if w.skipEncrypt {
		if !w.headerWritten {
			w.headerWritten = true
			w.ResponseWriter.WriteHeader(w.statusCode)
		}
		return w.ResponseWriter.Write(b)
	}

	// Buffer the response for encryption
	return w.buf.Write(b)
}

func (w *encryptResponseWriter) finish() error {
	if w.skipEncrypt {
		return nil
	}

	if w.buf.Len() == 0 {
		if !w.headerWritten {
			w.headerWritten = true
			w.ResponseWriter.WriteHeader(w.statusCode)
		}
		return nil
	}

	// Encrypt the buffered response
	encrypted, err := w.encryptor.Encrypt(w.buf.Bytes())
	if err != nil {
		return err
	}

	// Set encrypted content type
	w.ResponseWriter.Header().Set("Content-Type", EncryptedContentType)
	w.ResponseWriter.Header().Del("Content-Length")

	if !w.headerWritten {
		w.headerWritten = true
		w.ResponseWriter.WriteHeader(w.statusCode)
	}

	_, err = w.ResponseWriter.Write(encrypted)
	return err
}

func (w *encryptResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// New creates an encryption middleware with the given key and optional configuration.
func New(key []byte, config ...Config) (aarv.Middleware, error) {
	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}
	cfg.Key = key

	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}

	encryptor, err := NewEncryptor(key)
	if err != nil {
		return nil, err
	}

	// Build excluded paths set
	excludedPaths := make(map[string]struct{}, len(cfg.ExcludedPaths))
	for _, p := range cfg.ExcludedPaths {
		excludedPaths[p] = struct{}{}
	}

	// Build excluded types set
	excludedTypes := make(map[string]struct{})
	excludedPrefixes := make([]string, 0)
	for _, t := range cfg.ExcludedTypes {
		if strings.HasSuffix(t, "/") {
			excludedPrefixes = append(excludedPrefixes, t)
		} else {
			excludedTypes[t] = struct{}{}
		}
	}

	isExcludedType := func(contentType string) bool {
		if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
			contentType = strings.TrimSpace(contentType[:idx])
		}
		if _, ok := excludedTypes[contentType]; ok {
			return true
		}
		for _, prefix := range excludedPrefixes {
			if strings.HasPrefix(contentType, prefix) {
				return true
			}
		}
		return false
	}

	isExcludedPath := func(path string) bool {
		_, ok := excludedPaths[path]
		return ok
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if path is excluded
			if isExcludedPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Decrypt request body if enabled and content type is encrypted
			if cfg.DecryptRequest && r.Body != nil && r.ContentLength > 0 {
				ct := r.Header.Get("Content-Type")
				if ct == EncryptedContentType {
					body, err := io.ReadAll(r.Body)
					r.Body.Close()
					if err != nil {
						if cfg.OnDecryptError != nil {
							cfg.OnDecryptError(w, r, err)
						}
						return
					}

					decrypted, err := encryptor.Decrypt(body)
					if err != nil {
						if cfg.OnDecryptError != nil {
							cfg.OnDecryptError(w, r, err)
						}
						return
					}

					// Replace request body with decrypted content
					r.Body = io.NopCloser(bytes.NewReader(decrypted))
					r.ContentLength = int64(len(decrypted))
					// Restore original content type if provided in header
					if origCT := r.Header.Get("X-Original-Content-Type"); origCT != "" {
						r.Header.Set("Content-Type", origCT)
					} else {
						r.Header.Set("Content-Type", "application/json")
					}
				}
			}

			// Encrypt response if enabled
			if cfg.EncryptResponse {
				erw := &encryptResponseWriter{
					ResponseWriter:   w,
					encryptor:        encryptor,
					statusCode:       http.StatusOK,
					isExcludedFunc:   isExcludedType,
				}

				defer func() { _ = erw.finish() }()
				next.ServeHTTP(erw, r)
			} else {
				next.ServeHTTP(w, r)
			}
		})
	}, nil
}

// MustNew creates an encryption middleware and panics on error.
func MustNew(key []byte, config ...Config) aarv.Middleware {
	m, err := New(key, config...)
	if err != nil {
		panic(err)
	}
	return m
}
