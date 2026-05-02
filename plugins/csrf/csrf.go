// Package csrf provides Cross-Site Request Forgery protection middleware
// for the aarv framework using the double-submit cookie pattern.
//
// On a request with a method in SafeMethods (default GET, HEAD, OPTIONS,
// TRACE) the middleware ensures a CSRF token cookie is present, issuing a
// fresh one if not. On a request with any other method the middleware
// requires the cookie to be present and to match a token submitted via
// the configured HeaderName (default X-CSRF-Token) or, optionally, via a
// form field.
//
// # Cookie ergonomics — HttpOnly default
//
// CookieHTTPOnly defaults to false. The classic double-submit pattern
// requires JS-side code to copy the cookie value into the X-CSRF-Token
// request header on each unsafe request, which it cannot do for an
// HttpOnly cookie. Server-rendered apps that prefer not to expose the
// cookie to JS can set CookieHTTPOnly: true and inject the token into
// HTML/meta/form via csrf.Token(c). Both patterns work; the default is
// the API/SPA-ergonomic one.
//
// HttpOnly is a meaningful defense for *session* cookies but provides
// little value for CSRF tokens: an attacker with XSS can already invoke
// fetch() with cookies attached and synthesize the X-CSRF-Token header
// from a sibling readable token; the cookie itself is not the secret.
//
// # SafeMethods nil-vs-empty contract
//
//	SafeMethods: nil                      // defaults: GET, HEAD, OPTIONS, TRACE bypass CSRF
//	SafeMethods: []string{http.MethodGet} // only GET bypasses
//	SafeMethods: []string{}               // every method requires a valid token
//
// New substitutes built-in defaults only when the slice is nil; an empty
// non-nil slice is honored verbatim.
package csrf

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
)

// randReader is the source of cryptographic randomness used for token
// generation. Indirected through a package variable so tests can swap
// in a failing reader to exercise the error path without touching
// crypto/rand.Reader (which Go 1.26+ treats as fatal-on-failure).
var randReader io.Reader = rand.Reader

// ErrorHandler builds the rejection response. Defaults to a JSON 403.
type ErrorHandler func(c *aarv.Context, reason string) error

// Skipper bypasses the middleware when it returns true. OR-combined with
// SkipPaths.
type Skipper func(*aarv.Context) bool

// Config holds CSRF middleware configuration. Pass through New; New
// panics on invalid TokenLength only.
type Config struct {
	CookieName     string
	HeaderName     string
	FormField      string
	TokenLength    int
	CookiePath     string
	CookieDomain   string
	CookieMaxAge   int
	CookieSecure   bool
	CookieHTTPOnly bool
	CookieSameSite http.SameSite

	// SafeMethods follows the nil-vs-empty contract above.
	SafeMethods []string

	Skipper      Skipper
	SkipPaths    []string
	ErrorHandler ErrorHandler
}

// DefaultConfig returns a Config wired for an API-first SPA app.
func DefaultConfig() Config {
	return Config{
		CookieName:     "_csrf",
		HeaderName:     "X-CSRF-Token",
		TokenLength:    32,
		CookiePath:     "/",
		CookieMaxAge:   12 * 60 * 60, // 12h in seconds
		CookieSecure:   true,
		CookieHTTPOnly: false,
		CookieSameSite: http.SameSiteLaxMode,
		// SafeMethods nil → defaults applied in New
	}
}

// tokenContextKey is the *aarv.Context key under which the issued token
// is stored. Hardcoded so Token always finds it (mirrors apikey/jwt).
const tokenContextKey = "csrfToken"

type normalized struct {
	cookieName     string
	headerName     string
	formField      string
	tokenLength    int
	cookiePath     string
	cookieDomain   string
	cookieMaxAge   int
	cookieSecure   bool
	cookieHTTPOnly bool
	cookieSameSite http.SameSite

	safeMethods map[string]struct{}

	skipper   Skipper
	skipPaths map[string]struct{}
	errFn     ErrorHandler
}

