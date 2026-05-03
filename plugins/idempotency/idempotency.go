// Package idempotency provides RFC-aligned idempotency-key middleware
// for the aarv framework.
//
// On the first request carrying an Idempotency-Key header (configurable),
// the middleware Locks the key, captures the response (status, headers,
// body), Saves it with a TTL, and returns it. On a retry with the same
// key, the cached response is replayed verbatim with an extra
// Idempotency-Replayed: true response header.
//
// # SafeMethods nil-vs-empty contract
//
//	SafeMethods: nil                                                // defaults: GET, HEAD, OPTIONS bypass
//	SafeMethods: []string{http.MethodHead, http.MethodOptions}      // GET participates
//	SafeMethods: []string{}                                         // every method participates, including GET
//
// New substitutes built-in defaults only when the slice is nil; an empty
// non-nil slice is honored verbatim.
//
// # CacheStatuses nil-vs-empty contract
//
//	CacheStatuses: nil                  // default: cache 2xx and 3xx
//	CacheStatuses: []int{200, 201, 202} // cache only these
//	CacheStatuses: []int{}              // cache nothing — every response bypasses the cache
//
// # CachedHeaders allowlist
//
// Only headers in the CachedHeaders allowlist are persisted (and
// therefore replayed on retries). The default allowlist is
// Content-Type, Content-Encoding, Cache-Control, Location, ETag.
// Hop-by-hop headers, Set-Cookie, Authorization, WWW-Authenticate,
// and X-Request-Id are ALWAYS stripped, regardless of allowlist
// configuration — replaying them would carry stale per-request
// security state into a different request's response.
//
// # CacheStatusFunc
//
// CacheStatusFunc, when non-nil, fully replaces the CacheStatuses
// allowlist as the per-status caching predicate. Use it when the
// caching policy is simpler to express in code than as a list (e.g.
// "cache 2xx, plus deterministic 4xx like 409, but never 5xx").
//
// # ConflictWait + WaitableStore
//
// ConflictWait requires the configured Store to implement WaitableStore.
// Stores that don't implement it behave exactly like ConflictReject —
// returning 409 immediately on contention. No polling, no busy wait.
//
// # Per-route TTL
//
// Routes registered with aarv.WithRouteIdempotencyTTL override the
// middleware's global TTL for that single route. The override is
// resolved at request time via Context.RouteIdempotencyTTL, so the
// global Config.TTL is the fallback for routes that did not set one.
// A route registered with TTL == 0 opts out of caching for that
// route only — the response is returned but not persisted.
//
// # Recommended middleware order
//
//	requestid -> recover -> hmacauth -> idempotency -> handler
//
// Place idempotency AFTER hmacauth so the lock key is derived from
// an authenticated request — otherwise an unauthenticated caller
// could pollute the lock space with arbitrary keys.
//
// # Payload-mismatch error code
//
// When HashRequestBody is enabled and a retry with the same key
// arrives carrying a different body, the middleware responds with
// 422 and the JSON error code "idempotency_key_reused_with_different_payload".
// Other 422 paths (none today) emit the generic "unprocessable_entity"
// code; the specific code lets clients reliably identify replays
// against drifted payloads.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
)

// ConflictBehavior selects what happens when two concurrent requests
// arrive with the same Idempotency-Key while the first is still
// in-flight.
type ConflictBehavior int

const (
	// ConflictReject returns 409 Conflict immediately.
	ConflictReject ConflictBehavior = iota

	// ConflictWait blocks the second request up to WaitTimeout, then
	// replays the cached response. Falls back to ConflictReject when the
	// configured Store does not implement WaitableStore.
	ConflictWait
)

// Skipper bypasses the middleware when it returns true. OR-combined with
// SkipPaths.
type Skipper func(*aarv.Context) bool

// ErrorHandler builds a custom rejection / error response. When non-nil
// it preempts the default JSON error path.
type ErrorHandler func(c *aarv.Context, status int, message string) error

