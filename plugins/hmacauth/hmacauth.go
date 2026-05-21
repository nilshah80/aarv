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
//
// # Observability
//
// Set Config.Observer to receive an Event after every verification
// attempt — success and failure — carrying the canonical Outcome,
// the client ID, the response status, the wall-clock duration, and
// (for clock-skew failures) the absolute drift in seconds. The hook
// is the supported way to layer tracing, metrics, or audit logging
// without bringing those dependencies into the root aarv module.
//
//	cfg.Observer = func(c *aarv.Context, e hmacauth.Event) {
//	    metrics.IncCounter("hmac.verify",
//	        "outcome", string(e.Outcome),
//	        "client_id", e.ClientID)
//	    if e.Outcome != hmacauth.OutcomeOK {
//	        slog.Warn("hmac verify failed",
//	            "outcome", e.Outcome,
//	            "skew_seconds", e.SkewSeconds)
//	    }
//	}
//
// OpenTelemetry adapters for the hook live in their own modules
// (e.g. plugins/hmacauth-otel) so that the root aarv contract stays
// zero-dependency.
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
type principalContextKey struct{}

const (
	identityStoreKey  = "hmacAuthClient"
	principalStoreKey = "hmacAuthPrincipal"
)

// Principal is the secretless view of an authenticated request.
// Handlers and downstream plugins should read Principal in
// preference to Client when they only need to know "who is this
// request?" — Principal carries the public ClientID and the
// caller-owned Identity, and deliberately does NOT expose Secret
// or Secrets.
//
// Storing the Client struct (with secrets) on the request context
// is convenient for tests and for legacy uses, but it makes the
// secret bytes reachable from any downstream code that holds the
// Context. Principal is the safer-by-default shape: pass it to
// loggers, telemetry, error renderers, and audit hooks without
// having to mask anything.
type Principal struct {
	// ClientID is the authenticated client's public identifier.
	// Always populated on a successfully authenticated request.
	ClientID string

	// Identity is the opaque caller-owned metadata copied from
	// Client.Identity at verification time. Same shape as before;
	// the only difference vs reading Client.Identity is that
	// Principal does not also expose the secret material.
	Identity any
}

// principalOf builds the secretless view from a verified Client.
// Internal helper — callers should use From / PrincipalFrom.
func principalOf(c Client) Principal {
	return Principal{
		ClientID: c.ClientID,
		Identity: c.Identity,
	}
}

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
	Validator  Validator
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

	// Observer, when non-nil, is invoked once per verification attempt
	// (after Skipper bypass; before the handler runs on success, after
	// the response status is decided on failure). Observer is the
	// supported way to layer tracing, metrics, or audit logging on top
	// of hmacauth without bringing those dependencies into the root
	// aarv module — tracing-aware companions live in their own
	// modules (e.g. plugins/hmacauth-otel) and wire themselves in via
	// this hook. See observer.go for the Outcome and Event shapes.
	Observer Observer

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
	observer        Observer
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
		observer:        cfg.Observer,
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
//
// Timing for the Observer Event is gated on n.observer != nil. When no
// Observer is configured the verify path makes no extra observer
// timing calls and constructs no Event value — verify() itself still
// reads n.now() once for the skew-window check, which is unrelated to
// the Observer pathway.
func (n *normalized) verifyNative(c *aarv.Context) error {
	hasObs := n.observer != nil
	var start time.Time
	if hasObs {
		start = n.now()
	}
	r := c.RawRequest()
	clientID := c.Header(n.clientIDHeader)
	tsStr := c.Header(n.timestampHeader)
	nonce := c.Header(n.nonceHeader)
	sigHex := c.Header(n.signatureHeader)

	body, status, readOutcome, err := n.readBody(c, r)
	if err != nil {
		if hasObs {
			n.observer(c, Event{
				Outcome:  readOutcome,
				ClientID: clientID,
				Status:   status,
				Duration: n.now().Sub(start),
			})
		}
		return n.errNative(c, status, n.errorMessage)
	}

	res := n.verify(c.Context(), clientID, tsStr, nonce, sigHex, r.Method, r.URL.Path, r.URL.Query(), body)
	if !res.ok {
		if hasObs {
			n.observer(c, Event{
				Outcome:     res.outcome,
				ClientID:    clientID,
				Status:      res.status,
				SkewSeconds: res.skewSeconds,
				Duration:    n.now().Sub(start),
			})
		}
		return n.errNative(c, res.status, n.errorMessage)
	}

	principal := principalOf(res.client)
	c.Set(identityStoreKey, res.client)
	c.Set(principalStoreKey, principal)
	c.SetContextValue(contextKey{}, res.client)
	c.SetContextValue(principalContextKey{}, principal)
	if hasObs {
		n.observer(c, Event{
			Outcome:  OutcomeOK,
			ClientID: res.client.ClientID,
			Duration: n.now().Sub(start),
		})
	}
	return nil
}

