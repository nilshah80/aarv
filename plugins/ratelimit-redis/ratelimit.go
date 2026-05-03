// Package ratelimitredis provides a Redis-backed rate-limit
// middleware for the aarv framework. Use it in multi-instance
// deployments where the in-process plugins/ratelimit cannot share
// state across replicas.
//
// # Algorithm
//
// Token bucket implemented as a single atomic Lua script: read
// current state, refill based on elapsed time, decrement, write
// back. The script returns (allowed, remaining, reset_unix_ms) in a
// single round trip — no read-modify-write race possible.
//
// # Header parity with plugins/ratelimit
//
// X-RateLimit-Limit / X-RateLimit-Remaining / X-RateLimit-Reset are
// set on every admitted and denied request. Retry-After (seconds,
// minimum 1) is set on 429. ALP can swap implementations without
// any client-side header change.
//
// # Failure policy
//
// Redis-error policy is fail-closed by default. Signed-API surfaces
// generally cannot tolerate silent rate-limit disablement on
// transient Redis outages — the safer default is to reject the
// request than to admit unlimited traffic. Callers who need a
// different trade-off (e.g. non-auth-tied rate limit groups) can
// set FailOpenOnRedisError: true at construction.
//
// # Recommended middleware order
//
//	requestid -> recover -> hmacauth -> ratelimitredis -> handler
//
// Place ratelimitredis after hmacauth so the limit key can be
// derived from an authenticated client identity instead of the
// unauthenticated remote IP. ALP does this via:
//
//	KeyFunc: func(c *aarv.Context) string {
//	    if cl, ok := hmacauth.From(c); ok {
//	        return "client:" + cl.ClientID
//	    }
//	    return "ip:" + c.RealIP()
//	},
package ratelimitredis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/nilshah80/aarv"
	"github.com/redis/go-redis/v9"
)

// KeyFunc derives the rate-limit key from the request context.
// Defaults to (*aarv.Context).RealIP() when nil.
type KeyFunc func(*aarv.Context) string

// LimitHandler builds the denial response. Preempts StatusCode/
// Message when non-nil. The Snapshot describes limiter state at
// the time of denial.
type LimitHandler func(c *aarv.Context, snap Snapshot) error

// Skipper bypasses the limiter when it returns true. OR-combined
// with SkipPaths.
type Skipper func(*aarv.Context) bool

// Snapshot mirrors plugins/ratelimit.Snapshot so callers can write a
// shared LimitHandler without depending on this package's types.
type Snapshot struct {
	Limit      int
	Remaining  int
	Reset      time.Time
	RetryAfter time.Duration
}

// Config holds Redis-backed rate-limit configuration.
type Config struct {
	// Client is the redis client. Required.
	Client *redis.Client

	// Limit is the maximum number of requests permitted within
	// Window. Required.
	Limit int

	// Window is the refill window. The token bucket fully refills
	// to Limit over each Window. Required.
	Window time.Duration

	// Burst is the immediate-burst capacity. When zero, defaults to
	// Limit (no extra burst headroom). Values larger than Limit
	// allow short bursts that a steady-state Limit per Window would
	// not.
	Burst int

	// KeyFunc derives the per-request rate-limit key. Defaults to
	// (*aarv.Context).RealIP().
	KeyFunc KeyFunc

	// SkipPaths is the set of request paths that bypass the
	// limiter entirely. Useful for /health, /ready, /live,
	// /metrics, /debug/*. Path matching is exact.
	SkipPaths []string

	// Skipper is a programmatic bypass. Combined with SkipPaths via
	// OR.
	Skipper Skipper

	// StatusCode is the response status returned when a request is
	// denied. Defaults to 429.
	StatusCode int

	// Message is the JSON error body's "message" when a request is
	// denied. Defaults to "rate limit exceeded".
	Message string

	// Handler builds the denial response when set, preempting the
	// default JSON shape.
	Handler LimitHandler

	// KeyPrefix is prepended to every Redis key. Defaults to
	// "aarv:ratelimit:". Configure when running multiple
	// applications against a shared Redis.
	KeyPrefix string

	// FailOpenOnRedisError, when true, admits the request on a
	// Redis transport error. Default (false) fails CLOSED — the
	// safer choice for auth-tied rate limit groups. Toggle this
	// only with explicit reasoning about your specific deployment.
	FailOpenOnRedisError bool
}

// DefaultConfig returns a partially-configured Config. The caller
// must set Client, Limit, and Window before passing it to New.
func DefaultConfig() Config {
	return Config{
		StatusCode: http.StatusTooManyRequests,
		Message:    "rate limit exceeded",
	}
}

// DefaultKeyPrefix is the default Redis key namespace.
const DefaultKeyPrefix = "aarv:ratelimit:"