// Config holds the middleware configuration.
type Config struct {
	HeaderName      string
	Store           Store
	TTL             time.Duration
	SafeMethods     []string // nil-vs-empty contract above
	RequireKey      bool
	HashRequestBody bool

	// MaxRequestBodyBytes caps the request body read for hashing when
	// HashRequestBody is set, so a large body cannot force unbounded
	// memory use ahead of any downstream bodylimit middleware. Bodies
	// exceeding the cap are rejected with 413.
	//
	// Zero means "use the built-in 1 MiB default" — there is no
	// unbounded mode. Callers needing a higher cap must set an explicit
	// positive value; callers who do not need hashing should set
	// HashRequestBody:false and MaxRequestBodyBytes is then ignored.
	// Negative values are normalized to zero (which then resolves to
	// the default).
	MaxRequestBodyBytes int64

	ConflictBehavior ConflictBehavior
	WaitTimeout      time.Duration
	CacheStatuses    []int // nil-vs-empty contract above

	// CacheStatusFunc, when non-nil, fully replaces CacheStatuses as
	// the per-status caching predicate. See package doc.
	CacheStatusFunc func(status int) bool

	// CachedHeaders restricts which response header names are
	// persisted (and therefore replayed). The default allowlist
	// when nil is: Content-Type, Content-Encoding, Cache-Control,
	// Location, ETag. An empty non-nil slice means "do not persist
	// any header" (the replayed response will only carry the status
	// and body). Header names are matched via http.CanonicalHeaderKey.
	//
	// The hardcoded blocklist (Set-Cookie, Authorization,
	// WWW-Authenticate, X-Request-Id, hop-by-hop) is always applied
	// AFTER the allowlist; callers cannot opt back into replaying
	// those headers via this knob.
	CachedHeaders []string

	MaxResponseBytes int64
	Skipper          Skipper
	SkipPaths        []string
	ErrorHandler     ErrorHandler
}

// DefaultConfig preserves the nil semantics for SafeMethods and
// CacheStatuses — caller-side defaults are produced inside New().
func DefaultConfig() Config {
	return Config{
		HeaderName:          "Idempotency-Key",
		TTL:                 24 * time.Hour,
		ConflictBehavior:    ConflictReject,
		MaxResponseBytes:    4 << 20,
		MaxRequestBodyBytes: 1 << 20,
	}
}

type normalized struct {
	headerName          string
	store               Store
	waitable            WaitableStore // nil when store is not WaitableStore
	ttl                 time.Duration
	safeMethods         map[string]struct{}
	requireKey          bool
	hashRequestBody     bool
	maxRequestBodyBytes int64
	conflictBehavior    ConflictBehavior
	waitTimeout         time.Duration
	cacheStatuses       map[int]struct{} // nil = cache 2xx/3xx; empty map = cache nothing
	cacheNothing        bool
	cacheStatusFunc     func(int) bool
	cachedHeaders       map[string]struct{} // nil = built-in allowlist; empty = drop all
	maxResponseBytes    int64
	skipper             Skipper
	skipPaths           map[string]struct{}
	errFn               ErrorHandler
}

// PayloadMismatchErrorCode is the JSON error code emitted when a
// retry with a different payload is detected. Exposed so callers
// (and ALP) can match against it without duplicating the literal.
const PayloadMismatchErrorCode = "idempotency_key_reused_with_different_payload"

// payloadMismatchMessage is the human-readable companion for the
// payload-mismatch error code. Kept private — the code is the
// stable contract; the message is a developer hint.
const payloadMismatchMessage = "Idempotency-Key reused with a different request payload"

// New constructs idempotency middleware. Panics on invalid configuration.
func New(cfg Config) aarv.Middleware {
	n := normalize(cfg)

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			return n.handleNative(c, next)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.handleStdlib(w, r, next)
		})
	})

	return aarv.RegisterNativeMiddleware(m, native)
}