// verifyStdlib runs the verification on the stdlib path. On failure
// it writes the response itself and returns a sentinel error so the
// outer middleware can short-circuit.
//
// Same nil-observer gating contract as verifyNative: when no Observer
// is configured the verify path makes no extra observer timing calls
// and constructs no Event value. (verify() still calls n.now() once
// for the skew-window check; that's required by verification, not by
// the Observer.)
func (n *normalized) verifyStdlib(w http.ResponseWriter, r *http.Request) (*http.Request, error) {
	hasObs := n.observer != nil
	var start time.Time
	if hasObs {
		start = n.now()
	}
	clientID := r.Header.Get(n.clientIDHeader)
	tsStr := r.Header.Get(n.timestampHeader)
	nonce := r.Header.Get(n.nonceHeader)
	sigHex := r.Header.Get(n.signatureHeader)

	// Recover the bridged aarv.Context once up front; the Observer
	// receives c (possibly nil) and we use it again on the success
	// path when the framework bridge is present.
	bridged, _ := aarv.FromRequest(r)

	body, status, readOutcome, err := n.readBodyStdlib(r)
	if err != nil {
		if hasObs {
			n.observer(bridged, Event{
				Outcome:  readOutcome,
				ClientID: clientID,
				Status:   status,
				Duration: n.now().Sub(start),
			})
		}
		n.errStdlib(w, r, status, n.errorMessage)
		return nil, errStop
	}

	res := n.verify(r.Context(), clientID, tsStr, nonce, sigHex, r.Method, r.URL.Path, r.URL.Query(), body)
	if !res.ok {
		if hasObs {
			n.observer(bridged, Event{
				Outcome:     res.outcome,
				ClientID:    clientID,
				Status:      res.status,
				SkewSeconds: res.skewSeconds,
				Duration:    n.now().Sub(start),
			})
		}
		n.errStdlib(w, r, res.status, n.errorMessage)
		return nil, errStop
	}

	principal := principalOf(res.client)
	if bridged != nil {
		bridged.Set(identityStoreKey, res.client)
		bridged.Set(principalStoreKey, principal)
		bridged.SetContextValue(contextKey{}, res.client)
		bridged.SetContextValue(principalContextKey{}, principal)
		if hasObs {
			n.observer(bridged, Event{
				Outcome:  OutcomeOK,
				ClientID: res.client.ClientID,
				Duration: n.now().Sub(start),
			})
		}
		return bridged.RawRequest(), nil
	}
	ctx := context.WithValue(r.Context(), contextKey{}, res.client)
	ctx = context.WithValue(ctx, principalContextKey{}, principal)
	if hasObs {
		n.observer(nil, Event{
			Outcome:  OutcomeOK,
			ClientID: res.client.ClientID,
			Duration: n.now().Sub(start),
		})
	}
	return r.WithContext(ctx), nil
}

var errStop = errors.New("hmacauth: response written")

// verifyResult is the shape verify returns. It carries the auth
// decision plus the data the Observer hook needs to classify the
// outcome — particularly the Outcome enum and, for clock-skew
// failures, the absolute drift in seconds.
type verifyResult struct {
	client      Client
	ok          bool
	status      int
	outcome     Outcome
	skewSeconds int64
}

