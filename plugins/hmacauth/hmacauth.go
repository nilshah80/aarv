// Package hmacauth provides HMAC-SHA256 signed-request authentication
// middleware for the aarv framework.
//
// # Threat model
//
// The middleware verifies that every protected request was signed by
// a known client using a shared secret, that the request body, query,
// path, and method have not been tampered with in transit, and that
// the request is fresh (within a configured clock-skew window) and
// not a replay (within the nonce TTL).
//
// It is NOT a substitute for TLS. Always terminate the listener on
// TLS — HMAC authentication does not protect the request from passive
// inspection or active downgrade attacks.
//
// # Canonical request
//
// The signed bytes are exactly:
//
//	METHOD\nPATH\nCANONICAL_QUERY\nHEX(SHA256(body))\nTIMESTAMP\nNONCE
//
// where CANONICAL_QUERY is keys+values sorted ASCII-ascending and
// percent-encoded per RFC 3986 §2.3 (NOT application/x-www-form-
// urlencoded — see the canonicalQuery comment for why). TIMESTAMP is
// Unix seconds in base-10 ASCII. The body hash is the lowercase hex
// SHA-256 digest of the request body bytes (the empty body hashes to
// e3b0c44...).
//
// # Required headers
//
// Every signed request carries four headers (names configurable):
//
//	X-Client-Id    — public client identifier
//	X-Timestamp    — Unix seconds, base-10
//	X-Nonce        — caller-generated nonce (recommended: 16 random bytes hex)
//	X-Signature    — lowercase hex of HMAC-SHA256(secret, canonical request)
//
// Missing or malformed values fail verification with a generic 401.
// The middleware deliberately never reveals which check failed.
//
// # Recommended middleware order
//
//	requestid -> recover -> bodylimit -> hmacauth -> handler
//
// hmacauth itself enforces a per-instance MaxBodyBytes cap so the
// signature check cannot be coerced into reading an unbounded request,
// but it does not replace plugins/bodylimit — the smaller of the two
// caps wins, and bodylimit's response shape is preserved.
//
// # Identity rotation
//
// To rotate a client's secret without downtime, populate both Secret
// and Secrets[0] with the new bytes and Secrets[1] with the old bytes
// (or any superset). The verifier checks every candidate without
// short-circuiting on the first match, so the time spent verifying
// does not depend on which secret matched. Remove the old entry from
// Secrets only after every signing client has migrated.
//
// # Replay protection
//
// On successful signature verification the middleware records the
// nonce via NonceStore.SetNX. The store MUST guarantee atomic insert
// semantics across goroutines and processes. Provide a Redis-backed
// store (plugins/hmacauth-redis) for multi-instance deployments;
// MemoryNonceStore is provided for development and single-instance
// use. Configuring NonceStore to nil disables replay protection and
// emits a one-time warning at startup.
package hmacauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
)

type contextKey struct{}

const identityStoreKey = "hmacAuthClient"

// Default header names. Callers can override via Config.
const (
	DefaultClientIDHeader  = "X-Client-Id"
	DefaultTimestampHeader = "X-Timestamp"
	DefaultNonceHeader     = "X-Nonce"
	DefaultSignatureHeader = "X-Signature"

	// DefaultSkewSeconds bounds how far the request timestamp can
	// drift from the server clock. Five minutes matches AWS SigV4
	// and is a generous default for clients without NTP.
	DefaultSkewSeconds = 300

	// DefaultMaxBodyBytes caps the body bytes the verifier reads for
	// hashing. Aligns with the idempotency plugin's default so the
	// canonical middleware order does not surprise callers with two
	// different limits.
	DefaultMaxBodyBytes = 1 << 20
)

// ErrorHandler builds a custom rejection response. When non-nil it
// preempts the default JSON error path. The middleware always passes
// status 401 and a generic message; handlers that need richer error
// shapes (e.g. RFC 7807 problem details) can wrap this hook.
type ErrorHandler func(c *aarv.Context, status int, message string) error