func normalize(cfg Config) *normalized {
	if cfg.HeaderName == "" {
		cfg.HeaderName = "Idempotency-Key"
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 24 * time.Hour
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 4 << 20
	}
	// MaxRequestBodyBytes contract: negative is normalized to 0, then
	// 0 resolves to the built-in 1 MiB default when hashing is on.
	// There is no unbounded path — every HashRequestBody:true
	// configuration ends up with a positive cap.
	if cfg.MaxRequestBodyBytes < 0 {
		cfg.MaxRequestBodyBytes = 0
	}
	if cfg.HashRequestBody && cfg.MaxRequestBodyBytes == 0 {
		cfg.MaxRequestBodyBytes = 1 << 20
	}
	store := cfg.Store
	if store == nil {
		store = NewMemoryStore()
	}

	// nil → defaults; empty non-nil → no bypass.
	var safe map[string]struct{}
	if cfg.SafeMethods == nil {
		safe = map[string]struct{}{
			http.MethodGet:     {},
			http.MethodHead:    {},
			http.MethodOptions: {},
		}
	} else {
		safe = make(map[string]struct{}, len(cfg.SafeMethods))
		for _, m := range cfg.SafeMethods {
			safe[m] = struct{}{}
		}
	}

	// nil → cache 2xx/3xx; empty non-nil → cache nothing.
	var cache map[int]struct{}
	cacheNothing := false
	switch {
	case cfg.CacheStatuses == nil:
		cache = nil
	case len(cfg.CacheStatuses) == 0:
		cacheNothing = true
	default:
		cache = make(map[int]struct{}, len(cfg.CacheStatuses))
		for _, s := range cfg.CacheStatuses {
			cache[s] = struct{}{}
		}
	}

	skipPaths := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skipPaths[p] = struct{}{}
	}

	// CachedHeaders allowlist. nil → built-in default allowlist;
	// empty non-nil → persist no header. Stored in canonical form so
	// runtime lookups against http.CanonicalHeaderKey are O(1) and
	// case-insensitive (which matches how http.Header is keyed).
	var cachedHeaders map[string]struct{}
	if cfg.CachedHeaders == nil {
		cachedHeaders = defaultCachedHeaders()
	} else {
		cachedHeaders = make(map[string]struct{}, len(cfg.CachedHeaders))
		for _, h := range cfg.CachedHeaders {
			cachedHeaders[http.CanonicalHeaderKey(h)] = struct{}{}
		}
	}

	n := &normalized{
		headerName:          cfg.HeaderName,
		store:               store,
		ttl:                 cfg.TTL,
		safeMethods:         safe,
		requireKey:          cfg.RequireKey,
		hashRequestBody:     cfg.HashRequestBody,
		maxRequestBodyBytes: cfg.MaxRequestBodyBytes,
		conflictBehavior:    cfg.ConflictBehavior,
		waitTimeout:         cfg.WaitTimeout,
		cacheStatuses:       cache,
		cacheNothing:        cacheNothing,
		cacheStatusFunc:     cfg.CacheStatusFunc,
		cachedHeaders:       cachedHeaders,
		maxResponseBytes:    cfg.MaxResponseBytes,
		skipper:             cfg.Skipper,
		skipPaths:           skipPaths,
		errFn:               cfg.ErrorHandler,
	}
	if ws, ok := store.(WaitableStore); ok {
		n.waitable = ws
	}
	return n
}

// --- request flow ---

