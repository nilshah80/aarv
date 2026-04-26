// Package basicauth provides HTTP Basic authentication middleware (RFC 7617)
// for the aarv framework.
//
// The middleware parses the Authorization request header, base64-decodes the
// credentials, splits username and password on the first colon, then delegates
// verification to a user-supplied Validator. On success the validator's
// identity value is stored on the Context and reachable through From or
// FromContext. On failure the middleware responds with 401 Unauthorized and a
// WWW-Authenticate challenge so user agents can prompt for credentials.
//
// # Security
//
// Basic Auth transmits credentials as base64 (effectively plaintext). Always
// terminate it on TLS — never on a cleartext listener. The plugin does not
// enforce TLS itself; deployment is responsible.
//
// The plugin does not compare the password itself: that comparison happens in
// the user-supplied Validator. The bundled StaticCreds helper performs
// constant-time comparison against an in-memory map; custom validators that
// hit a database should use a password hashing library (bcrypt, argon2) which
// performs constant-time verification internally.
package basicauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/nilshah80/aarv"
)

type contextKey struct{}

// identityStoreKey is the fixed key under which the middleware stores the
// authenticated identity on *aarv.Context. Hardcoded so From always succeeds
// when auth ran.
const identityStoreKey = "basicAuthUser"

// Validator authenticates a username/password pair and returns the caller's
// identity. The identity value is opaque to the plugin and is stored on the
// request Context for downstream use. A non-nil error rejects the request; if
// the error is an *aarv.AppError its status and message are honored, and the
// WWW-Authenticate challenge is suppressed for non-401 statuses (e.g. 403).
//
// On success the identity must be non-nil. Returning (nil, nil) is treated as
// authentication failure — context.Context cannot distinguish a stored nil
// from a missing value, so the plugin refuses to store one.
type Validator func(user, pass string) (identity any, err error)

// Config holds configuration for the Basic auth middleware.
type Config struct {
	// Realm is included in the WWW-Authenticate challenge as
	// realm="<value>" when non-empty. Per RFC 7235 the value must not contain
	// '"', '\\', or control characters; New panics if it does.
	Realm string

	// Charset, when non-empty, is included in the WWW-Authenticate challenge
	// as charset="<value>". Per RFC 7617 §2.1 the only allowed value is
	// "UTF-8" (matched case-insensitively); New panics if any other value is
	// supplied. Advertising "UTF-8" tells modern user agents to encode
	// credentials as UTF-8 rather than the legacy ISO-8859-1.
	Charset string

	// Validator is the function used to verify credentials. Required.
	Validator Validator

	// ErrorMessage is the message returned to clients on auth failure.
	// Default: "missing or invalid credentials".
	ErrorMessage string
}

// DefaultConfig returns a Config populated with the plugin defaults. The
// caller must still set Validator before passing it to New.
func DefaultConfig() Config {
	return Config{
		ErrorMessage: "missing or invalid credentials",
	}
}

// New creates a Basic auth middleware. It panics if cfg.Validator is nil
// (silent passthrough on misconfiguration is unsafe for an auth plugin) or if
// cfg.Realm contains characters that would produce a malformed
// WWW-Authenticate header.
func New(cfg Config) aarv.Middleware {
	if cfg.Validator == nil {
		panic("basicauth: Config.Validator is required")
	}
	if !validRealm(cfg.Realm) {
		panic("basicauth: Config.Realm must not contain '\"', '\\\\', or control characters")
	}
	if cfg.Charset != "" && !strings.EqualFold(cfg.Charset, "UTF-8") {
		panic(`basicauth: Config.Charset, when set, must be "UTF-8" (RFC 7617 §2.1)`)
	}
	if cfg.ErrorMessage == "" {
		cfg.ErrorMessage = "missing or invalid credentials"
	}

	challenge := buildChallenge(cfg.Realm, cfg.Charset)

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			user, pass, ok := parseAuthHeader(c.Header("Authorization"))
			if !ok {
				c.SetHeader("WWW-Authenticate", challenge)
				return aarv.ErrUnauthorized(cfg.ErrorMessage)
			}

			identity, err := cfg.Validator(user, pass)
			if err != nil {
				var appErr *aarv.AppError
				if errors.As(err, &appErr) {
					if appErr.StatusCode() == http.StatusUnauthorized {
						c.SetHeader("WWW-Authenticate", challenge)
					}
					return appErr
				}
				c.SetHeader("WWW-Authenticate", challenge)
				return aarv.ErrUnauthorized(cfg.ErrorMessage)
			}
			if identity == nil {
				c.SetHeader("WWW-Authenticate", challenge)
				return aarv.ErrUnauthorized(cfg.ErrorMessage)
			}

			c.Set(identityStoreKey, identity)
			c.SetContextValue(contextKey{}, identity)
			return next(c)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := parseAuthHeader(r.Header.Get("Authorization"))
			if !ok {
				w.Header().Set("WWW-Authenticate", challenge)
				writeError(w, r, http.StatusUnauthorized, cfg.ErrorMessage)
				return
			}

			identity, err := cfg.Validator(user, pass)
			if err != nil {
				status := http.StatusUnauthorized
				msg := cfg.ErrorMessage
				var appErr *aarv.AppError
				if errors.As(err, &appErr) {
					status = appErr.StatusCode()
					msg = appErr.Message()
				}
				if status == http.StatusUnauthorized {
					w.Header().Set("WWW-Authenticate", challenge)
				}
				writeError(w, r, status, msg)
				return
			}
			if identity == nil {
				w.Header().Set("WWW-Authenticate", challenge)
				writeError(w, r, http.StatusUnauthorized, cfg.ErrorMessage)
				return
			}

			if c, ok := aarv.FromRequest(r); ok {
				c.Set(identityStoreKey, identity)
				c.SetContextValue(contextKey{}, identity)
				r = c.RawRequest()
			} else {
				ctx := context.WithValue(r.Context(), contextKey{}, identity)
				r = r.WithContext(ctx)
			}

			next.ServeHTTP(w, r)
		})
	})
	return aarv.RegisterNativeMiddleware(m, native)
}