// New constructs CSRF middleware. Panics on TokenLength < 16 (a 16-byte
// raw token is the minimum that produces a 22-char base64-encoded value;
// shorter tokens are too easy to brute-force).
func New(cfg Config) aarv.Middleware {
	n := normalize(cfg)

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if n.shouldSkip(c) {
				return next(c)
			}
			method := c.Method()
			if _, isSafe := n.safeMethods[method]; isSafe {
				return n.handleSafeNative(c, next)
			}
			return n.handleUnsafeNative(c, next)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, hasCtx := aarv.FromRequest(r)
			if hasCtx && n.shouldSkip(c) {
				next.ServeHTTP(w, r)
				return
			}
			if _, isSafe := n.safeMethods[r.Method]; isSafe {
				n.handleSafeStdlib(w, r, c, hasCtx, next)
				return
			}
			n.handleUnsafeStdlib(w, r, c, hasCtx, next)
		})
	})

	return aarv.RegisterNativeMiddleware(m, native)
}

func normalize(cfg Config) *normalized {
	if cfg.TokenLength == 0 {
		cfg.TokenLength = 32
	}
	if cfg.TokenLength < 16 {
		panic("csrf: TokenLength must be >= 16 bytes")
	}
	if cfg.CookieName == "" {
		cfg.CookieName = "_csrf"
	}
	if cfg.HeaderName == "" {
		cfg.HeaderName = "X-CSRF-Token"
	}
	if cfg.CookiePath == "" {
		cfg.CookiePath = "/"
	}
	if cfg.CookieMaxAge == 0 {
		cfg.CookieMaxAge = 12 * 60 * 60
	}
	if cfg.CookieSameSite == 0 {
		cfg.CookieSameSite = http.SameSiteLaxMode
	}

	// nil-vs-empty: nil means defaults; empty non-nil means no bypass.
	var safeMethods map[string]struct{}
	if cfg.SafeMethods == nil {
		safeMethods = map[string]struct{}{
			http.MethodGet:     {},
			http.MethodHead:    {},
			http.MethodOptions: {},
			http.MethodTrace:   {},
		}
	} else {
		safeMethods = make(map[string]struct{}, len(cfg.SafeMethods))
		for _, m := range cfg.SafeMethods {
			safeMethods[m] = struct{}{}
		}
	}

	skipPaths := map[string]struct{}{}
	for _, p := range cfg.SkipPaths {
		skipPaths[p] = struct{}{}
	}

	return &normalized{
		cookieName:     cfg.CookieName,
		headerName:     cfg.HeaderName,
		formField:      cfg.FormField,
		tokenLength:    cfg.TokenLength,
		cookiePath:     cfg.CookiePath,
		cookieDomain:   cfg.CookieDomain,
		cookieMaxAge:   cfg.CookieMaxAge,
		cookieSecure:   cfg.CookieSecure,
		cookieHTTPOnly: cfg.CookieHTTPOnly,
		cookieSameSite: cfg.CookieSameSite,
		safeMethods:    safeMethods,
		skipper:        cfg.Skipper,
		skipPaths:      skipPaths,
		errFn:          cfg.ErrorHandler,
	}
}

func (n *normalized) shouldSkip(c *aarv.Context) bool {
	if _, ok := n.skipPaths[c.Path()]; ok {
		return true
	}
	if n.skipper != nil && n.skipper(c) {
		return true
	}
	return false
}

// --- safe methods: ensure cookie issued, expose token via Context ---

func (n *normalized) handleSafeNative(c *aarv.Context, next aarv.HandlerFunc) error {
	token, err := n.ensureToken(c.Request(), c.Response())
	if err != nil {
		return aarv.ErrInternal(err).WithDetail("csrf: failed to issue token")
	}
	c.Set(tokenContextKey, token)
	return next(c)
}

func (n *normalized) handleSafeStdlib(w http.ResponseWriter, r *http.Request, c *aarv.Context, hasCtx bool, next http.Handler) {
	token, err := n.ensureToken(r, w)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "csrf: failed to issue token", "")
		return
	}
	if hasCtx {
		c.Set(tokenContextKey, token)
	}
	next.ServeHTTP(w, r)
}

// --- unsafe methods: validate header/form token against cookie ---