// New constructs the middleware. Panics on missing required fields
// or on non-positive Limit/Window — these are misconfigurations
// that would silently disable rate limiting.
func New(cfg Config) aarv.Middleware {
	if cfg.Client == nil {
		panic("ratelimitredis: Config.Client is required")
	}
	if cfg.Limit <= 0 {
		panic("ratelimitredis: Config.Limit must be > 0")
	}
	if cfg.Window < time.Millisecond {
		// The Lua script keys off Window.Milliseconds(); a
		// sub-millisecond Window would round to zero and divide-by-
		// zero inside the script. Anything tighter than 1ms is also
		// physically uninteresting for a network rate limiter
		// (Redis RTT alone is in the 100µs-1ms range), so we reject
		// at the boundary rather than rounding silently.
		panic("ratelimitredis: Config.Window must be >= 1ms")
	}
	burst := cfg.Burst
	if burst <= 0 {
		burst = cfg.Limit
	}
	if cfg.StatusCode == 0 {
		cfg.StatusCode = http.StatusTooManyRequests
	}
	if cfg.Message == "" {
		cfg.Message = "rate limit exceeded"
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = DefaultKeyPrefix
	}

	keyFunc := cfg.KeyFunc
	if keyFunc == nil {
		keyFunc = func(c *aarv.Context) string { return c.RealIP() }
	}

	skipPaths := make(map[string]struct{}, len(cfg.SkipPaths))
	for _, p := range cfg.SkipPaths {
		skipPaths[p] = struct{}{}
	}

	rl := &limiter{
		client:        cfg.Client,
		limit:         cfg.Limit,
		burst:         burst,
		windowMs:      cfg.Window.Milliseconds(),
		keyFunc:       keyFunc,
		skipPaths:     skipPaths,
		skipper:       cfg.Skipper,
		statusCode:    cfg.StatusCode,
		message:       cfg.Message,
		handler:       cfg.Handler,
		keyPrefix:     cfg.KeyPrefix,
		failOpenError: cfg.FailOpenOnRedisError,
		script:        redis.NewScript(tokenBucketLua),
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if rl.shouldSkipNative(c) {
				return next(c)
			}
			admit, snap, err := rl.decide(c.Context(), keyFunc(c))
			if err != nil {
				if rl.failOpenError {
					// Fail-open: admit and forward, but do NOT set
					// rate-limit headers — the values would be
					// misleading (snap carries only the configured
					// limit, not real bucket state).
					return next(c)
				}
				rl.setHeaders(c.Response().Header(), snap, true)
				return aarv.NewError(http.StatusServiceUnavailable, "rate_limit_unavailable", "rate limiter backend unavailable")
			}
			rl.setHeaders(c.Response().Header(), snap, !admit)
			if !admit {
				if rl.handler != nil {
					return rl.handler(c, snap)
				}
				return c.JSON(rl.statusCode, errorBody{
					Error:     codeForStatus(rl.statusCode),
					Message:   rl.message,
					RequestID: c.RequestID(),
				})
			}
			return next(c)
		}
	})

	stdlib := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, hasCtx := aarv.FromRequest(r)
			if rl.shouldSkipStdlib(r.URL.Path, c, hasCtx) {
				next.ServeHTTP(w, r)
				return
			}
			var key string
			if hasCtx {
				key = keyFunc(c)
			} else {
				key = r.RemoteAddr
			}
			admit, snap, err := rl.decide(r.Context(), key)
			if err != nil {
				if rl.failOpenError {
					next.ServeHTTP(w, r)
					return
				}
				rl.setHeaders(w.Header(), snap, true)
				writeJSONError(w, http.StatusServiceUnavailable, "rate_limit_unavailable", "rate limiter backend unavailable", requestIDOf(c, hasCtx))
				return
			}
			rl.setHeaders(w.Header(), snap, !admit)
			if !admit {
				if rl.handler != nil && hasCtx {
					if err := rl.handler(c, snap); err != nil {
						writeJSONError(w, rl.statusCode, codeForStatus(rl.statusCode), rl.message, requestIDOf(c, hasCtx))
					}
					return
				}
				writeJSONError(w, rl.statusCode, codeForStatus(rl.statusCode), rl.message, requestIDOf(c, hasCtx))
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	return aarv.RegisterNativeMiddleware(stdlib, native)
}

type limiter struct {
	client        *redis.Client
	limit         int
	burst         int
	windowMs      int64
	keyFunc       KeyFunc
	skipPaths     map[string]struct{}
	skipper       Skipper
	statusCode    int
	message       string
	handler       LimitHandler
	keyPrefix     string
	failOpenError bool
	script        *redis.Script
}