// Skipper returns true to bypass verification for a given request.
// Useful for health/probe endpoints that must remain reachable
// without credentials.
type Skipper func(*aarv.Context) bool

// Config holds middleware configuration. Validator is required;
// every other field has a sane default exposed as a Default*
// constant.
type Config struct {
	Validator Validator
	NonceStore NonceStore

	ClientIDHeader  string
	TimestampHeader string
	NonceHeader     string
	SignatureHeader string

	// SkewSeconds is the maximum absolute difference between the
	// signed timestamp and server time. Zero uses DefaultSkewSeconds;
	// negative values panic.
	SkewSeconds int64

	// NonceTTL is how long a nonce is remembered for replay
	// detection. Zero defaults to 2*SkewSeconds + 60s; explicit
	// negative values panic at construction.
	NonceTTL time.Duration

	// MaxBodyBytes caps the body bytes read for hashing. Bodies
	// exceeding the cap are rejected with 413, matching plugins/
	// bodylimit semantics. Zero uses DefaultMaxBodyBytes; negative
	// values panic.
	MaxBodyBytes int64

	// ErrorMessage is the single message returned to every failure
	// mode. Default: "missing or invalid authentication".
	ErrorMessage string

	ErrorHandler ErrorHandler
	Skipper      Skipper

	// Now overrides time.Now for tests. Production code leaves this
	// nil.
	Now func() time.Time
}

// DefaultConfig returns a Config populated with the plugin defaults.
// The caller must still set Validator before passing it to New.
func DefaultConfig() Config {
	return Config{
		ClientIDHeader:  DefaultClientIDHeader,
		TimestampHeader: DefaultTimestampHeader,
		NonceHeader:     DefaultNonceHeader,
		SignatureHeader: DefaultSignatureHeader,
		SkewSeconds:     DefaultSkewSeconds,
		MaxBodyBytes:    DefaultMaxBodyBytes,
		ErrorMessage:    "missing or invalid authentication",
	}
}

// New constructs the HMAC verification middleware. Panics on missing
// Validator, negative SkewSeconds, negative NonceTTL, or
// negative MaxBodyBytes — these are all misconfigurations that
// would silently weaken auth.
func New(cfg Config) aarv.Middleware {
	n := normalize(cfg)

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if n.skipper != nil && n.skipper(c) {
				return next(c)
			}
			if err := n.verifyNative(c); err != nil {
				return err
			}
			return next(c)
		}
	})

	stdlib := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c, ok := aarv.FromRequest(r); ok && n.skipper != nil && n.skipper(c) {
				next.ServeHTTP(w, r)
				return
			}
			r2, err := n.verifyStdlib(w, r)
			if err != nil {
				return
			}
			next.ServeHTTP(w, r2)
		})
	})

	return aarv.RegisterNativeMiddleware(stdlib, native)
}

type normalized struct {
	validator       Validator
	store           NonceStore
	clientIDHeader  string
	timestampHeader string
	nonceHeader     string
	signatureHeader string
	skewSeconds     int64
	nonceTTL        time.Duration
	maxBodyBytes    int64
	errorMessage    string
	errorHandler    ErrorHandler
	skipper         Skipper
	now             func() time.Time
}

