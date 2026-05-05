// Package jwt provides JSON Web Token (RFC 7519) authentication middleware
// for the aarv framework.
//
// The middleware extracts a token from one or more configured Lookups
// (Authorization: Bearer header by default), parses it, verifies its
// signature against an allow-listed algorithm, validates the standard
// registered claims (exp, nbf, iat, iss, aud), runs an optional custom
// ClaimsValidator, and stores the resulting claims map on the Context for
// downstream handlers via From / FromContext / GetClaims[T].
//
// # Security model
//
//   - "alg":"none" tokens are rejected unconditionally; the algorithm is
//     never registered.
//   - The token's alg header is verified against Config.Algorithms before
//     any key resolution. The key returned by KeyFunc is then type-asserted
//     against the alg's required Go type (e.g. RS256 must get
//     *rsa.PublicKey). Together these close the alg-confusion attack.
//   - NumericDate claims (exp, nbf, iat) must be JSON integers in
//     [0, 253402300799] — fractional, string-shaped, negative, and
//     millisecond-scale values are rejected. This is intentionally stricter
//     than RFC 7519 §2 and is documented in the CHANGELOG.
//   - KeyFunc receives the parsed JOSE header only. Issuer-based key
//     selection is not supported by the framework; callers that need it
//     must decode unverified claims themselves and dispatch from there.
//
// # Configuration discipline
//
// New panics on any misconfiguration (parity with apikey / basicauth).
// Parse and RefreshToken validate the same Config but return typed errors
// instead of panicking, so programmatic callers can branch on the
// sentinels in this package without `recover`.
package jwt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nilshah80/aarv"
)

type contextKey struct{}

// identityStoreKey is the fixed key under which the middleware stores the
// validated claims on *aarv.Context. Hardcoded so From always succeeds when
// auth ran (mirrors apikey / basicauth).
const identityStoreKey = "jwtClaims"

// KeyFunc resolves the key used to verify (or sign) a token. It receives
// the parsed JOSE header so callers can branch on kid, alg, or other
// header parameters. Returning (nil, nil) is treated as auth failure —
// the plugin refuses to attempt verification with a nil key.
type KeyFunc func(header map[string]any) (key any, err error)

// ClaimsValidator is an optional hook called after the standard registered
// claims pass. Returning a non-nil error rejects the token; returning an
// *aarv.AppError lets the validator pick a custom status (e.g. 403).
type ClaimsValidator func(claims map[string]any) error

// Config holds JWT middleware configuration.
type Config struct {
	// Algorithms is the allow-list of acceptable alg header values. When
	// empty and HMACSecret is set, the plugin defaults to [HS256]. When
	// empty and only KeyFunc is set, configuration is invalid (no silent
	// HS256 fallback for asymmetric setups).
	Algorithms []Algorithm

	// KeyFunc resolves a verification (or signing) key from the token's
	// JOSE header. Mutually exclusive with HMACSecret.
	KeyFunc KeyFunc

	// HMACSecret is a convenience for HS* deployments. When set, the
	// plugin synthesizes a KeyFunc that returns these bytes. Mutually
	// exclusive with KeyFunc.
	HMACSecret []byte

	// Lookups is the ordered list of token sources. The first non-empty
	// extraction wins. Defaults to a single Authorization: Bearer header.
	Lookups []Lookup

	// Issuer, when non-empty, must equal the token's iss claim exactly.
	Issuer string

	// Audience, when non-empty, must match the token's aud claim
	// (which may be a string or an array of strings).
	Audience string

	// Leeway is added to exp / nbf comparisons to tolerate clock skew.
	Leeway time.Duration

	// ClaimsValidator runs after standard claim validation; returning a
	// non-nil error rejects the token.
	ClaimsValidator ClaimsValidator

	// SkipPaths is an exact-match list of request paths that bypass auth.
	SkipPaths []string

	// ErrorMessage is the message returned to clients on auth failure.
	// Defaults to "missing or invalid token".
	ErrorMessage string
}