func (n *normalized) handleUnsafeNative(c *aarv.Context, next aarv.HandlerFunc) error {
	cookie, err := c.Cookie(n.cookieName)
	if err != nil || cookie == nil || cookie.Value == "" {
		return n.rejectNative(c, "missing CSRF cookie")
	}
	submitted := c.Header(n.headerName)
	if submitted == "" && n.formField != "" {
		submitted = c.Request().FormValue(n.formField)
	}
	if submitted == "" {
		return n.rejectNative(c, "missing CSRF token")
	}
	if !tokensEqual(cookie.Value, submitted) {
		return n.rejectNative(c, "CSRF token mismatch")
	}
	c.Set(tokenContextKey, cookie.Value)
	return next(c)
}

func (n *normalized) handleUnsafeStdlib(w http.ResponseWriter, r *http.Request, c *aarv.Context, hasCtx bool, next http.Handler) {
	cookie, err := r.Cookie(n.cookieName)
	if err != nil || cookie == nil || cookie.Value == "" {
		n.rejectStdlib(w, c, hasCtx, "missing CSRF cookie")
		return
	}
	submitted := r.Header.Get(n.headerName)
	if submitted == "" && n.formField != "" {
		submitted = r.FormValue(n.formField)
	}
	if submitted == "" {
		n.rejectStdlib(w, c, hasCtx, "missing CSRF token")
		return
	}
	if !tokensEqual(cookie.Value, submitted) {
		n.rejectStdlib(w, c, hasCtx, "CSRF token mismatch")
		return
	}
	if hasCtx {
		c.Set(tokenContextKey, cookie.Value)
	}
	next.ServeHTTP(w, r)
}

// --- token issue & cookie management ---

// ensureToken returns the existing CSRF token cookie value when present,
// or issues a fresh one and writes it as a Set-Cookie header.
func (n *normalized) ensureToken(r *http.Request, w http.ResponseWriter) (string, error) {
	if cookie, err := r.Cookie(n.cookieName); err == nil && cookie != nil && cookie.Value != "" {
		return cookie.Value, nil
	}
	token, err := generateToken(n.tokenLength)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     n.cookieName,
		Value:    token,
		Path:     n.cookiePath,
		Domain:   n.cookieDomain,
		MaxAge:   n.cookieMaxAge,
		Expires:  time.Now().Add(time.Duration(n.cookieMaxAge) * time.Second),
		Secure:   n.cookieSecure,
		HttpOnly: n.cookieHTTPOnly,
		SameSite: n.cookieSameSite,
	})
	return token, nil
}

func generateToken(rawLen int) (string, error) {
	buf := make([]byte, rawLen)
	if _, err := io.ReadFull(randReader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// tokensEqual decodes both tokens to bytes and compares with
// constant-time equality. Length-prefixed comparison via decode-then-
// compare closes the length side channel.
func tokensEqual(a, b string) bool {
	ab, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil {
		return false
	}
	bb, err := base64.RawURLEncoding.DecodeString(b)
	if err != nil {
		return false
	}
	if len(ab) != len(bb) {
		return false
	}
	return subtle.ConstantTimeCompare(ab, bb) == 1
}

// Token returns the CSRF token associated with the current request, or
// "" if the middleware has not run for it. Useful for server-rendered
// templates that need to inject the token into HTML/meta/form fields.
func Token(c *aarv.Context) string {
	if c == nil {
		return ""
	}
	v, ok := c.Get(tokenContextKey)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// --- rejection paths ---

func (n *normalized) rejectNative(c *aarv.Context, reason string) error {
	if n.errFn != nil {
		return n.errFn(c, reason)
	}
	return aarv.NewError(http.StatusForbidden, "forbidden", "CSRF token validation failed").WithDetail(reason)
}

func (n *normalized) rejectStdlib(w http.ResponseWriter, c *aarv.Context, hasCtx bool, reason string) {
	if hasCtx && n.errFn != nil {
		if err := n.errFn(c, reason); err != nil {
			writeJSONError(w, http.StatusForbidden, "CSRF token validation failed", c.RequestID())
		}
		return
	}
	requestID := ""
	if hasCtx {
		requestID = c.RequestID()
	}
	writeJSONError(w, http.StatusForbidden, "CSRF token validation failed", requestID)
}

// --- error response helpers ---

type errorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func writeJSONError(w http.ResponseWriter, status int, message, requestID string) {
	body := errorBody{
		Error:     codeForStatus(status),
		Message:   message,
		RequestID: requestID,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusForbidden:
		return "forbidden"
	default:
		return http.StatusText(status)
	}
}
