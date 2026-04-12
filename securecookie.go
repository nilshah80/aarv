package aarv

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors for secure cookie operations.
var (
	// ErrEmptySecret is returned when a signing secret is empty.
	ErrEmptySecret = errors.New("aarv: signing secret must not be empty")

	// ErrInvalidCookieSignature is returned when the HMAC signature does not
	// match (tampered value, wrong secret, or cross-name replay).
	ErrInvalidCookieSignature = errors.New("aarv: invalid cookie signature")

	// ErrCookieExpired is returned when the server-side timestamp check fails.
	ErrCookieExpired = errors.New("aarv: secure cookie expired")

	// ErrInvalidCookieFormat is returned when the cookie value does not have
	// the expected payload|timestamp|mac structure.
	ErrInvalidCookieFormat = errors.New("aarv: invalid secure cookie format")

	// ErrInvalidEncryptionKey is returned when the encryption key is not
	// exactly 32 bytes.
	ErrInvalidEncryptionKey = errors.New("aarv: encryption key must be 32 bytes")

	// ErrCookieDecryptFailed is returned when decryption fails due to
	// corrupted data, short nonce, or AES-GCM authentication failure.
	ErrCookieDecryptFailed = errors.New("aarv: cookie decryption failed")
)

const gcmNonceSize = 12

// CookieOptions configures the HTTP attributes of a secure cookie.
// When provided to SetSecureCookie or SetEncryptedCookie, unset fields
// inherit secure defaults: Path="/", HttpOnly=true, SameSite=Lax.
// To explicitly disable HttpOnly, set DisableHttpOnly=true.
type CookieOptions struct {
	// Path restricts the cookie to this URL path. Default: "/".
	Path string

	// Domain restricts the cookie to this domain. Default: "" (current domain).
	Domain string

	// MaxAge is the HTTP Max-Age in seconds.
	// 0 = session cookie (browser deletes on close).
	// >0 = persistent cookie with both Max-Age and Expires set.
	// <0 = delete cookie (Max-Age=-1, Expires=epoch).
	MaxAge int

	// Secure restricts the cookie to HTTPS connections. Default: false.
	Secure bool

	// DisableHttpOnly explicitly disables the HttpOnly flag.
	// When false (default), HttpOnly is set to true for security.
	DisableHttpOnly bool

	// SameSite controls cross-site request cookie behavior.
	// Default: http.SameSiteLaxMode. Set to http.SameSiteNoneMode to
	// explicitly allow cross-site cookies.
	SameSite http.SameSite
}

func defaultCookieOptions() CookieOptions {
	return CookieOptions{
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	}
}

func resolveCookieOptions(opts []CookieOptions) CookieOptions {
	d := defaultCookieOptions()
	if len(opts) == 0 {
		return d
	}
	o := opts[0]
	if o.Path == "" {
		o.Path = d.Path
	}
	if o.SameSite == 0 {
		o.SameSite = d.SameSite
	}
	return o
}

// buildHTTPCookie constructs an *http.Cookie from the encoded value and options.
func buildHTTPCookie(name, encodedValue string, opts CookieOptions) *http.Cookie {
	c := &http.Cookie{
		Name:     name,
		Value:    encodedValue,
		Path:     opts.Path,
		Domain:   opts.Domain,
		Secure:   opts.Secure,
		HttpOnly: !opts.DisableHttpOnly,
		SameSite: opts.SameSite,
	}
	if opts.MaxAge > 0 {
		c.MaxAge = opts.MaxAge
		c.Expires = time.Now().Add(time.Duration(opts.MaxAge) * time.Second)
	} else if opts.MaxAge < 0 {
		c.MaxAge = -1
		c.Expires = time.Unix(1, 0)
	}
	return c
}

// --- Signing ---

// computeMAC returns hex(HMAC-SHA256(secret, "name|payload|timestamp")).
func computeMAC(name, payload, timestamp string, secret []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(name))
	h.Write([]byte("|"))
	h.Write([]byte(payload))
	h.Write([]byte("|"))
	h.Write([]byte(timestamp))
	return hex.EncodeToString(h.Sum(nil))
}

// encodeSigned returns "payload|timestamp|mac".
// The payload must already be base64url-encoded by the caller.
func encodeSigned(name, payload string, secret []byte) string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := computeMAC(name, payload, ts, secret)
	return payload + "|" + ts + "|" + mac
}

// encodeSignedAt is like encodeSigned but accepts an explicit timestamp.
// Used by tests to construct deterministic cookies without sleeps.
func encodeSignedAt(name, payload string, secret []byte, unixTime int64) string {
	ts := strconv.FormatInt(unixTime, 10)
	mac := computeMAC(name, payload, ts, secret)
	return payload + "|" + ts + "|" + mac
}

// decodeSigned splits on "|", verifies the MAC via constant-time comparison
// (hmac.Equal), checks server-side expiry, and returns the raw payload
// (still base64url-encoded).
func decodeSigned(name, raw string, secret []byte, maxAge int) (string, error) {
	parts := strings.SplitN(raw, "|", 4)
	if len(parts) != 3 {
		return "", ErrInvalidCookieFormat
	}

	payload, tsStr, storedMAC := parts[0], parts[1], parts[2]

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return "", ErrInvalidCookieFormat
	}

	expected := computeMAC(name, payload, tsStr, secret)
	if !hmac.Equal([]byte(storedMAC), []byte(expected)) {
		return "", ErrInvalidCookieSignature
	}

	if maxAge > 0 && time.Now().Unix()-ts > int64(maxAge) {
		return "", ErrCookieExpired
	}

	return payload, nil
}

// --- Key Derivation ---

// deriveKeys performs deterministic, domain-separated subkey derivation via
// HMAC-SHA256. The master key must be exactly 32 bytes. The derived encKey
// and macKey are cryptographically independent — a collision between them
// is negligible, not impossible.
func deriveKeys(masterKey []byte) (encKey, macKey []byte, err error) {
	if len(masterKey) != 32 {
		return nil, nil, ErrInvalidEncryptionKey
	}

	encH := hmac.New(sha256.New, masterKey)
	encH.Write([]byte("aarv-cookie-enc"))
	encKey = encH.Sum(nil)

	macH := hmac.New(sha256.New, masterKey)
	macH.Write([]byte("aarv-cookie-mac"))
	macKey = macH.Sum(nil)

	return encKey, macKey, nil
}

// --- Encryption ---

// encryptValue encrypts plaintext with AES-256-GCM and returns a
// base64url-encoded string of nonce || ciphertext || tag.
func encryptValue(plaintext string, encKey []byte) (string, error) {
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", ErrCookieDecryptFailed
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrCookieDecryptFailed
	}

	nonce := make([]byte, gcmNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", ErrCookieDecryptFailed
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// decryptValue decodes a base64url-encoded string and decrypts it with
// AES-256-GCM.
//
// Note: encrypted cookies have significant size overhead (base64 encoding +
// 12-byte nonce + 16-byte GCM tag + MAC + timestamp). Callers should be
// aware of the ~4KB browser cookie size limit.
func decryptValue(encoded string, encKey []byte) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrCookieDecryptFailed
	}
	if len(data) < gcmNonceSize {
		return "", ErrCookieDecryptFailed
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", ErrCookieDecryptFailed
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrCookieDecryptFailed
	}

	nonce, ciphertext := data[:gcmNonceSize], data[gcmNonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrCookieDecryptFailed
	}

	return string(plaintext), nil
}
