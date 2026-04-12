package aarv

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

var testSecret = []byte("test-secret-key-for-signing-1234")

func newSignedCookieContext(t *testing.T, cookieName, cookieValue string) (*Context, *httptest.ResponseRecorder) {
	t.Helper()
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookieName != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
	}
	ctx, rec := newAppContext(app, req)
	t.Cleanup(func() { app.ReleaseContext(ctx) })
	return ctx, rec
}

// --- Signing Tests ---

func TestSecureCookieSignVerifyHappyPath(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	if err := ctx.SetSecureCookie("session", "user123", testSecret); err != nil {
		t.Fatal(err)
	}

	// Extract the cookie from response, put it on a new request
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" {
		t.Fatalf("expected 1 session cookie, got %v", cookies)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookies[0])
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	val, err := ctx2.SecureCookie("session", testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if val != "user123" {
		t.Fatalf("expected user123, got %q", val)
	}
}

func TestSecureCookieValueWithPipe(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	original := "value|with|pipes"
	_ = ctx.SetSecureCookie("test", original, testSecret)

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(rec.Result().Cookies()[0])
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	val, err := ctx2.SecureCookie("test", testSecret)
	if err != nil || val != original {
		t.Fatalf("expected %q, got %q err=%v", original, val, err)
	}
}

func TestSecureCookieValueWithUnicode(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	original := "こんにちは世界"
	_ = ctx.SetSecureCookie("test", original, testSecret)

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(rec.Result().Cookies()[0])
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	val, err := ctx2.SecureCookie("test", testSecret)
	if err != nil || val != original {
		t.Fatalf("expected %q, got %q err=%v", original, val, err)
	}
}

func TestSecureCookieEmptyValue(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.SetSecureCookie("test", "", testSecret)

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(rec.Result().Cookies()[0])
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	val, err := ctx2.SecureCookie("test", testSecret)
	if err != nil || val != "" {
		t.Fatalf("expected empty string, got %q err=%v", val, err)
	}
}

func TestSecureCookieTamperedValue(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte("original"))
	signed := encodeSigned("test", payload, testSecret)
	// Tamper the base64 payload part
	parts := strings.SplitN(signed, "|", 3)
	parts[0] = base64.RawURLEncoding.EncodeToString([]byte("tampered"))
	tampered := strings.Join(parts, "|")

	ctx, _ := newSignedCookieContext(t, "test", tampered)
	_, err := ctx.SecureCookie("test", testSecret)
	if err != ErrInvalidCookieSignature {
		t.Fatalf("expected ErrInvalidCookieSignature, got %v", err)
	}
}

func TestSecureCookieTamperedSignature(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte("value"))
	signed := encodeSigned("test", payload, testSecret)
	parts := strings.SplitN(signed, "|", 3)
	parts[2] = "deadbeef" + parts[2][8:] // corrupt signature
	tampered := strings.Join(parts, "|")

	ctx, _ := newSignedCookieContext(t, "test", tampered)
	_, err := ctx.SecureCookie("test", testSecret)
	if err != ErrInvalidCookieSignature {
		t.Fatalf("expected ErrInvalidCookieSignature, got %v", err)
	}
}

func TestSecureCookieTamperedTimestamp(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte("value"))
	signed := encodeSigned("test", payload, testSecret)
	parts := strings.SplitN(signed, "|", 3)
	parts[1] = "9999999999" // change timestamp
	tampered := strings.Join(parts, "|")

	ctx, _ := newSignedCookieContext(t, "test", tampered)
	_, err := ctx.SecureCookie("test", testSecret)
	if err != ErrInvalidCookieSignature {
		t.Fatalf("expected ErrInvalidCookieSignature, got %v", err)
	}
}

func TestSecureCookieWrongSecret(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte("value"))
	signed := encodeSigned("test", payload, testSecret)

	ctx, _ := newSignedCookieContext(t, "test", signed)
	_, err := ctx.SecureCookie("test", []byte("wrong-secret"))
	if err != ErrInvalidCookieSignature {
		t.Fatalf("expected ErrInvalidCookieSignature, got %v", err)
	}
}

func TestSecureCookieExpired(t *testing.T) {
	// Construct a cookie signed 2 hours ago
	payload := base64.RawURLEncoding.EncodeToString([]byte("value"))
	oldTime := time.Now().Unix() - 7200
	signed := encodeSignedAt("test", payload, testSecret, oldTime)

	ctx, _ := newSignedCookieContext(t, "test", signed)
	_, err := ctx.SecureCookie("test", testSecret, 60) // 60s maxAge
	if err != ErrCookieExpired {
		t.Fatalf("expected ErrCookieExpired, got %v", err)
	}
}