func (n *normalized) handleNative(c *aarv.Context, next aarv.HandlerFunc) error {
	if n.shouldSkipNative(c) {
		return next(c)
	}
	if _, isSafe := n.safeMethods[c.Method()]; isSafe {
		return next(c)
	}

	key := c.Header(n.headerName)
	if key == "" {
		if n.requireKey {
			return n.errNative(c, http.StatusBadRequest, "missing "+n.headerName+" header")
		}
		return next(c)
	}

	// Optional payload hash for mismatch detection. The read is bounded
	// by maxRequestBodyBytes (default 1 MiB) so a large body cannot
	// force unbounded memory use ahead of any downstream bodylimit
	// middleware.
	var (
		hash    [32]byte
		hashSet bool
	)
	if n.hashRequestBody {
		bodyBuf, tooLarge, err := readCapped(c.BodyReader(), n.maxRequestBodyBytes)
		if err != nil {
			return aarv.ErrBadRequest("failed to read request body").WithInternal(err)
		}
		if tooLarge {
			return aarv.ErrPayloadTooLarge("request body exceeds idempotency hash cap")
		}
		c.SetBody(io.NopCloser(bytes.NewReader(bodyBuf)))
		hash = sha256.Sum256(bodyBuf)
		hashSet = true
	}

	// Cache hit: replay.
	if cached, err := n.store.Get(key); err != nil {
		return aarv.ErrInternal(err).WithDetail("idempotency: store Get failed")
	} else if cached != nil {
		if hashSet && cached.PayloadHash != ([32]byte{}) && cached.PayloadHash != hash {
			return n.errPayloadMismatchNative(c)
		}
		return n.replayNative(c, cached)
	}

	// Try to acquire the lock.
	acquired, err := n.store.Lock(key)
	if err != nil {
		return aarv.ErrInternal(err).WithDetail("idempotency: store Lock failed")
	}
	if !acquired {
		return n.handleConflictNative(c, key, hashSet, hash)
	}
	defer func() {
		_ = n.store.Unlock(key)
	}()

	// First request: capture and save.
	orig := c.Response()
	cw := acquireCaptureWriter(orig, n.maxResponseBytes)
	defer releaseCaptureWriter(cw)
	c.SetResponse(cw)
	defer c.SetResponse(orig)

	handlerErr := next(c)

	if cw.Overflowed() {
		// Already streamed to the client with the explanatory header.
		// Skip Save.
		return handlerErr
	}

	// Decide whether to cache the response. shouldCacheForRequest
	// honors a per-route TTL of zero as the caching opt-out signal.
	if n.shouldCacheForRequest(c, cw.Status()) {
		snap := cw.Snapshot(n.cachedHeaders)
		if hashSet {
			snap.PayloadHash = hash
		}
		ttl := n.resolveTTL(c)
		if err := n.store.Save(key, snap, ttl); err != nil {
			// Save failed; still flush the response and return the
			// handler's result. We don't fail the user's request just
			// because the cache step failed.
			cw.FlushUnderCap()
			return handlerErr
		}
	}

	cw.FlushUnderCap()
	return handlerErr
}