// DefaultConfig returns a Config with the default Lookups and ErrorMessage.
// It deliberately does not populate Algorithms, KeyFunc, or HMACSecret;
// passing it through validateConfig as-is returns ErrMissingKey, so callers
// must explicitly opt into a verification strategy.
func DefaultConfig() Config {
	return Config{
		Lookups: []Lookup{
			{Source: lookupHeader, Name: "Authorization", Scheme: "Bearer"},
		},
		ErrorMessage: "missing or invalid token",
	}
}

// Sentinel errors. All of these support errors.Is and are documented in
// the package CHANGELOG entry.
var (
	// Token shape & decoding.
	ErrMalformedToken = errors.New("jwt: malformed token")
	ErrMissingToken   = errors.New("jwt: token not present in any configured lookup")

	// Algorithm & signature.
	ErrAlgNone          = errors.New("jwt: alg=none rejected")
	ErrUnknownAlg       = errors.New("jwt: unknown algorithm")
	ErrAlgNotAllowed    = errors.New("jwt: algorithm not in allow-list")
	ErrKeyTypeMismatch  = errors.New("jwt: key type does not match algorithm")
	ErrWeakKey          = errors.New("jwt: hmac key shorter than hash output")
	ErrInvalidSignature = errors.New("jwt: invalid signature")

	// Claims.
	ErrExpired            = errors.New("jwt: token expired")
	ErrNotYetValid        = errors.New("jwt: token not yet valid")
	ErrInvalidIssuer      = errors.New("jwt: invalid issuer")
	ErrInvalidAudience    = errors.New("jwt: invalid audience")
	ErrInvalidNumericDate = errors.New("jwt: invalid NumericDate value")

	// Configuration. New panics with strings derived from these; Parse
	// and RefreshToken return them directly.
	ErrMissingKey        = errors.New("jwt: KeyFunc or HMACSecret required")
	ErrNoAlgorithms      = errors.New("jwt: Algorithms must be non-empty when KeyFunc is set")
	ErrConflictingKey    = errors.New("jwt: KeyFunc and HMACSecret are mutually exclusive")
	ErrSecretAlgMismatch = errors.New("jwt: HMACSecret only valid with HS* algorithms")
	ErrInvalidLookup     = errors.New("jwt: Lookup.Source must be header, query, or cookie")

	// Refresh.
	ErrInvalidTTL = errors.New("jwt: ttl must be >= 1s")
)

// normalized is the validated form of Config that middleware closes over.
// It is identical in shape to Config except KeyFunc is always set (the
// HMACSecret path is folded into a KeyFunc), Algorithms is non-empty, and
// Lookups / ErrorMessage carry their defaults.
type normalized struct {
	algorithms      map[Algorithm]struct{}
	keyFunc         KeyFunc
	lookups         []Lookup
	issuer          string
	audience        string
	leeway          time.Duration
	claimsValidator ClaimsValidator
	skipPaths       map[string]struct{}
	errorMessage    string
}

