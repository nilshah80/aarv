// Package bearer provides Bearer Token authentication middleware (RFC 6750)
// for the aarv framework.
//
// The middleware extracts a token from the Authorization request header using
// the "Bearer" auth scheme (case-insensitive, RFC 7235 §2.1) and, optionally,
// from a URL query parameter. It then delegates verification to a user-supplied
// Validator. On success the validator's identity value (claims, user struct,
// session record, etc.) is stored on the Context and reachable through From or
// FromContext. On failure the middleware responds with 401 Unauthorized and a
// WWW-Authenticate: Bearer challenge per RFC 6750 §3.
//
// # Header vs query lookup
//
// Per RFC 6750 §2.1, the Authorization header is the canonical transport for
// bearer tokens. Query parameter transport (§2.3) is opt-in (Config.Query
// defaults to "") because tokens carried in URLs are routinely captured in
// proxy access logs, browser history, and Referer headers. Form-encoded body
// transport (§2.2) is intentionally not supported — it interferes with normal
// request body parsing and is discouraged by the spec.
//
// # Relationship to the JWT plugin
//
// This plugin is intended for opaque tokens (database/cache lookups, session
// identifiers, OAuth2 reference tokens). For self-contained signed JWTs use
// plugins/jwt, which handles algorithm negotiation, signature verification,
// and standard-claims validation. Both plugins follow the same identity
// storage contract, so a handler protected by either can call the package's
// From / FromContext helpers in the same shape.
//
// # Cross-path response parity
//
// bearer registers both a native (*aarv.HandlerFunc) and a stdlib
// (http.Handler) middleware path. Their failure responses (status, body
// bytes, Content-Type, WWW-Authenticate) are byte-identical only when ALL
// of the following hold:
//
//   - the framework uses its default ErrorHandler (no [aarv.WithErrorHandler]),
//   - the framework uses its default JSON codec / "application/json"
//     content type (no [aarv.WithCodec] customization that would change
//     either the serialized error shape or the Content-Type header), AND
//   - no [OnError] hook mutates the response (status, headers, or body).
//
// The native path returns *aarv.AppError to the framework, which runs
// OnError, may invoke a user ErrorHandler, and serializes via the
// configured codec. The stdlib path bypasses all three: it writes
// "application/json" + the framework's default error JSON directly. Any
// of those three customizations therefore causes the two paths to
// diverge. If you need symmetric custom behavior across both paths,
// either stay on the framework defaults or install your customization at
// a layer outside the plugin.
package bearer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/nilshah80/aarv"
)

type contextKey struct{}

// identityStoreKey is the fixed key under which the middleware stores the
// authenticated identity on *aarv.Context. It is hardcoded (not configurable)
// so the public From / FromContext helpers always succeed when auth ran.
const identityStoreKey = "bearerIdentity"

// schemeName is the auth scheme matched in the Authorization header. RFC 6750
// fixes this at "Bearer"; matching is case-insensitive per RFC 7235 §2.1.
const schemeName = "Bearer"

// Validator authenticates a bearer token and returns the caller's identity.
// The identity value is opaque to the plugin and is stored on the request
// Context for downstream use. A non-nil error rejects the request; if the
// error is an *aarv.AppError, its status and message are honored. The
// WWW-Authenticate challenge is suppressed for non-401 statuses (e.g. 403)
// because RFC 7235 only requires the challenge on 401 responses.
//
// On success the identity must be non-nil. Returning (nil, nil) is treated as
// authentication failure — context.Context cannot distinguish a stored nil
// from a missing value, so the plugin refuses to store one.
type Validator func(token string) (identity any, err error)

// Config holds configuration for the Bearer token middleware.
type Config struct {
	// Header is the request header read for the bearer token.
	// Default: "Authorization".
	Header string

	// Query is the URL query parameter read for the bearer token when the
	// configured Header is absent or empty. Default: "" (disabled).
	//
	// Header presence is exclusive: when the configured Header is non-empty
	// on a request, it MUST yield a valid Bearer token, and the query
	// parameter is NOT consulted. A request that sets, e.g.,
	// "Authorization: Basic ..." or "Authorization: Bearer " (no token) is
	// rejected with 401 even when a valid query token is also present. This
	// matches RFC 6750 §2's single-transport model and prevents an attacker
	// from sneaking a valid query token past header-based audit logging by
	// pairing it with a malformed Authorization header.
	//
	// Setting this enables a secondary lookup; prefer header-only unless the
	// deployment requires tokens in URLs (e.g. browser-driven file downloads
	// where the browser cannot set headers).
	Query string

	// Realm is included in the WWW-Authenticate challenge as
	// realm="<value>" when non-empty. Per RFC 7235 the value must not contain
	// '"', '\\', or control characters; New panics if it does.
	Realm string

	// Validator is the function used to authenticate tokens. Required.
	Validator Validator

	// ErrorMessage is the message returned to clients on auth failure.
	// Default: "missing or invalid bearer token".
	ErrorMessage string
}