func (n *normalized) handleStdlib(w http.ResponseWriter, r *http.Request, next http.Handler) {
	c, hasCtx := aarv.FromRequest(r)
	if hasCtx && n.shouldSkipNative(c) {
		next.ServeHTTP(w, r)
		return
	} else if !hasCtx {
		if _, ok := n.skipPaths[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
	}
	if _, isSafe := n.safeMethods[r.Method]; isSafe {
		next.ServeHTTP(w, r)
		return
	}

	key := r.Header.Get(n.headerName)
	if key == "" {
		if n.requireKey {
			n.errStdlib(w, c, hasCtx, http.StatusBadRequest, "missing "+n.headerName+" header")
			return
		}
		next.ServeHTTP(w, r)
		return
	}

	var (
		hash    [32]byte
		hashSet bool
	)
	if n.hashRequestBody {
		bodyBuf, tooLarge, err := readCapped(r.Body, n.maxRequestBodyBytes)
		if err != nil {
			n.errStdlib(w, c, hasCtx, http.StatusBadRequest, "failed to read request body")
			return
		}
		if tooLarge {
			n.errStdlib(w, c, hasCtx, http.StatusRequestEntityTooLarge, "request body exceeds idempotency hash cap")
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(bodyBuf))
		hash = sha256.Sum256(bodyBuf)
		hashSet = true
	}

	if cached, err := n.store.Get(key); err != nil {
		n.errStdlib(w, c, hasCtx, http.StatusInternalServerError, "idempotency: store Get failed")
		return
	} else if cached != nil {
		if hashSet && cached.PayloadHash != ([32]byte{}) && cached.PayloadHash != hash {
			n.errPayloadMismatchStdlib(w, c, hasCtx)
			return
		}
		n.replayStdlib(w, cached)
		return
	}

	acquired, err := n.store.Lock(key)
	if err != nil {
		n.errStdlib(w, c, hasCtx, http.StatusInternalServerError, "idempotency: store Lock failed")
		return
	}
	if !acquired {
		n.handleConflictStdlib(w, r, c, hasCtx, key, hashSet, hash)
		return
	}
	defer func() { _ = n.store.Unlock(key) }()

	cw := acquireCaptureWriter(w, n.maxResponseBytes)
	defer releaseCaptureWriter(cw)
	next.ServeHTTP(cw, r)

	if cw.Overflowed() {
		return
	}

	if n.shouldCacheForRequest(c, cw.Status()) {
		snap := cw.Snapshot(n.cachedHeaders)
		if hashSet {
			snap.PayloadHash = hash
		}
		ttl := n.resolveTTL(c)
		if err := n.store.Save(key, snap, ttl); err != nil {
			cw.FlushUnderCap()
			return
		}
	}
	cw.FlushUnderCap()
}

// --- conflict handling ---

func (n *normalized) handleConflictNative(c *aarv.Context, key string, hashSet bool, hash [32]byte) error {
	if n.conflictBehavior == ConflictWait && n.waitable != nil {
		ctx, cancel := n.waitContext(c.Context())
		defer cancel()
		resp, err := n.waitable.Wait(ctx, key)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return n.errNative(c, http.StatusConflict, "idempotency: concurrent request in flight")
			}
			return aarv.ErrInternal(err).WithDetail("idempotency: store Wait failed")
		}
		if resp == nil {
			// Holder finished without saving (handler error). Reject.
			return n.errNative(c, http.StatusConflict, "idempotency: concurrent request in flight")
		}
		if hashSet && resp.PayloadHash != ([32]byte{}) && resp.PayloadHash != hash {
			return n.errPayloadMismatchNative(c)
		}
		return n.replayNative(c, resp)
	}
	// ConflictReject (also: ConflictWait + non-waitable store fallback).
	return n.errNative(c, http.StatusConflict, "idempotency: concurrent request in flight")
}

func (n *normalized) handleConflictStdlib(w http.ResponseWriter, r *http.Request, c *aarv.Context, hasCtx bool, key string, hashSet bool, hash [32]byte) {
	if n.conflictBehavior == ConflictWait && n.waitable != nil {
		ctx, cancel := n.waitContext(r.Context())
		defer cancel()
		resp, err := n.waitable.Wait(ctx, key)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				n.errStdlib(w, c, hasCtx, http.StatusConflict, "idempotency: concurrent request in flight")
				return
			}
			n.errStdlib(w, c, hasCtx, http.StatusInternalServerError, "idempotency: store Wait failed")
			return
		}
		if resp == nil {
			n.errStdlib(w, c, hasCtx, http.StatusConflict, "idempotency: concurrent request in flight")
			return
		}
		if hashSet && resp.PayloadHash != ([32]byte{}) && resp.PayloadHash != hash {
			n.errPayloadMismatchStdlib(w, c, hasCtx)
			return
		}
		n.replayStdlib(w, resp)
		return
	}
	n.errStdlib(w, c, hasCtx, http.StatusConflict, "idempotency: concurrent request in flight")
}