// validateConfig is the single source of truth for config legality. It is
// called by both New (which converts errors into panics) and Parse /
// RefreshToken (which return them).
func validateConfig(cfg Config) (normalized, error) {
	var n normalized

	hasKeyFunc := cfg.KeyFunc != nil
	hasSecret := len(cfg.HMACSecret) > 0
	switch {
	case !hasKeyFunc && !hasSecret:
		return n, ErrMissingKey
	case hasKeyFunc && hasSecret:
		return n, ErrConflictingKey
	}

	// Resolve algorithms. cfg.Algorithms is consumed only into the
	// `allowed` map below — the slice itself is not retained, so
	// post-construction mutation of cfg.Algorithms cannot affect
	// middleware behavior. Lookups and HMACSecret get explicit defensive
	// copies further down for the same reason.
	algs := cfg.Algorithms
	if len(algs) == 0 {
		if hasSecret {
			algs = []Algorithm{HS256}
		} else {
			return n, ErrNoAlgorithms
		}
	}

	allowed := make(map[Algorithm]struct{}, len(algs))
	for _, a := range algs {
		if a == "none" {
			return n, ErrAlgNone
		}
		if _, ok := lookupAlg(a); !ok {
			return n, ErrUnknownAlg
		}
		if hasSecret && !isHMAC(a) {
			return n, ErrSecretAlgMismatch
		}
		if hasSecret {
			spec, _ := lookupAlg(a)
			if len(cfg.HMACSecret) < spec.hmacSize {
				return n, ErrWeakKey
			}
		}
		allowed[a] = struct{}{}
	}

	// Lookups. Defensive-copy the caller's slice so later mutations of
	// cfg.Lookups cannot change the middleware's behavior or race with
	// in-flight requests. HMACSecret gets the same treatment below.
	lookups := make([]Lookup, len(cfg.Lookups))
	copy(lookups, cfg.Lookups)
	if len(lookups) == 0 {
		lookups = []Lookup{{Source: lookupHeader, Name: "Authorization", Scheme: "Bearer"}}
	}
	for _, lk := range lookups {
		switch lk.Source {
		case lookupHeader, lookupQuery, lookupCookie:
		default:
			return n, ErrInvalidLookup
		}
	}

	// Resolve KeyFunc: HMACSecret is sugar for a closure that returns the
	// secret bytes for any allow-listed alg. The header is ignored because
	// the alg has already been allow-list-checked at this point.
	keyFunc := cfg.KeyFunc
	if hasSecret {
		secret := append([]byte(nil), cfg.HMACSecret...)
		keyFunc = func(_ map[string]any) (any, error) {
			return secret, nil
		}
	}

	// Skip paths set.
	skip := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skip[p] = struct{}{}
	}

	msg := cfg.ErrorMessage
	if msg == "" {
		msg = "missing or invalid token"
	}

	n = normalized{
		algorithms:      allowed,
		keyFunc:         keyFunc,
		lookups:         lookups,
		issuer:          cfg.Issuer,
		audience:        cfg.Audience,
		leeway:          cfg.Leeway,
		claimsValidator: cfg.ClaimsValidator,
		skipPaths:       skip,
		errorMessage:    msg,
	}
	return n, nil
}

// New creates JWT authentication middleware. It panics on any
// misconfiguration; callers needing non-panicking validation can use
// Parse (which performs the same validation but returns errors).
func New(cfg Config) aarv.Middleware {
	n, err := validateConfig(cfg)
	if err != nil {
		panic("jwt: " + err.Error())
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if _, skip := n.skipPaths[c.Path()]; skip {
				return next(c)
			}
			token, lookupErr := extractToken(n.lookups, contextSource{c: c})
			if lookupErr != nil {
				// Attach the sentinel as the AppError's internal cause so
				// downstream error handlers can branch via errors.Is. The
				// wire response stays the configured 401 message.
				return aarv.ErrUnauthorized(n.errorMessage).WithInternal(lookupErr)
			}
			_, claims, err := parseAndVerify(token, n)
			if err != nil {
				var appErr *aarv.AppError
				if errors.As(err, &appErr) {
					return appErr
				}
				return aarv.ErrUnauthorized(n.errorMessage)
			}
			c.Set(identityStoreKey, claims)
			c.SetContextValue(contextKey{}, claims)
			return next(c)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, skip := n.skipPaths[r.URL.Path]; skip {
				next.ServeHTTP(w, r)
				return
			}
			token, lookupErr := extractToken(n.lookups, requestSource{r: r})
			if lookupErr != nil {
				// lookupErr is ErrMissingToken — see extractToken's
				// contract. The stdlib path can't propagate an *aarv.AppError
				// chain to user code, so the sentinel stays internal here
				// and only the 401 reaches the wire.
				writeError(w, r, http.StatusUnauthorized, n.errorMessage)
				return
			}
			_, claims, err := parseAndVerify(token, n)
			if err != nil {
				status := http.StatusUnauthorized
				msg := n.errorMessage
				var appErr *aarv.AppError
				if errors.As(err, &appErr) {
					status = appErr.StatusCode()
					msg = appErr.Message()
				}
				writeError(w, r, status, msg)
				return
			}
			if c, ok := aarv.FromRequest(r); ok {
				c.Set(identityStoreKey, claims)
				c.SetContextValue(contextKey{}, claims)
				r = c.RawRequest()
			} else {
				ctx := context.WithValue(r.Context(), contextKey{}, claims)
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	})
	return aarv.RegisterNativeMiddleware(m, native)
}