func TestSecureCookieNoExpiryCheck(t *testing.T) {
	// Construct a cookie signed 2 hours ago
	payload := base64.RawURLEncoding.EncodeToString([]byte("value"))
	oldTime := time.Now().Unix() - 7200
	signed := encodeSignedAt("test", payload, testSecret, oldTime)

	ctx, _ := newSignedCookieContext(t, "test", signed)
	val, err := ctx.SecureCookie("test", testSecret) // no maxAge
	if err != nil || val != "value" {
		t.Fatalf("expected value without expiry check, got %q err=%v", val, err)
	}
}

func TestSecureCookieMalformedZeroParts(t *testing.T) {
	ctx, _ := newSignedCookieContext(t, "test", "nopipes")
	_, err := ctx.SecureCookie("test", testSecret)
	if err != ErrInvalidCookieFormat {
		t.Fatalf("expected ErrInvalidCookieFormat, got %v", err)
	}
}

func TestSecureCookieMalformedTwoParts(t *testing.T) {
	ctx, _ := newSignedCookieContext(t, "test", "a|b")
	_, err := ctx.SecureCookie("test", testSecret)
	if err != ErrInvalidCookieFormat {
		t.Fatalf("expected ErrInvalidCookieFormat, got %v", err)
	}
}

func TestSecureCookieNonNumericTimestamp(t *testing.T) {
	ctx, _ := newSignedCookieContext(t, "test", "payload|notanumber|sig")
	_, err := ctx.SecureCookie("test", testSecret)
	if err != ErrInvalidCookieFormat {
		t.Fatalf("expected ErrInvalidCookieFormat, got %v", err)
	}
}

func TestSecureCookieMissing(t *testing.T) {
	ctx, _ := newSignedCookieContext(t, "", "")
	_, err := ctx.SecureCookie("missing", testSecret)
	if err != http.ErrNoCookie {
		t.Fatalf("expected http.ErrNoCookie, got %v", err)
	}
}

func TestSecureCookieCrossNameReplay(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte("value"))
	signed := encodeSigned("cookie-a", payload, testSecret)

	// Try to read it as cookie-b
	ctx, _ := newSignedCookieContext(t, "cookie-b", signed)
	_, err := ctx.SecureCookie("cookie-b", testSecret)
	if err != ErrInvalidCookieSignature {
		t.Fatalf("expected ErrInvalidCookieSignature for cross-name replay, got %v", err)
	}
}

func TestSecureCookieEmptySecretOnWrite(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	err := ctx.SetSecureCookie("test", "value", nil)
	if err != ErrEmptySecret {
		t.Fatalf("expected ErrEmptySecret, got %v", err)
	}
	err = ctx.SetSecureCookie("test", "value", []byte{})
	if err != ErrEmptySecret {
		t.Fatalf("expected ErrEmptySecret for empty slice, got %v", err)
	}
}

func TestSecureCookieEmptySecretOnRead(t *testing.T) {
	ctx, _ := newSignedCookieContext(t, "test", "anything")
	_, err := ctx.SecureCookie("test", nil)
	if err != ErrEmptySecret {
		t.Fatalf("expected ErrEmptySecret, got %v", err)
	}
	_, err = ctx.SecureCookie("test", []byte{})
	if err != ErrEmptySecret {
		t.Fatalf("expected ErrEmptySecret for empty slice, got %v", err)
	}
}

// --- Encryption Tests ---

var testKey = []byte("0123456789abcdef0123456789abcdef") // 32 bytes