func normalize(cfg Config) *normalized {
	if cfg.Validator == nil {
		panic("hmacauth: Config.Validator is required")
	}
	if cfg.SkewSeconds < 0 {
		panic("hmacauth: Config.SkewSeconds must be >= 0")
	}
	if cfg.NonceTTL < 0 {
		panic("hmacauth: Config.NonceTTL must not be negative")
	}
	if cfg.MaxBodyBytes < 0 {
		panic("hmacauth: Config.MaxBodyBytes must not be negative")
	}

	n := &normalized{
		validator:       cfg.Validator,
		store:           cfg.NonceStore,
		clientIDHeader:  defaulted(cfg.ClientIDHeader, DefaultClientIDHeader),
		timestampHeader: defaulted(cfg.TimestampHeader, DefaultTimestampHeader),
		nonceHeader:     defaulted(cfg.NonceHeader, DefaultNonceHeader),
		signatureHeader: defaulted(cfg.SignatureHeader, DefaultSignatureHeader),
		skewSeconds:     cfg.SkewSeconds,
		nonceTTL:        cfg.NonceTTL,
		maxBodyBytes:    cfg.MaxBodyBytes,
		errorMessage:    defaulted(cfg.ErrorMessage, "missing or invalid authentication"),
		errorHandler:    cfg.ErrorHandler,
		skipper:         cfg.Skipper,
		now:             cfg.Now,
	}
	if n.now == nil {
		n.now = time.Now
	}
	if n.skewSeconds == 0 {
		n.skewSeconds = DefaultSkewSeconds
	}
	if n.maxBodyBytes == 0 {
		n.maxBodyBytes = DefaultMaxBodyBytes
	}
	if n.nonceTTL == 0 {
		n.nonceTTL = time.Duration(2*n.skewSeconds)*time.Second + 60*time.Second
	}
	if n.store == nil {
		warnReplayDisabledOnce.Do(func() {
			slog.Warn("hmacauth: NonceStore is nil; replay protection is disabled")
		})
	}
	return n
}

var warnReplayDisabledOnce sync.Once

// resetWarnReplayOnceForTesting resets the one-shot warning latch.
// Test-only — the production code never calls it.
func resetWarnReplayOnceForTesting() {
	warnReplayDisabledOnce = sync.Once{}
}

