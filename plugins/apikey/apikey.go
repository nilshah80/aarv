// Package apikey provides API key authentication middleware for the aarv framework.
//
// The middleware looks up an API key from a configurable request header and,
// optionally, a query parameter, then delegates verification to a user-supplied
// Validator. On success, the validator's identity value (claims, client name,
// account struct, etc.) is stored on the Context so downstream handlers can
// retrieve it via From or FromContext. On failure, the request is rejected with
// 401 Unauthorized.
//
// Query lookup is opt-in (Config.Query defaults to "") because keys carried in
// URLs are routinely captured in proxy access logs, browser history, and
// Referer headers. Enable it explicitly only when the deployment justifies it.
package apikey

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nilshah80/aarv"
)

type contextKey struct{}

// identityStoreKey is the fixed key under which the middleware stores the
// authenticated identity on *aarv.Context. It is hardcoded (not configurable)
// so the public From/FromContext helpers always succeed when auth ran.
const identityStoreKey = "apiClient"

// Validator authenticates an API key and returns the caller's identity.
// The identity value is opaque to the plugin and is stored on the request
// Context for downstream use. A non-nil error rejects the request; if the
// error is an *aarv.AppError, its status and message are honored when the
// middleware runs on the native fast path.
//
// On success the identity must be non-nil. Returning (nil, nil) is treated as
// an authentication failure — context.Context cannot distinguish a stored nil
// from a missing value, so the plugin refuses to store one.
type Validator func(key string) (identity any, err error)

// Config holds configuration for the API key middleware.
type Config struct {
	// Header is the request header read for the API key.
	// Default: "X-API-Key". Set to "" to disable header lookup.
	Header string

	// Query is the URL query parameter read for the API key when the header
	// is absent or empty. Default: "" (disabled). Setting this enables a
	// secondary lookup; prefer header-only unless the deployment requires
	// keys in URLs.
	Query string

	// Validator is the function used to authenticate keys. Required.
	Validator Validator

	// ErrorMessage is the message returned to clients on auth failure.
	// Default: "missing or invalid API key".
	ErrorMessage string
}

// DefaultConfig returns a Config populated with the plugin defaults. The
// caller must still set Validator before passing it to New.
func DefaultConfig() Config {
	return Config{
		Header:       "X-API-Key",
		ErrorMessage: "missing or invalid API key",
	}
}

// New creates an API key authentication middleware. It panics if cfg.Validator
// is nil — silently skipping unauthenticated requests is unsafe. Empty Header
// and Query both being unset is also a misconfiguration and panics.
func New(cfg Config) aarv.NativeMiddleware {
	if cfg.Validator == nil {
		panic("apikey: Config.Validator is required")
	}
	if cfg.Header == "" && cfg.Query == "" {
		panic("apikey: at least one of Config.Header or Config.Query must be set")
	}
	if cfg.ErrorMessage == "" {
		cfg.ErrorMessage = "missing or invalid API key"
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			key := extractKey(cfg, c.Header, c.Query)
			if key == "" {
				return aarv.ErrUnauthorized(cfg.ErrorMessage)
			}

			identity, err := cfg.Validator(key)
			if err != nil {
				var appErr *aarv.AppError
				if errors.As(err, &appErr) {
					return appErr
				}
				return aarv.ErrUnauthorized(cfg.ErrorMessage)
			}
			if identity == nil {
				return aarv.ErrUnauthorized(cfg.ErrorMessage)
			}

			c.Set(identityStoreKey, identity)
			c.SetContextValue(contextKey{}, identity)
			return next(c)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := extractKey(cfg, r.Header.Get, r.URL.Query().Get)
			if key == "" {
				writeUnauthorized(w, r, cfg.ErrorMessage)
				return
			}

			identity, err := cfg.Validator(key)
			if err != nil {
				msg := cfg.ErrorMessage
				status := http.StatusUnauthorized
				var appErr *aarv.AppError
				if errors.As(err, &appErr) {
					status = appErr.StatusCode()
					msg = appErr.Message()
				}
				writeError(w, r, status, msg)
				return
			}
			if identity == nil {
				writeUnauthorized(w, r, cfg.ErrorMessage)
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

// extractKey reads the API key from the configured header (and query, if
// enabled). Header takes precedence over query when both are present.
func extractKey(cfg Config, header, query func(string) string) string {
	if cfg.Header != "" {
		if v := header(cfg.Header); v != "" {
			return v
		}
	}
	if cfg.Query != "" {
		if v := query(cfg.Query); v != "" {
			return v
		}
	}
	return ""
}

// StaticKeys returns a Validator that authenticates against an in-memory map
// of key→identity. Keys are hashed to fixed-length 32-byte SHA-256 digests at
// snapshot time, and per-request lookup hashes the presented key the same way
// before doing the lookup. This closes the key-length side channel that a
// naïve byte-by-byte compare exposes when stored and presented keys have
// different lengths.
//
// SHA-256 is used here for in-memory side-channel resistance, not at-rest key
// protection — for that, store key digests externally and write a custom
// validator that hits a credential store. The map lookup itself remains a
// small "is this hash known" timing channel.
//
// An empty input key always fails, regardless of whether "" is present in the
// input map.
func StaticKeys(keys map[string]any) Validator {
	snapshot := make(map[[32]byte]any, len(keys))
	for k, v := range keys {
		snapshot[sha256.Sum256([]byte(k))] = v
	}
	return func(presented string) (any, error) {
		if presented == "" {
			return nil, errInvalidKey
		}
		digest := sha256.Sum256([]byte(presented))
		if v, ok := snapshot[digest]; ok {
			return v, nil
		}
		return nil, errInvalidKey
	}
}

var errInvalidKey = errors.New("apikey: invalid key")

// From retrieves the identity stored by the middleware from an aarv.Context.
// Returns (nil, false) if no identity is present (e.g. the middleware did not
// run on this route).
func From(c *aarv.Context) (any, bool) {
	if c == nil {
		return nil, false
	}
	return c.Get(identityStoreKey)
}

// FromContext retrieves the identity from a request's context.Context. Useful
// from handlers or plugins that operate on r.Context() rather than *aarv.Context.
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

func writeUnauthorized(w http.ResponseWriter, r *http.Request, message string) {
	writeError(w, r, http.StatusUnauthorized, message)
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