// verify is the shared per-request decision logic. It returns a
// status only as a hint to the caller for response shaping —
// failure modes always collapse to 401 in the response, but body
// overflow returns 413 separately because that maps to a different
// recovery path on the client.
//
// On reject: ok=false, status=401 or 413, outcome populated. On
// accept: ok=true, status=0, outcome=OutcomeOK, client populated.
func (n *normalized) verify(ctx context.Context, clientID, tsStr, nonce, sigHex, method, path string, query map[string][]string, body []byte) verifyResult {
	if clientID == "" || tsStr == "" || nonce == "" || sigHex == "" {
		return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeUnauthorized}
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil || ts <= 0 {
		return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeUnauthorized}
	}
	// Reject absurdly large timestamps before doing arithmetic on
	// them. The 2^53 ceiling matches JavaScript's safe-integer bound
	// (the most common cross-language source of timestamp drift) and
	// keeps `now-ts` well clear of int64 overflow even for distant
	// future or past values. Anything past 2^53 seconds (~285 million
	// years from epoch) is unambiguously a parse bug or a malicious
	// input — there is no legitimate skew-window argument for it.
	const maxSafeTimestamp = int64(1) << 53
	if ts > maxSafeTimestamp {
		return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeUnauthorized}
	}

	now := n.now().Unix()
	if drift := abs64(now - ts); drift > n.skewSeconds {
		return verifyResult{
			ok:          false,
			status:      http.StatusUnauthorized,
			outcome:     OutcomeClockSkew,
			skewSeconds: drift,
		}
	}

	receivedSig, err := hex.DecodeString(sigHex)
	if err != nil || len(receivedSig) != sha256.Size {
		return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeSignatureInvalid}
	}

	client, err := n.validator(clientID)
	if err != nil {
		return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeUnauthorized}
	}
	// Reject a zero-ish client. Both the empty-ClientID case AND
	// the no-secret case are treated as "unknown" — a custom
	// Validator that returns Client{ClientID: ""} would otherwise
	// sign all such requests under a single empty-string nonce
	// namespace, letting one client's replay invalidate another
	// client's traffic.
	if client.ClientID == "" {
		return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeUnauthorized}
	}
	if len(client.Secret) == 0 && len(client.Secrets) == 0 {
		return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeUnauthorized}
	}

	canonical := canonicalRequest(method, path, query, body, ts, nonce)
	matched := compareAllSecrets(canonical, receivedSig, client.Secret, client.Secrets)
	if !matched {
		return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeSignatureInvalid}
	}

	if n.store != nil {
		fresh, err := n.store.SetNX(ctx, "nonce:"+clientID+":"+nonce, n.nonceTTL)
		if err != nil {
			// Nonce-store transport failure (Redis unreachable, etc.).
			// We must reject the request — without a successful SetNX
			// we cannot rule out a replay — but reporting this as
			// OutcomeReplayDetected would make a Redis outage look
			// like a flood of replay attacks on dashboards. Surface
			// it as OutcomeUnauthorized so the operator sees an
			// auth-availability incident, not a security incident.
			return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeUnauthorized}
		}
		if !fresh {
			// Nonce was already present — genuine replay.
			return verifyResult{ok: false, status: http.StatusUnauthorized, outcome: OutcomeReplayDetected}
		}
	}

	return verifyResult{client: client, ok: true, status: 0, outcome: OutcomeOK}
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
//
// Returns (body, status, outcome, err). On the happy path:
// (body, 0, OutcomeOK, nil). On overflow: (nil, 413,
// OutcomeBodyTooLarge, MaxBytesError). On any other I/O failure:
// (nil, 401, OutcomeUnauthorized, transportErr) — observers should
// see this as a generic auth failure rather than a body-size signal.
func (n *normalized) readBody(c *aarv.Context, r *http.Request) ([]byte, int, Outcome, error) {
	if r.Body == nil {
		return nil, 0, OutcomeOK, nil
	}
	limited := http.MaxBytesReader(c.Response(), r.Body, n.maxBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, http.StatusRequestEntityTooLarge, OutcomeBodyTooLarge, err
		}
		// Transport-level failure (connection reset, partial read,
		// etc.). The signature can't be verified on a body we could
		// not read, so we collapse to 401 — but the outcome is
		// OutcomeUnauthorized rather than OutcomeBodyTooLarge so
		// dashboards don't see transport blips as "body too large".
		return nil, http.StatusUnauthorized, OutcomeUnauthorized, err
	}
	c.SetBody(io.NopCloser(bytesReader(body)))
	return body, 0, OutcomeOK, nil
}

// readBodyStdlib is the stdlib path equivalent. It uses the
// max+1 read pattern because stdlib middleware does not have the
// aarv response writer hooked up at this point — emitting 413 is
// done by errStdlib below.
//
// Same return contract as readBody: (body, status, outcome, err).
func (n *normalized) readBodyStdlib(r *http.Request) ([]byte, int, Outcome, error) {
	if r.Body == nil {
		return nil, 0, OutcomeOK, nil
	}
	cap := n.maxBodyBytes
	buf := make([]byte, 0, min64(cap+1, 64<<10))
	tmp := make([]byte, 32<<10)
	for {
		nRead, err := r.Body.Read(tmp)
		if nRead > 0 {
			buf = append(buf, tmp[:nRead]...)
			if int64(len(buf)) > cap {
				return nil, http.StatusRequestEntityTooLarge, OutcomeBodyTooLarge, errBodyOverflow
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, http.StatusUnauthorized, OutcomeUnauthorized, err
		}
	}
	r.Body = io.NopCloser(bytesReader(buf))
	if c, ok := aarv.FromRequest(r); ok {
		c.SetBody(r.Body)
	}
	return buf, 0, OutcomeOK, nil
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
//
// The returned Client carries the secret material (Secret /
// Secrets) — useful for tests and for legacy callers, but most
// handlers want PrincipalFrom instead, which exposes only the
// public ClientID and caller-owned Identity without surfacing the
// HMAC keys to downstream code.
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
//
// Same secret-exposure caveat as From applies: prefer
// PrincipalFromContext for handler code that does not need the raw
// HMAC keys.
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

// PrincipalFrom retrieves the secretless Principal of the
// authenticated request from an aarv.Context. Returns
// (Principal{}, false) when the middleware did not run on this
// route or authentication failed.
//
// PrincipalFrom is the recommended accessor for handler code,
// loggers, audit hooks, and telemetry: it carries the public
// ClientID and the caller-owned Identity without exposing the HMAC
// secret bytes to downstream code that has no business with them.
func PrincipalFrom(c *aarv.Context) (Principal, bool) {
	if c == nil {
		return Principal{}, false
	}
	v, ok := c.Get(principalStoreKey)
	if !ok {
		return Principal{}, false
	}
	p, ok := v.(Principal)
	return p, ok
}

// PrincipalFromContext is the context.Context-keyed equivalent of
// PrincipalFrom. Use it from handlers and downstream plugins that
// only have r.Context() in scope.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	v := ctx.Value(principalContextKey{})
	if v == nil {
		return Principal{}, false
	}
	p, ok := v.(Principal)
	return p, ok
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