// parseAuthHeader extracts the username and password from a Basic
// Authorization header. It returns ok=false if the header is missing, uses a
// non-Basic scheme, contains malformed base64, or has no colon separator in
// the decoded credentials.
//
// Per RFC 7235 the scheme token is case-insensitive. Per RFC 7617 username
// and password are split on the first ':' so passwords containing ':' are
// preserved.
func parseAuthHeader(header string) (user, pass string, ok bool) {
	const prefix = "Basic "
	if len(header) < len(prefix) {
		return "", "", false
	}
	if !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}
	encoded := header[len(prefix):]
	// Tolerate optional whitespace between scheme and token (some clients
	// send "Basic  <token>"). Strip leading spaces only; trailing whitespace
	// is preserved because base64 padding is sensitive.
	for len(encoded) > 0 && encoded[0] == ' ' {
		encoded = encoded[1:]
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}
	idx := strings.IndexByte(string(decoded), ':')
	if idx < 0 {
		return "", "", false
	}
	return string(decoded[:idx]), string(decoded[idx+1:]), true
}

// validRealm reports whether s is safe to embed in a quoted-string parameter
// of a WWW-Authenticate header (no double-quote, backslash, CR, LF, or other
// control characters per RFC 5234).
func validRealm(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' || c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// buildChallenge produces the WWW-Authenticate header value. Realm and
// charset are appended only when non-empty.
func buildChallenge(realm, charset string) string {
	var b strings.Builder
	b.WriteString("Basic")
	if realm != "" {
		b.WriteString(` realm="`)
		b.WriteString(realm)
		b.WriteByte('"')
	}
	if charset != "" {
		if realm != "" {
			b.WriteByte(',')
		}
		b.WriteString(` charset="`)
		b.WriteString(charset)
		b.WriteByte('"')
	}
	return b.String()
}

// StaticCreds returns a Validator that authenticates against an in-memory map
// of username→password. Identity returned on success is the username string.
//
// Stored passwords are hashed to fixed-length 32-byte SHA-256 digests at
// snapshot time, and per-request comparison hashes the attempted password and
// uses crypto/subtle.ConstantTimeCompare on the digests. The fixed length
// closes the password-length side channel that ConstantTimeCompare exposes
// when input lengths differ. The map lookup itself remains a small timing
// channel for "is this username known"; SHA-256 is used here for side-channel
// resistance, not at-rest password protection — for that, validators should
// hit a credential store backed by bcrypt or argon2.
//
// An empty username always fails, regardless of whether the map contains "".
func StaticCreds(creds map[string]string) Validator {
	snapshot := make(map[string][32]byte, len(creds))
	for k, v := range creds {
		snapshot[k] = sha256.Sum256([]byte(v))
	}
	// Sentinel digest used when the username is unknown. SHA-256 of any
	// realistic password is overwhelmingly unlikely to collide with the zero
	// digest, so a real password can never accidentally authenticate against
	// this sentinel.
	var sentinel [32]byte
	return func(user, pass string) (any, error) {
		if user == "" {
			return nil, errInvalidCreds
		}
		expected, known := snapshot[user]
		if !known {
			expected = sentinel
		}
		attempted := sha256.Sum256([]byte(pass))
		match := subtle.ConstantTimeCompare(attempted[:], expected[:]) == 1
		if !known || !match {
			return nil, errInvalidCreds
		}
		return user, nil
	}
}

var errInvalidCreds = errors.New("basicauth: invalid credentials")

// From retrieves the identity stored by the middleware from an aarv.Context.
// Returns (nil, false) if no identity is present (the middleware did not run
// on this route, or auth failed).
func From(c *aarv.Context) (any, bool) {
	if c == nil {
		return nil, false
	}
	return c.Get(identityStoreKey)
}

// FromContext retrieves the identity from a request's context.Context. Useful
// from handlers or plugins that operate on r.Context() rather than
// *aarv.Context.
func FromContext(ctx context.Context) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(contextKey{})
	if v == nil {
		return nil, false
	}
	return v, true
}

// errorBody mirrors the framework's default JSON error shape so the stdlib
// path emits responses indistinguishable from native-path failures.
type errorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	requestID := ""
	if c, ok := aarv.FromRequest(r); ok {
		requestID = c.RequestID()
	}
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
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	default:
		return http.StatusText(status)
	}
}