// Parse decodes and verifies a token against cfg without running any
// middleware-specific behavior (no skip paths, no Context storage). It
// returns typed errors for both config and token failures so callers can
// branch via errors.Is.
func Parse(token string, cfg Config) (header, claims map[string]any, err error) {
	n, err := validateConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return parseAndVerify(token, n)
}

// SignToken produces a signed compact-serialized JWT. The header is built
// from alg and "typ":"JWT" only; this helper is deliberately minimal.
// Callers needing extra header parameters (e.g. kid) on a freshly issued
// token should build the header inline and sign with their own crypto, or
// use RefreshToken which preserves the original token's JOSE header
// verbatim across a refresh.
//
// claims must be non-nil: a nil map marshals to JSON null, and Parse
// rejects null payloads as ErrMalformedToken. SignToken refuses to emit
// a token its own package will not accept.
func SignToken(alg Algorithm, key any, claims map[string]any) (string, error) {
	if claims == nil {
		return "", ErrMalformedToken
	}
	spec, ok := lookupAlg(alg)
	if !ok {
		return "", ErrUnknownAlg
	}
	// alg has been validated by lookupAlg: registered algs are ASCII
	// identifiers safe to interpolate, so the header is built as a
	// constant byte slice without going through json.Marshal.
	hb := []byte(`{"alg":"` + string(alg) + `","typ":"JWT"}`)
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := append(append(b64Encode(hb), '.'), b64Encode(cb)...)
	sig, err := spec.sign(key, signingInput)
	if err != nil {
		return "", err
	}
	return string(signingInput) + "." + string(b64Encode(sig)), nil
}

// RefreshToken verifies token against cfg, then re-signs the same claims
// with a fresh iat/exp using signingKey. The verified alg is preserved
// (it is, by definition, in cfg.Algorithms). signingKey must match the
// preserved alg's required key type.
//
// The original JOSE header is preserved verbatim, so kid and any other
// custom header parameters carry across the refresh. This is required for
// JWKS-style key rotation: a verifier that selects keys by kid would
// otherwise fail to dispatch on the refreshed token. The "alg" field is
// always rewritten from the verified alg to keep header/alg coherence;
// "typ" defaults to "JWT" when absent.
//
// Only iat and exp are rewritten on the claim side. All other claims —
// including nbf and jti — are copied across unchanged. Callers that need
// to roll nbf forward or rotate jti must do so themselves via
// Parse → mutate → SignToken.
//
// ttl must be >= 1s; sub-second values are rejected with ErrInvalidTTL
// because NumericDate is second-granular per RFC 7519 and a sub-second
// ttl would issue a token whose exp equals iat.
func RefreshToken(token string, cfg Config, signingKey any, ttl time.Duration) (string, error) {
	if ttl < time.Second {
		return "", ErrInvalidTTL
	}
	n, err := validateConfig(cfg)
	if err != nil {
		return "", err
	}
	header, claims, err := parseAndVerify(token, n)
	if err != nil {
		return "", err
	}
	algStr, _ := header["alg"].(string)
	alg := Algorithm(algStr)

	// Build a fresh claims map so we don't mutate the parsed one.
	newClaims := make(map[string]any, len(claims)+2)
	for k, v := range claims {
		newClaims[k] = v
	}
	now := time.Now()
	newClaims["iat"] = float64(now.Unix())
	newClaims["exp"] = float64(now.Add(ttl).Unix())
	return signWithHeader(alg, signingKey, header, newClaims)
}