func (n *normalized) waitContext(parent context.Context) (context.Context, context.CancelFunc) {
	if n.waitTimeout > 0 {
		return context.WithTimeout(parent, n.waitTimeout)
	}
	return context.WithCancel(parent)
}

// --- replay paths ---

func (n *normalized) replayNative(c *aarv.Context, resp *Response) error {
	n.replayHeadersTo(c.Response().Header(), resp.Headers)
	c.Response().Header().Set("Idempotency-Replayed", "true")
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	c.Response().WriteHeader(status)
	if len(resp.Body) > 0 {
		_, _ = c.Response().Write(resp.Body)
	}
	return nil
}

func (n *normalized) replayStdlib(w http.ResponseWriter, resp *Response) {
	n.replayHeadersTo(w.Header(), resp.Headers)
	w.Header().Set("Idempotency-Replayed", "true")
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
	}
}

// replayHeadersTo copies the cached header set into dst, applying
// the same allowlist + blocklist that Snapshot used at capture
// time. The double-filter is intentional: a Store populated by an
// older middleware version (or by a direct backend write) may have
// entries that were not previously filtered. Filtering on read
// ensures replays remain safe regardless of the cache provenance.
func (n *normalized) replayHeadersTo(dst, src http.Header) {
	for k, vs := range src {
		canon := http.CanonicalHeaderKey(k)
		if isHardStripped(canon) {
			continue
		}
		if n.cachedHeaders != nil {
			if _, ok := n.cachedHeaders[canon]; !ok {
				continue
			}
		}
		for _, v := range vs {
			dst.Add(canon, v)
		}
	}
}

// --- caching policy ---

func (n *normalized) shouldCache(status int) bool {
	// CacheStatusFunc takes precedence over the allowlist when set.
	// Both fall through to the default 2xx/3xx behavior when neither
	// is configured.
	if n.cacheStatusFunc != nil {
		return n.cacheStatusFunc(status)
	}
	if n.cacheNothing {
		return false
	}
	if n.cacheStatuses == nil {
		// Default: 2xx and 3xx.
		return status >= 200 && status < 400
	}
	_, ok := n.cacheStatuses[status]
	return ok
}

// defaultCachedHeaders is the built-in response-header allowlist
// applied when Config.CachedHeaders is nil. These are the headers
// that have well-defined replay semantics: content negotiation,
// caching directives, redirect targets, and entity tags.
//
// Headers that depend on per-request state — Set-Cookie (session),
// Authorization / WWW-Authenticate (per-request auth), X-Request-Id
// (per-request tracing), and the standard hop-by-hop set — are
// stripped unconditionally elsewhere, regardless of allowlist.
func defaultCachedHeaders() map[string]struct{} {
	return map[string]struct{}{
		"Content-Type":     {},
		"Content-Encoding": {},
		"Cache-Control":    {},
		"Location":         {},
		"Etag":             {},
	}
}