// DefaultConfig returns a Config populated with the plugin defaults. The
// caller must still set Validator before passing it to New.
func DefaultConfig() Config {
	return Config{
		Header:       "Authorization",
		ErrorMessage: "missing or invalid bearer token",
	}
}

// New creates a Bearer token authentication middleware. It panics if
// cfg.Validator is nil (silent passthrough on misconfiguration is unsafe for
// an auth plugin), if both Header and Query are empty, or if cfg.Realm
// contains characters that would produce a malformed WWW-Authenticate header.
func New(cfg Config) aarv.NativeMiddleware {
	if cfg.Validator == nil {
		panic("bearer: Config.Validator is required")
	}
	if cfg.Header == "" && cfg.Query == "" {
		panic("bearer: at least one of Config.Header or Config.Query must be set")
	}
	if !validRealm(cfg.Realm) {
		panic("bearer: Config.Realm must not contain '\"', '\\\\', or control characters")
	}
	if cfg.ErrorMessage == "" {
		cfg.ErrorMessage = "missing or invalid bearer token"
	}

	challenge := buildChallenge(cfg.Realm)

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			token := extractToken(cfg, c.Header, c.Query)
			if token == "" {
				c.SetHeader("WWW-Authenticate", challenge)
				return aarv.ErrUnauthorized(cfg.ErrorMessage)
			}

			identity, err := cfg.Validator(token)
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
			token := extractToken(cfg, r.Header.Get, r.URL.Query().Get)
			if token == "" {
				w.Header().Set("WWW-Authenticate", challenge)
				writeUnauthorized(w, r, cfg.ErrorMessage)
				return
			}

			identity, err := cfg.Validator(token)
			if err != nil {
				var appErr *aarv.AppError
				if errors.As(err, &appErr) {
					if appErr.StatusCode() == http.StatusUnauthorized {
						w.Header().Set("WWW-Authenticate", challenge)
					}
					writeAppError(w, r, appErr)
					return
				}
				w.Header().Set("WWW-Authenticate", challenge)
				writeUnauthorized(w, r, cfg.ErrorMessage)
				return
			}
			if identity == nil {
				w.Header().Set("WWW-Authenticate", challenge)
				writeUnauthorized(w, r, cfg.ErrorMessage)
				return
			}

			if c, ok := aarv.FromRequest(r); ok {
				// Re-bind c to the (possibly upstream-mutated) r before
				// stamping identity. (*Context).BindRequest preserves
				// any upstream URL/Header/Body changes — SetContextValue
				// alone would rewrap the framework's original c.req and
				// silently discard them. We don't need BindRequest's
				// return value because SetContextValue rewraps c.req
				// again immediately after; take RawRequest then to pick
				// up the final wrapped request before forwarding.
				c.BindRequest(r)
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

// extractToken reads the bearer token from the configured header (and query,
// if enabled). Header presence is exclusive: when the configured Header is
// non-empty on the request, the parsed Bearer token is returned and the
// query parameter is NOT consulted, even if parsing fails. The query
// parameter is consulted only when the configured Header is absent or empty.
// See Config.Query godoc for the rationale (RFC 6750 §2 single-transport
// model and audit-log-bypass prevention).
func extractToken(cfg Config, header, query func(string) string) string {
	if cfg.Header != "" {
		if raw := header(cfg.Header); raw != "" {
			return parseAuthHeader(raw)
		}
	}
	if cfg.Query != "" {
		if v := query(cfg.Query); v != "" {
			return v
		}
	}
	return ""
}

// parseAuthHeader extracts the token from an Authorization header that uses
// the Bearer scheme. Returns "" when the header is missing, uses a non-Bearer
// scheme, or has no token after the scheme. Per RFC 7235 §2.1 the scheme
// match is case-insensitive; one optional leading space after the scheme
// separator is tolerated.
func parseAuthHeader(value string) string {
	if len(value) < len(schemeName)+1 {
		return ""
	}
	if !strings.EqualFold(value[:len(schemeName)], schemeName) {
		return ""
	}
	rest := value[len(schemeName):]
	// RFC 7235 §2.1 requires a single space between scheme and token.
	if rest[0] != ' ' {
		return ""
	}
	rest = rest[1:]
	// Tolerate one extra leading space ("Bearer  <token>") for clients that
	// double-space the separator. Trailing whitespace is preserved in case a
	// token validator wishes to reject it explicitly.
	if len(rest) > 0 && rest[0] == ' ' {
		rest = rest[1:]
	}
	return rest
}

// validRealm reports whether s is safe to embed in a quoted-string parameter
// of a WWW-Authenticate header (no double-quote, backslash, or control
// characters per RFC 5234).
func validRealm(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' || c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// buildChallenge produces the WWW-Authenticate header value. Per RFC 6750 §3
// the scheme name is "Bearer"; realm is appended only when non-empty.
func buildChallenge(realm string) string {
	if realm == "" {
		return schemeName
	}
	var b strings.Builder
	b.WriteString(schemeName)
	b.WriteString(` realm="`)
	b.WriteString(realm)
	b.WriteByte('"')
	return b.String()
}

// StaticTokens returns a Validator that authenticates against an in-memory map
// of token→identity. Tokens are hashed to fixed-length 32-byte SHA-256 digests
// at snapshot time, and per-request lookup hashes the presented token the same
// way before doing the lookup. This closes the token-length side channel that
// a naïve byte-by-byte compare exposes when stored and presented tokens have
// different lengths.
//
// SHA-256 is used here for in-memory side-channel resistance, not at-rest
// token protection — for that, store token digests externally and write a
// custom validator that hits a credential store. The map lookup itself
// remains a small "is this hash known" timing channel.
//
// An empty input token always fails, regardless of whether "" is present in
// the input map.
func StaticTokens(tokens map[string]any) Validator {
	snapshot := make(map[[32]byte]any, len(tokens))
	for k, v := range tokens {
		snapshot[sha256.Sum256([]byte(k))] = v
	}
	return func(presented string) (any, error) {
		if presented == "" {
			return nil, errInvalidToken
		}
		digest := sha256.Sum256([]byte(presented))
		if v, ok := snapshot[digest]; ok {
			return v, nil
		}
		return nil, errInvalidToken
	}
}

var errInvalidToken = errors.New("bearer: invalid token")

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

// errorBody mirrors aarv's framework default JSON error shape (see
// errorResponse in the root package) so the stdlib path emits responses
// matching the native path when the framework uses its default
// ErrorHandler / default JSON codec / no response-mutating OnError.
// AppError.Code() and AppError.Detail() are preserved on the wire when
// the validator returns a typed error. With any of those three
// customizations the wire bytes diverge — see the package godoc.
type errorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// writeUnauthorized is the canonical 401 emitter for "no token / bad token /
// nil identity" failures where the plugin synthesizes the error itself.
func writeUnauthorized(w http.ResponseWriter, r *http.Request, message string) {
	writeBody(w, http.StatusUnauthorized, errorBody{
		Error:     "unauthorized",
		Message:   message,
		RequestID: requestID(r),
	})
}

// writeAppError serializes a validator-returned *aarv.AppError using the
// same wire shape aarv's default error handler emits on the native path
// — so a custom error like aarv.NewError(401, "token_expired", "...").
// WithDetail("...") matches across paths when the framework uses its
// default ErrorHandler, default JSON codec, and no response-mutating
// OnError. With any of those customizations the native path's wire bytes
// will differ; see the package godoc.
func writeAppError(w http.ResponseWriter, r *http.Request, appErr *aarv.AppError) {
	writeBody(w, appErr.StatusCode(), errorBody{
		Error:     appErr.Code(),
		Message:   appErr.Message(),
		Detail:    appErr.Detail(),
		RequestID: requestID(r),
	})
}

func writeBody(w http.ResponseWriter, status int, body errorBody) {
	// Match aarv's framework default JSON Content-Type (application/json,
	// no charset parameter) so native and stdlib middleware paths emit
	// the same Content-Type when the framework also runs its default
	// codec. RFC 8259 §11 implies UTF-8, so the charset parameter is
	// redundant; sticking to the framework default keeps downstream
	// consumers (logs, gateway parsers) from observing a path-dependent
	// content type. With WithCodec installed, the native path's
	// Content-Type will follow the codec and diverge from this constant.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func requestID(r *http.Request) string {
	if c, ok := aarv.FromRequest(r); ok {
		return c.RequestID()
	}
	return ""
}