// signWithHeader signs claims with the given alg/key, preserving the
// caller-supplied JOSE header map (apart from "alg", which is always
// written from alg to guarantee header/alg coherence even if the source
// header carries a stale or attacker-controlled value). "typ" defaults to
// "JWT" when absent. Used by RefreshToken to carry kid and other custom
// header parameters across a refresh.
func signWithHeader(alg Algorithm, key any, header, claims map[string]any) (string, error) {
	if claims == nil {
		return "", ErrMalformedToken
	}
	spec, ok := lookupAlg(alg)
	if !ok {
		return "", ErrUnknownAlg
	}
	hdr := make(map[string]any, len(header)+1)
	for k, v := range header {
		hdr[k] = v
	}
	hdr["alg"] = string(alg)
	if _, ok := hdr["typ"]; !ok {
		hdr["typ"] = "JWT"
	}
	hb, err := json.Marshal(hdr)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := append(append(b64Encode(hb), '.'), b64Encode(cb)...)
	sig, err := spec.sign(key, signingInput)
	if err != nil {
		return "", err
	}
	return string(signingInput) + "." + string(b64Encode(sig)), nil
}

// From retrieves the validated claims from an *aarv.Context.
func From(c *aarv.Context) (map[string]any, bool) {
	if c == nil {
		return nil, false
	}
	v, ok := c.Get(identityStoreKey)
	if !ok {
		return nil, false
	}
	claims, ok := v.(map[string]any)
	return claims, ok
}

// FromContext retrieves the validated claims from a context.Context. Useful
// for downstream stdlib handlers that operate on r.Context() directly.
func FromContext(ctx context.Context) (map[string]any, bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(contextKey{})
	if v == nil {
		return nil, false
	}
	claims, ok := v.(map[string]any)
	return claims, ok
}

// GetClaims decodes the stored claims map into T via JSON round-trip.
// Useful for callers that prefer a typed view of their claims; for hot
// paths use From(c) to access the map directly without re-marshaling.
func GetClaims[T any](c *aarv.Context) (T, bool) {
	var zero T
	claims, ok := From(c)
	if !ok {
		return zero, false
	}
	buf, err := json.Marshal(claims)
	if err != nil {
		return zero, false
	}
	var out T
	if err := json.Unmarshal(buf, &out); err != nil {
		return zero, false
	}
	return out, true
}

// --- internal: parsing & verification ---