// hardStrippedHeaders is the unconditional blocklist applied to
// captured + replayed responses regardless of the allowlist. These
// can never be safely replayed across requests — replaying them
// either leaks per-request security state (Set-Cookie,
// Authorization), poisons the new request's tracing context
// (X-Request-Id), or depends on a hop the cached response no
// longer terminates (hop-by-hop).
var hardStrippedHeaders = map[string]struct{}{
	"Set-Cookie":          {},
	"Authorization":       {},
	"Www-Authenticate":    {},
	"X-Request-Id":        {},
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func isHardStripped(name string) bool {
	_, ok := hardStrippedHeaders[http.CanonicalHeaderKey(name)]
	return ok
}

// resolveTTL picks the effective TTL for the current request. Per-
// route TTL set via aarv.WithRouteIdempotencyTTL wins when present,
// even if it is zero (zero is the per-route opt-out signal — see
// shouldCacheForRequest).
func (n *normalized) resolveTTL(c *aarv.Context) time.Duration {
	if c == nil {
		return n.ttl
	}
	if d, ok := c.RouteIdempotencyTTL(); ok {
		return d
	}
	return n.ttl
}

// shouldCacheForRequest combines the global status policy with the
// per-route TTL opt-out. A per-route TTL of exactly zero means
// "never cache this route's responses, regardless of status".
func (n *normalized) shouldCacheForRequest(c *aarv.Context, status int) bool {
	if c != nil {
		if d, ok := c.RouteIdempotencyTTL(); ok && d == 0 {
			return false
		}
	}
	return n.shouldCache(status)
}

// --- skip / error helpers ---

func (n *normalized) shouldSkipNative(c *aarv.Context) bool {
	if _, ok := n.skipPaths[c.Path()]; ok {
		return true
	}
	if n.skipper != nil && n.skipper(c) {
		return true
	}
	return false
}

func (n *normalized) errNative(c *aarv.Context, status int, message string) error {
	if n.errFn != nil {
		return n.errFn(c, status, message)
	}
	return aarv.NewError(status, codeForStatus(status), message)
}

// errPayloadMismatchNative emits a 422 with the contract-stable
// PayloadMismatchErrorCode. Custom ErrorHandler hooks still receive
// the status + message — they cannot, however, change the error
// code observed by clients that bypass the hook (the framework
// JSON path uses the code directly).
func (n *normalized) errPayloadMismatchNative(c *aarv.Context) error {
	if n.errFn != nil {
		return n.errFn(c, http.StatusUnprocessableEntity, payloadMismatchMessage)
	}
	return aarv.NewError(http.StatusUnprocessableEntity, PayloadMismatchErrorCode, payloadMismatchMessage)
}

// errPayloadMismatchStdlib is the stdlib-path equivalent. It writes
// a JSON error body carrying the PayloadMismatchErrorCode directly
// — bypassing codeForStatus, which would emit the generic
// "unprocessable_entity" code that other 422 paths use.
func (n *normalized) errPayloadMismatchStdlib(w http.ResponseWriter, c *aarv.Context, hasCtx bool) {
	if hasCtx && n.errFn != nil {
		if err := n.errFn(c, http.StatusUnprocessableEntity, payloadMismatchMessage); err != nil {
			writePayloadMismatch(w, c, hasCtx)
		}
		return
	}
	writePayloadMismatch(w, c, hasCtx)
}

func writePayloadMismatch(w http.ResponseWriter, c *aarv.Context, hasCtx bool) {
	requestID := ""
	if hasCtx {
		requestID = c.RequestID()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(errorBody{
		Error:     PayloadMismatchErrorCode,
		Message:   payloadMismatchMessage,
		RequestID: requestID,
	})
}

func (n *normalized) errStdlib(w http.ResponseWriter, c *aarv.Context, hasCtx bool, status int, message string) {
	if hasCtx && n.errFn != nil {
		if err := n.errFn(c, status, message); err != nil {
			writeJSONError(w, status, message, c.RequestID())
		}
		return
	}
	requestID := ""
	if hasCtx {
		requestID = c.RequestID()
	}
	writeJSONError(w, status, message, requestID)
}

// readCapped reads from r up to maxBytes+1 bytes; if the read exceeds
// maxBytes, returns (nil, true, nil). When maxBytes is 0 the read is
// unbounded — caller has opted out of the cap.
func readCapped(r io.Reader, maxBytes int64) (body []byte, tooLarge bool, err error) {
	if r == nil {
		return nil, false, nil
	}
	if maxBytes <= 0 {
		body, err = io.ReadAll(r)
		return body, false, err
	}
	body, err = io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > maxBytes {
		return nil, true, nil
	}
	return body, false, nil
}

// --- JSON error writer ---

type errorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func writeJSONError(w http.ResponseWriter, status int, message, requestID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{
		Error:     codeForStatus(status),
		Message:   message,
		RequestID: requestID,
	})
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusInternalServerError:
		return "internal_error"
	default:
		return http.StatusText(status)
	}
}