func TestEncryptedCookieHappyPath(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	if err := ctx.SetEncryptedCookie("secret", "classified", testKey); err != nil {
		t.Fatal(err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookies[0])
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	val, err := ctx2.EncryptedCookie("secret", testKey)
	if err != nil {
		t.Fatal(err)
	}
	if val != "classified" {
		t.Fatalf("expected classified, got %q", val)
	}
}

func TestEncryptedCookieUnicode(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	original := "日本語テスト🔐"
	_ = ctx.SetEncryptedCookie("test", original, testKey)

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(rec.Result().Cookies()[0])
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	val, err := ctx2.EncryptedCookie("test", testKey)
	if err != nil || val != original {
		t.Fatalf("expected %q, got %q err=%v", original, val, err)
	}
}

func TestEncryptedCookieEmptyValue(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.SetEncryptedCookie("test", "", testKey)

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(rec.Result().Cookies()[0])
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	val, err := ctx2.EncryptedCookie("test", testKey)
	if err != nil || val != "" {
		t.Fatalf("expected empty, got %q err=%v", val, err)
	}
}

func TestEncryptedCookieTamperedPayload(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.SetEncryptedCookie("test", "secret-data", testKey)
	cookie := rec.Result().Cookies()[0]

	// Tamper the encrypted payload (first part before |)
	parts := strings.SplitN(cookie.Value, "|", 3)
	if len(parts) == 3 {
		parts[0] = "AAAA" + parts[0][4:]
		cookie.Value = strings.Join(parts, "|")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	_, err := ctx2.EncryptedCookie("test", testKey)
	if err != ErrInvalidCookieSignature {
		t.Fatalf("expected ErrInvalidCookieSignature, got %v", err)
	}
}

func TestEncryptedCookieWrongKeyCorrectLength(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.SetEncryptedCookie("test", "data", testKey)

	wrongKey := []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx") // 32 bytes, different
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(rec.Result().Cookies()[0])
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	_, err := ctx2.EncryptedCookie("test", wrongKey)
	// MAC uses derived macKey from wrong master → signature mismatch
	if err != ErrInvalidCookieSignature {
		t.Fatalf("expected ErrInvalidCookieSignature, got %v", err)
	}
}

func TestEncryptedCookieInvalidKeyLengthOnWrite(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, _ := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	err := ctx.SetEncryptedCookie("test", "data", []byte("short"))
	if err != ErrInvalidEncryptionKey {
		t.Fatalf("expected ErrInvalidEncryptionKey, got %v", err)
	}
}

func TestEncryptedCookieInvalidKeyLengthOnRead(t *testing.T) {
	ctx, _ := newSignedCookieContext(t, "test", "anything")
	_, err := ctx.EncryptedCookie("test", []byte("short"))
	if err != ErrInvalidEncryptionKey {
		t.Fatalf("expected ErrInvalidEncryptionKey, got %v", err)
	}
}

func TestEncryptedCookieNonceTooShort(t *testing.T) {
	// Construct a cookie where the encrypted payload is valid base64url
	// but too short for a nonce, signed with the correct macKey
	_, macKey, _ := deriveKeys(testKey)
	shortPayload := base64.RawURLEncoding.EncodeToString([]byte("short")) // < 12 bytes
	signed := encodeSigned("test", shortPayload, macKey)

	ctx, _ := newSignedCookieContext(t, "test", signed)
	_, err := ctx.EncryptedCookie("test", testKey)
	if err != ErrCookieDecryptFailed {
		t.Fatalf("expected ErrCookieDecryptFailed, got %v", err)
	}
}

func TestEncryptedCookieCrossNameReplay(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.SetEncryptedCookie("cookie-a", "secret", testKey)
	cookie := rec.Result().Cookies()[0]
	cookie.Name = "cookie-b" // replay as different name

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	ctx2, _ := newAppContext(app, req2)
	defer app.ReleaseContext(ctx2)

	_, err := ctx2.EncryptedCookie("cookie-b", testKey)
	if err != ErrInvalidCookieSignature {
		t.Fatalf("expected ErrInvalidCookieSignature for cross-name replay, got %v", err)
	}
}

func TestEncryptedCookieServerMaxAge(t *testing.T) {
	_, macKey, _ := deriveKeys(testKey)
	encKey, _, _ := deriveKeys(testKey)

	encrypted, _ := encryptValue("data", encKey)
	oldTime := time.Now().Unix() - 7200
	signed := encodeSignedAt("test", encrypted, macKey, oldTime)

	ctx, _ := newSignedCookieContext(t, "test", signed)
	_, err := ctx.EncryptedCookie("test", testKey, 60)
	if err != ErrCookieExpired {
		t.Fatalf("expected ErrCookieExpired, got %v", err)
	}
}

func TestEncryptedCookieMissing(t *testing.T) {
	ctx, _ := newSignedCookieContext(t, "", "")
	_, err := ctx.EncryptedCookie("missing", testKey)
	if err != http.ErrNoCookie {
		t.Fatalf("expected http.ErrNoCookie, got %v", err)
	}
}

// --- Key Derivation Tests ---

func TestDeriveKeysDistinct(t *testing.T) {
	encKey, macKey, err := deriveKeys(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(encKey) == string(macKey) {
		t.Fatal("expected distinct encKey and macKey")
	}
	if len(encKey) != 32 || len(macKey) != 32 {
		t.Fatalf("expected 32-byte keys, got enc=%d mac=%d", len(encKey), len(macKey))
	}
}

func TestDeriveKeysDeterministic(t *testing.T) {
	enc1, mac1, _ := deriveKeys(testKey)
	enc2, mac2, _ := deriveKeys(testKey)
	if string(enc1) != string(enc2) || string(mac1) != string(mac2) {
		t.Fatal("expected deterministic derivation")
	}
}

func TestDeriveKeysRejectsWrongLength(t *testing.T) {
	_, _, err := deriveKeys([]byte("short"))
	if err != ErrInvalidEncryptionKey {
		t.Fatalf("expected ErrInvalidEncryptionKey, got %v", err)
	}
	_, _, err = deriveKeys([]byte("this-is-way-too-long-for-a-32-byte-key-requirement"))
	if err != ErrInvalidEncryptionKey {
		t.Fatalf("expected ErrInvalidEncryptionKey, got %v", err)
	}
}

// --- CookieOptions Tests ---

func TestCookieOptionsApplied(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.SetSecureCookie("test", "value", testSecret, CookieOptions{
		Path:     "/api",
		Domain:   "example.com",
		MaxAge:   3600,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		// DisableHttpOnly defaults to false → HttpOnly=true
	})

	cookie := rec.Result().Cookies()[0]
	if cookie.Path != "/api" {
		t.Fatalf("expected path /api, got %q", cookie.Path)
	}
	if cookie.Domain != "example.com" {
		t.Fatalf("expected domain example.com, got %q", cookie.Domain)
	}
	if cookie.MaxAge != 3600 {
		t.Fatalf("expected maxage 3600, got %d", cookie.MaxAge)
	}
	if !cookie.Secure {
		t.Fatal("expected Secure=true")
	}
	if !cookie.HttpOnly {
		t.Fatal("expected HttpOnly=true")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("expected SameSiteStrict, got %d", cookie.SameSite)
	}
	if cookie.Expires.IsZero() {
		t.Fatal("expected Expires to be set when MaxAge > 0")
	}
}

func TestCookieOptionsDefaults(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.SetSecureCookie("test", "value", testSecret) // no opts

	cookie := rec.Result().Cookies()[0]
	if cookie.Path != "/" {
		t.Fatalf("expected default path /, got %q", cookie.Path)
	}
	if !cookie.HttpOnly {
		t.Fatal("expected default HttpOnly=true")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected default SameSiteLax, got %d", cookie.SameSite)
	}
}

func TestCookieOptionsDisableHttpOnly(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	_ = ctx.SetSecureCookie("test", "value", testSecret, CookieOptions{
		DisableHttpOnly: true,
	})

	cookie := rec.Result().Cookies()[0]
	if cookie.HttpOnly {
		t.Fatal("expected HttpOnly=false when DisableHttpOnly=true")
	}
}

func TestCookieOptionsPartialOverrideKeepsDefaults(t *testing.T) {
	app := New(WithBanner(false), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, rec := newAppContext(app, req)
	defer app.ReleaseContext(ctx)

	// Caller only sets Secure — HttpOnly and SameSite should keep defaults
	_ = ctx.SetSecureCookie("test", "value", testSecret, CookieOptions{
		Secure: true,
	})

	cookie := rec.Result().Cookies()[0]
	if !cookie.Secure {
		t.Fatal("expected Secure=true")
	}
	if !cookie.HttpOnly {
		t.Fatal("expected HttpOnly=true (default preserved)")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected SameSiteLax (default preserved), got %d", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Fatalf("expected Path=/ (default preserved), got %q", cookie.Path)
	}
}

func TestCookieOptionsMaxAgePositiveSetsExpires(t *testing.T) {
	c := buildHTTPCookie("test", "val", CookieOptions{Path: "/", MaxAge: 300})
	if c.MaxAge != 300 {
		t.Fatalf("expected MaxAge=300, got %d", c.MaxAge)
	}
	if c.Expires.IsZero() {
		t.Fatal("expected Expires to be set for MaxAge > 0")
	}
	// Expires should be roughly 300s from now
	diff := time.Until(c.Expires).Seconds()
	if diff < 298 || diff > 302 {
		t.Fatalf("expected Expires ~300s from now, got %.0f", diff)
	}
}

func TestCookieOptionsMaxAgeNegativeDeletes(t *testing.T) {
	c := buildHTTPCookie("test", "val", CookieOptions{Path: "/", MaxAge: -1})
	if c.MaxAge != -1 {
		t.Fatalf("expected MaxAge=-1, got %d", c.MaxAge)
	}
	if !c.Expires.Equal(time.Unix(1, 0)) {
		t.Fatalf("expected Expires=epoch, got %v", c.Expires)
	}
}

func TestCookieOptionsMaxAgeZeroSession(t *testing.T) {
	c := buildHTTPCookie("test", "val", CookieOptions{Path: "/", MaxAge: 0})
	if c.MaxAge != 0 {
		t.Fatalf("expected MaxAge=0, got %d", c.MaxAge)
	}
	if !c.Expires.IsZero() {
		t.Fatalf("expected zero Expires for session cookie, got %v", c.Expires)
	}
}

// --- Helper Direct Tests ---

func TestComputeMACDeterministic(t *testing.T) {
	mac1 := computeMAC("name", "payload", "12345", testSecret)
	mac2 := computeMAC("name", "payload", "12345", testSecret)
	if mac1 != mac2 {
		t.Fatal("expected deterministic MAC")
	}
	// Different inputs produce different MACs
	mac3 := computeMAC("other", "payload", "12345", testSecret)
	if mac1 == mac3 {
		t.Fatal("expected different MAC for different name")
	}
}

func TestEncodeDecodeSignedRoundTrip(t *testing.T) {
	payload := "test-payload"
	encoded := encodeSigned("cookie", payload, testSecret)
	decoded, err := decodeSigned("cookie", encoded, testSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != payload {
		t.Fatalf("expected %q, got %q", payload, decoded)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	encKey, _, _ := deriveKeys(testKey)

	encrypted, err := encryptValue("hello secure world", encKey)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := decryptValue(encrypted, encKey)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "hello secure world" {
		t.Fatalf("expected hello secure world, got %q", decrypted)
	}
}

func TestDecryptValueBadBase64(t *testing.T) {
	encKey, _, _ := deriveKeys(testKey)
	_, err := decryptValue("not!valid!base64!!!", encKey)
	if err != ErrCookieDecryptFailed {
		t.Fatalf("expected ErrCookieDecryptFailed, got %v", err)
	}
}

func TestDecryptValueShortData(t *testing.T) {
	encKey, _, _ := deriveKeys(testKey)
	short := base64.RawURLEncoding.EncodeToString([]byte("tiny"))
	_, err := decryptValue(short, encKey)
	if err != ErrCookieDecryptFailed {
		t.Fatalf("expected ErrCookieDecryptFailed, got %v", err)
	}
}

func TestDecryptValueCorruptedCiphertext(t *testing.T) {
	encKey, _, _ := deriveKeys(testKey)
	encrypted, _ := encryptValue("original", encKey)
	data, _ := base64.RawURLEncoding.DecodeString(encrypted)
	// Corrupt a byte in the ciphertext (after nonce)
	data[gcmNonceSize+1] ^= 0xFF
	corrupted := base64.RawURLEncoding.EncodeToString(data)
	_, err := decryptValue(corrupted, encKey)
	if err != ErrCookieDecryptFailed {
		t.Fatalf("expected ErrCookieDecryptFailed, got %v", err)
	}
}

func TestEncodeSignedAtDeterministic(t *testing.T) {
	payload := "test"
	ts := int64(1700000000)
	s1 := encodeSignedAt("name", payload, testSecret, ts)
	s2 := encodeSignedAt("name", payload, testSecret, ts)
	if s1 != s2 {
		t.Fatal("expected deterministic output")
	}
	// Verify it contains the expected timestamp
	parts := strings.SplitN(s1, "|", 3)
	if parts[1] != strconv.FormatInt(ts, 10) {
		t.Fatalf("expected timestamp %d, got %s", ts, parts[1])
	}
}

func TestResolveCookieOptionsEmptyPath(t *testing.T) {
	// When caller provides opts with empty Path, it should default to "/"
	opts := resolveCookieOptions([]CookieOptions{{Secure: true}})
	if opts.Path != "/" {
		t.Fatalf("expected path /, got %q", opts.Path)
	}
	if !opts.Secure {
		t.Fatal("expected Secure=true preserved")
	}
}

func TestSecureCookieBase64DecodeError(t *testing.T) {
	// Construct a signed cookie where the payload is not valid base64
	// but the MAC is correct (sign it properly with invalid payload)
	badPayload := "not-valid-base64!!!"
	signed := encodeSigned("test", badPayload, testSecret)

	ctx, _ := newSignedCookieContext(t, "test", signed)
	_, err := ctx.SecureCookie("test", testSecret)
	if err != ErrInvalidCookieFormat {
		t.Fatalf("expected ErrInvalidCookieFormat for bad base64 payload, got %v", err)
	}
}