func defaulted(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// verifyNative runs the verification on the native (aarv.Context)
// path. On failure it returns an *aarv.AppError so the framework's
// error handler emits the standard JSON shape.
func (n *normalized) verifyNative(c *aarv.Context) error {
	r := c.RawRequest()
	clientID := c.Header(n.clientIDHeader)
	tsStr := c.Header(n.timestampHeader)
	nonce := c.Header(n.nonceHeader)
	sigHex := c.Header(n.signatureHeader)

	body, status, err := n.readBody(c, r)
	if err != nil {
		return n.errNative(c, status, n.errorMessage)
	}

	client, ok, status := n.verify(c.Context(), clientID, tsStr, nonce, sigHex, r.Method, r.URL.Path, r.URL.Query(), body)
	if !ok {
		return n.errNative(c, status, n.errorMessage)
	}

	c.Set(identityStoreKey, client)
	c.SetContextValue(contextKey{}, client)
	return nil
}

// verifyStdlib runs the verification on the stdlib path. On failure
// it writes the response itself and returns a sentinel error so the
// outer middleware can short-circuit.
func (n *normalized) verifyStdlib(w http.ResponseWriter, r *http.Request) (*http.Request, error) {
	clientID := r.Header.Get(n.clientIDHeader)
	tsStr := r.Header.Get(n.timestampHeader)
	nonce := r.Header.Get(n.nonceHeader)
	sigHex := r.Header.Get(n.signatureHeader)

	body, status, err := n.readBodyStdlib(r)
	if err != nil {
		n.errStdlib(w, r, status, n.errorMessage)
		return nil, errStop
	}

	client, ok, status := n.verify(r.Context(), clientID, tsStr, nonce, sigHex, r.Method, r.URL.Path, r.URL.Query(), body)
	if !ok {
		n.errStdlib(w, r, status, n.errorMessage)
		return nil, errStop
	}

	if c, ok := aarv.FromRequest(r); ok {
		c.Set(identityStoreKey, client)
		c.SetContextValue(contextKey{}, client)
		return c.RawRequest(), nil
	}
	ctx := context.WithValue(r.Context(), contextKey{}, client)
	return r.WithContext(ctx), nil
}

var errStop = errors.New("hmacauth: response written")

// verify is the shared per-request decision logic. It returns a
// status only as a hint to the caller for response shaping —
// failure modes always collapse to 401 in the response, but body
// overflow returns 413 separately because that maps to a different
// recovery path on the client.
//
// Returning (Client{}, false, status) means "reject"; (client, true, 0)
// means "accept".
func (n *normalized) verify(ctx context.Context, clientID, tsStr, nonce, sigHex, method, path string, query map[string][]string, body []byte) (Client, bool, int) {
	if clientID == "" || tsStr == "" || nonce == "" || sigHex == "" {
		return Client{}, false, http.StatusUnauthorized
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil || ts <= 0 {
		return Client{}, false, http.StatusUnauthorized
	}

	now := n.now().Unix()
	if abs64(now-ts) > n.skewSeconds {
		return Client{}, false, http.StatusUnauthorized
	}

	receivedSig, err := hex.DecodeString(sigHex)
	if err != nil || len(receivedSig) != sha256.Size {
		return Client{}, false, http.StatusUnauthorized
	}

	client, err := n.validator(clientID)
	if err != nil {
		return Client{}, false, http.StatusUnauthorized
	}
	// Reject a zero-ish client. Both the empty-ClientID case AND
	// the no-secret case are treated as "unknown" — a custom
	// Validator that returns Client{ClientID: ""} would otherwise
	// sign all such requests under a single empty-string nonce
	// namespace, letting one client's replay invalidate another
	// client's traffic.
	if client.ClientID == "" {
		return Client{}, false, http.StatusUnauthorized
	}
	if len(client.Secret) == 0 && len(client.Secrets) == 0 {
		return Client{}, false, http.StatusUnauthorized
	}

	canonical := canonicalRequest(method, path, query, body, ts, nonce)
	matched := compareAllSecrets(canonical, receivedSig, client.Secret, client.Secrets)
	if !matched {
		return Client{}, false, http.StatusUnauthorized
	}

	if n.store != nil {
		fresh, err := n.store.SetNX(ctx, "nonce:"+clientID+":"+nonce, n.nonceTTL)
		if err != nil || !fresh {
			return Client{}, false, http.StatusUnauthorized
		}
	}

	return client, true, 0
}

// compareAllSecrets verifies the received signature against every
// candidate secret without short-circuiting. The accumulator-OR
// pattern is intentional: short-circuiting on the first match would
// leak which secret in the rotation set succeeded via timing.
//
// Empty/nil candidate slices are skipped (an empty HMAC key produces
// a deterministic but useless digest, and accepting it could let a
// caller authenticate with an unconfigured rotation slot).
func compareAllSecrets(canonical, received []byte, primary []byte, rotation [][]byte) bool {
	matched := false
	if len(primary) > 0 {
		mac := hmac.New(sha256.New, primary)
		mac.Write(canonical)
		expected := mac.Sum(nil)
		if subtle.ConstantTimeCompare(expected, received) == 1 {
			matched = true
		}
	}
	for _, s := range rotation {
		if len(s) == 0 {
			continue
		}
		mac := hmac.New(sha256.New, s)
		mac.Write(canonical)
		expected := mac.Sum(nil)
		if subtle.ConstantTimeCompare(expected, received) == 1 {
			matched = true
		}
	}
	return matched
}

// readBody is the native-path body reader. It uses
// http.MaxBytesReader so the framework's response writer machinery
// is wired up for the 413 path, then re-injects the buffered bytes
// via Context.SetBody so downstream binders see the same body.
func (n *normalized) readBody(c *aarv.Context, r *http.Request) ([]byte, int, error) {
	if r.Body == nil {
		return nil, 0, nil
	}
	limited := http.MaxBytesReader(c.Response(), r.Body, n.maxBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		// MaxBytesReader returns *http.MaxBytesError on overflow;
		// any other error is a transport problem and also collapses
		// to 401 (we cannot verify a signature on a body we could
		// not read).
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, http.StatusRequestEntityTooLarge, err
		}
		return nil, http.StatusUnauthorized, err
	}
	c.SetBody(io.NopCloser(bytesReader(body)))
	return body, 0, nil
}

// readBodyStdlib is the stdlib path equivalent. It uses the
// max+1 read pattern because stdlib middleware does not have the
// aarv response writer hooked up at this point — emitting 413 is
// done by errStdlib below.
func (n *normalized) readBodyStdlib(r *http.Request) ([]byte, int, error) {
	if r.Body == nil {
		return nil, 0, nil
	}
	cap := n.maxBodyBytes
	buf := make([]byte, 0, min64(cap+1, 64<<10))
	tmp := make([]byte, 32<<10)
	for {
		nRead, err := r.Body.Read(tmp)
		if nRead > 0 {
			buf = append(buf, tmp[:nRead]...)
			if int64(len(buf)) > cap {
				return nil, http.StatusRequestEntityTooLarge, errBodyOverflow
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, http.StatusUnauthorized, err
		}
	}
	r.Body = io.NopCloser(bytesReader(buf))
	if c, ok := aarv.FromRequest(r); ok {
		c.SetBody(r.Body)
	}
	return buf, 0, nil
}

var errBodyOverflow = errors.New("hmacauth: body exceeds MaxBodyBytes")

// errNative emits a verification failure on the native path.
func (n *normalized) errNative(c *aarv.Context, status int, message string) error {
	if status == 0 {
		status = http.StatusUnauthorized
	}
	if n.errorHandler != nil {
		return n.errorHandler(c, status, message)
	}
	switch status {
	case http.StatusRequestEntityTooLarge:
		return aarv.NewError(status, "payload_too_large", message)
	default:
		return aarv.ErrUnauthorized(message)
	}
}

// errStdlib writes a JSON failure response on the stdlib path.
func (n *normalized) errStdlib(w http.ResponseWriter, r *http.Request, status int, message string) {
	if status == 0 {
		status = http.StatusUnauthorized
	}
	if n.errorHandler != nil {
		// The stdlib path does not have a context to pass to a
		// caller-supplied error handler that takes *aarv.Context.
		// Recover the bridged context if it is available; if not,
		// fall through to the default JSON shape so callers do not
		// see a different response shape on the stdlib side.
		if c, ok := aarv.FromRequest(r); ok {
			if err := n.errorHandler(c, status, message); err != nil {
				// Caller's handler returned an error; surface it
				// the same way the framework would. We do not have
				// app.handleError here, so collapse to the default
				// shape.
				writeJSONError(w, r, status, message)
				return
			}
			return
		}
	}
	writeJSONError(w, r, status, message)
}

type errorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func writeJSONError(w http.ResponseWriter, r *http.Request, status int, message string) {
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
	case http.StatusRequestEntityTooLarge:
		return "payload_too_large"
	default:
		return http.StatusText(status)
	}
}

// From retrieves the authenticated Client from an aarv.Context.
// Returns (Client{}, false) if the middleware did not run on this
// route or auth failed.
func From(c *aarv.Context) (Client, bool) {
	if c == nil {
		return Client{}, false
	}
	v, ok := c.Get(identityStoreKey)
	if !ok {
		return Client{}, false
	}
	client, ok := v.(Client)
	return client, ok
}

// FromContext retrieves the authenticated Client from a request's
// context.Context. Useful for handlers and downstream plugins that
// only have r.Context() in scope.
func FromContext(ctx context.Context) (Client, bool) {
	if ctx == nil {
		return Client{}, false
	}
	v := ctx.Value(contextKey{})
	if v == nil {
		return Client{}, false
	}
	client, ok := v.(Client)
	return client, ok
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// bytesReader wraps a []byte in an io.Reader without depending on
// bytes.NewReader (one less import to track when this file is
// compiled into the linker output).
type bytesReaderImpl struct {
	b   []byte
	pos int
}

func bytesReader(b []byte) *bytesReaderImpl { return &bytesReaderImpl{b: b} }

func (r *bytesReaderImpl) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}