// parseAndVerify decodes a compact-serialized JWT, enforces the alg
// allow-list, resolves the verification key, checks the signature, and
// validates the standard claims. It returns the decoded header and claims
// on success.
func parseAndVerify(token string, n normalized) (header, claims map[string]any, err error) {
	headerSeg, payloadSeg, sigSeg, signingInput, err := splitToken(token)
	if err != nil {
		return nil, nil, err
	}
	headerBytes, err := b64Decode(headerSeg)
	if err != nil {
		return nil, nil, ErrMalformedToken
	}
	header = map[string]any{}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, nil, ErrMalformedToken
	}
	rawAlg, ok := header["alg"]
	if !ok {
		return nil, nil, ErrMalformedToken
	}
	algStr, ok := rawAlg.(string)
	if !ok {
		return nil, nil, ErrMalformedToken
	}
	if algStr == "none" {
		return nil, nil, ErrAlgNone
	}
	alg := Algorithm(algStr)
	spec, ok := lookupAlg(alg)
	if !ok {
		return nil, nil, ErrUnknownAlg
	}
	if _, ok := n.algorithms[alg]; !ok {
		return nil, nil, ErrAlgNotAllowed
	}

	payloadBytes, err := b64Decode(payloadSeg)
	if err != nil {
		return nil, nil, ErrMalformedToken
	}
	claims = map[string]any{}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, nil, ErrMalformedToken
	}
	// Reject payloads that decode to JSON null or any non-object shape.
	// json.Unmarshal("null", &map) leaves the map nil without error, so
	// an attacker-controlled null-payload token would otherwise slip past
	// standard-claim validation when no issuer/audience/validator is set.
	if claims == nil {
		return nil, nil, ErrMalformedToken
	}

	sigBytes, err := b64Decode(sigSeg)
	if err != nil {
		return nil, nil, ErrMalformedToken
	}
	if len(sigBytes) == 0 {
		// alg!=none but no signature — clearly invalid.
		return nil, nil, ErrInvalidSignature
	}

	key, err := n.keyFunc(header)
	if err != nil {
		return nil, nil, err
	}
	if key == nil {
		return nil, nil, ErrInvalidSignature
	}

	if err := spec.verify(key, signingInput, sigBytes); err != nil {
		return nil, nil, err
	}

	if err := validateStandardClaims(claims, n.issuer, n.audience, n.leeway, time.Now()); err != nil {
		return nil, nil, err
	}
	if n.claimsValidator != nil {
		if err := n.claimsValidator(claims); err != nil {
			return nil, nil, err
		}
	}
	return header, claims, nil
}

// splitToken breaks a compact-serialized JWT into its three segments and
// returns the exact bytes used as the signing input. Re-encoding from
// the parsed header/payload would mutate JSON whitespace and break the
// signature; we keep the original byte slice instead.
func splitToken(token string) (h, p, sig, signingInput []byte, err error) {
	a := strings.IndexByte(token, '.')
	if a < 0 {
		return nil, nil, nil, nil, ErrMalformedToken
	}
	rest := token[a+1:]
	b := strings.IndexByte(rest, '.')
	if b < 0 {
		return nil, nil, nil, nil, ErrMalformedToken
	}
	// Reject 4+ segments.
	if strings.IndexByte(rest[b+1:], '.') >= 0 {
		return nil, nil, nil, nil, ErrMalformedToken
	}
	headerSeg := token[:a]
	payloadSeg := rest[:b]
	sigSeg := rest[b+1:]
	if headerSeg == "" || payloadSeg == "" {
		// Empty segments cannot decode to a valid JOSE structure. An
		// empty signature is allowed by the segment-shape check; the
		// alg-vs-signature relationship is enforced later (alg=none is
		// rejected unconditionally; non-none alg + empty sig becomes
		// ErrInvalidSignature in parseAndVerify).
		return nil, nil, nil, nil, ErrMalformedToken
	}
	signingInput = []byte(token[:a+1+b])
	return []byte(headerSeg), []byte(payloadSeg), []byte(sigSeg), signingInput, nil
}

func b64Encode(src []byte) []byte {
	dst := make([]byte, base64.RawURLEncoding.EncodedLen(len(src)))
	base64.RawURLEncoding.Encode(dst, src)
	return dst
}

func b64Decode(src []byte) ([]byte, error) {
	dst := make([]byte, base64.RawURLEncoding.DecodedLen(len(src)))
	n, err := base64.RawURLEncoding.Decode(dst, src)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// --- contextSource: native-path adapter for tokenSource ---

type contextSource struct{ c *aarv.Context }

func (s contextSource) header(name string) string { return s.c.Header(name) }
func (s contextSource) query(name string) string  { return s.c.Query(name) }
func (s contextSource) cookie(name string) string {
	c, err := s.c.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// --- error response helpers (mirror apikey/basicauth) ---

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
		// Parity with plugins/apikey and plugins/basicauth: fall through
		// to the stdlib reason phrase so validator-returned *aarv.AppError
		// statuses (e.g. 429) produce a readable error field.
		return http.StatusText(status)
	}
}