// tokenBucketLua is the atomic decide+refill+decrement script.
//
// KEYS[1] — bucket key
// ARGV[1] — current time (ms)
// ARGV[2] — refill window (ms) — Limit tokens replenish over this
// ARGV[3] — burst capacity (max tokens the bucket can hold)
// ARGV[4] — limit (refill amount per window)
// ARGV[5] — TTL for the key (ms) — typically window*2 to keep a
//
//	cooled-down bucket addressable but reclaimable by Redis
//
// Returns:
//
//	{allowed (0|1), remaining, reset_unix_ms}
const tokenBucketLua = `
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local limit = tonumber(ARGV[4])
local ttl_ms = tonumber(ARGV[5])

local data = redis.call('HMGET', key, 'tokens', 'updated_ms')
local tokens = tonumber(data[1])
local updated_ms = tonumber(data[2])

if tokens == nil then
  tokens = burst
  updated_ms = now_ms
end

-- Refill: linear over the window. tokens += limit * (elapsed / window).
local elapsed = now_ms - updated_ms
if elapsed < 0 then elapsed = 0 end
local refill = (elapsed * limit) / window_ms
tokens = math.min(burst, tokens + refill)

local allowed = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
end

redis.call('HSET', key, 'tokens', tokens, 'updated_ms', now_ms)
redis.call('PEXPIRE', key, ttl_ms)

-- Reset: time at which the bucket is full again. Computed in ms
-- since epoch so the middleware can format X-RateLimit-Reset and
-- Retry-After consistently.
local missing = burst - tokens
local refill_ms = math.ceil((missing * window_ms) / limit)
local reset_ms = now_ms + refill_ms

return { allowed, math.floor(tokens), reset_ms }
`

func (rl *limiter) decide(ctx context.Context, key string) (bool, Snapshot, error) {
	full := rl.keyPrefix + key
	now := time.Now()
	nowMs := now.UnixMilli()
	ttlMs := rl.windowMs * 2
	if ttlMs < 1000 {
		ttlMs = 1000
	}
	res, err := rl.script.Run(ctx, rl.client, []string{full},
		nowMs, rl.windowMs, rl.burst, rl.limit, ttlMs,
	).Result()
	if err != nil {
		// Surface the redis error so the caller can apply the
		// fail-closed/fail-open policy. Snapshot carries the
		// configured limit so the X-RateLimit-Limit header is
		// still useful in the error response.
		return false, Snapshot{Limit: rl.limit}, err
	}
	arr, ok := res.([]any)
	if !ok || len(arr) != 3 {
		return false, Snapshot{Limit: rl.limit}, errors.New("ratelimitredis: unexpected script reply shape")
	}
	allowed := luaInt(arr[0]) == 1
	remaining := int(luaInt(arr[1]))
	resetMs := luaInt(arr[2])
	resetAt := time.UnixMilli(resetMs)
	retry := time.Until(resetAt)
	if retry < 0 {
		retry = 0
	}
	return allowed, Snapshot{
		Limit:      rl.limit,
		Remaining:  remaining,
		Reset:      resetAt,
		RetryAfter: retry,
	}, nil
}

// luaInt accepts the int64 / float64 / string forms Redis Lua
// return values can take depending on driver coercion. The Lua
// script returns plain numbers, but the redis client surfaces them
// as int64 in some go-redis versions and float64 in others.
func luaInt(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

func (rl *limiter) shouldSkipNative(c *aarv.Context) bool {
	if _, ok := rl.skipPaths[c.Path()]; ok {
		return true
	}
	if rl.skipper != nil && rl.skipper(c) {
		return true
	}
	return false
}

func (rl *limiter) shouldSkipStdlib(path string, c *aarv.Context, hasCtx bool) bool {
	if _, ok := rl.skipPaths[path]; ok {
		return true
	}
	if rl.skipper != nil && hasCtx && rl.skipper(c) {
		return true
	}
	return false
}

func (rl *limiter) setHeaders(h http.Header, snap Snapshot, denied bool) {
	h.Set("X-RateLimit-Limit", strconv.Itoa(snap.Limit))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(snap.Remaining))
	if !snap.Reset.IsZero() {
		h.Set("X-RateLimit-Reset", strconv.FormatInt(snap.Reset.Unix(), 10))
	}
	if denied {
		secs := int(snap.RetryAfter / time.Second)
		if secs < 1 {
			secs = 1
		}
		h.Set("Retry-After", strconv.Itoa(secs))
	}
}

func requestIDOf(c *aarv.Context, hasCtx bool) string {
	if !hasCtx || c == nil {
		return ""
	}
	return c.RequestID()
}

// --- error response ---

type errorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func writeJSONError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{
		Error:     code,
		Message:   message,
		RequestID: requestID,
	})
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		return http.StatusText(status)
	}
}
