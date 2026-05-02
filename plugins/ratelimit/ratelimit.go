// Package ratelimit provides token-bucket and sliding-window rate limiting
// middleware for the aarv framework.
//
// # Cleanup
//
// New(cfg) starts no background goroutines. Stale entries are pruned
// in-line via a sharded sweep driven by an atomic counter — every 64th
// limiter check (admitted or denied) sweeps one shard, cycling through
// all shards over time. Counting denied requests as well as admitted
// ones keeps cleanup running under sustained denial pressure. Bounded
// work per request and no goroutine to leak.
//
// Callers who need a periodic janitor instead can use NewWithCleanup,
// which returns a stop function. The recommended wiring is:
//
//	mw, stop := ratelimit.NewWithCleanup(cfg)
//	app.Use(mw)
//	app.OnShutdown(func(ctx interface{ Done() <-chan struct{} }) error {
//	    return stop()
//	})
//
// # Algorithms
//
//   - TokenBucket (default): refilling bucket. Burst (default = Limit)
//     controls maximum burst size; Limit/Window controls steady refill.
//   - SlidingWindow: ring of slidingBuckets sub-counters per key,
//     smoothing fixed-window boundary effects.
//
// # Headers
//
// X-RateLimit-Limit, X-RateLimit-Remaining, and X-RateLimit-Reset (Unix
// seconds) are set on every response, admitted or denied. On 429,
// Retry-After is also set with seconds-until-reset.
package ratelimit

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
)

// Algorithm selects the rate-limiting algorithm.
type Algorithm int

const (
	// TokenBucket is the default. Refilling bucket of capacity Burst,
	// refilling at Limit/Window per second. Allows bursts up to Burst,
	// sustained throughput at Limit/Window.
	TokenBucket Algorithm = iota

	// SlidingWindow uses a ring of sub-counters covering Window. Smoother
	// than fixed-window counters at boundary crossings.
	SlidingWindow
)

// Snapshot is passed to a custom LimitHandler describing the limiter
// state at the time of denial.
type Snapshot struct {
	Limit      int
	Remaining  int
	Reset      time.Time
	RetryAfter time.Duration
}

// KeyFunc derives the rate-limit key from the request context. Defaults
// to (*aarv.Context).RealIP() when nil.
type KeyFunc func(*aarv.Context) string

// LimitHandler builds the denial response. Preempts StatusCode/Message
// when non-nil.
type LimitHandler func(c *aarv.Context, snap Snapshot) error

// Skipper bypasses the limiter when it returns true. OR-combined with
// SkipPaths.
type Skipper func(*aarv.Context) bool

// Config holds rate-limit configuration. Limit and Window are required.
type Config struct {
	Algorithm Algorithm
	Limit     int
	Window    time.Duration
	Burst     int
	KeyFunc   KeyFunc
	SkipPaths []string
	Skipper   Skipper

	StatusCode int
	Message    string
	Handler    LimitHandler

	// EntryTTL is the duration after which a key with no traffic is
	// eligible for eviction by the in-line sweeper. Defaults to 2*Window.
	EntryTTL time.Duration
}

// DefaultConfig returns a partially-configured Config; the caller must
// set Limit and Window before calling New.
func DefaultConfig() Config {
	return Config{
		Algorithm:  TokenBucket,
		StatusCode: http.StatusTooManyRequests,
		Message:    "rate limit exceeded",
	}
}

type rateLimiter struct {
	cfg     Config // copied at construction
	store   *store
	keyFunc KeyFunc

	burst     int
	statusCode int
	message    string
	skipPaths map[string]struct{}
}

// New constructs ratelimit middleware. Panics on Limit <= 0 or
// Window <= 0.
func New(cfg Config) aarv.Middleware {
	rl := newLimiter(cfg)
	return rl.middleware()
}

// NewWithCleanup constructs ratelimit middleware and starts a periodic
// janitor goroutine that sweeps stale entries from every shard. The
// returned stop function gracefully stops the goroutine; safe to call
// from multiple goroutines and safe to call more than once (sync.Once
// gates the close-and-join).
//
// The janitor sweeps once per max(Window, 1*time.Minute) by default.
func NewWithCleanup(cfg Config) (aarv.Middleware, func() error) {
	rl := newLimiter(cfg)
	period := cfg.Window
	if period < time.Minute {
		period = time.Minute
	}
	return rl.middleware(), startJanitor(rl.store, period)
}

// startJanitor spawns the periodic sweep goroutine and returns its stop
// function. Extracted from NewWithCleanup so tests can drive a tiny
// period without waiting for the 1-minute production floor.
func startJanitor(s *store, period time.Duration) func() error {
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		t := time.NewTicker(period)
		defer t.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-t.C:
				s.sweepAll()
			}
		}
	}()
	var once sync.Once
	return func() error {
		once.Do(func() {
			close(stopCh)
			<-doneCh
		})
		return nil
	}
}

func newLimiter(cfg Config) *rateLimiter {
	if cfg.Limit <= 0 {
		panic("ratelimit: Limit must be > 0")
	}
	if cfg.Window <= 0 {
		panic("ratelimit: Window must be > 0")
	}
	if cfg.Burst <= 0 {
		cfg.Burst = cfg.Limit
	}
	if cfg.EntryTTL <= 0 {
		cfg.EntryTTL = 2 * cfg.Window
	}
	if cfg.StatusCode == 0 {
		cfg.StatusCode = http.StatusTooManyRequests
	}
	if cfg.Message == "" {
		cfg.Message = "rate limit exceeded"
	}
	keyFunc := cfg.KeyFunc
	if keyFunc == nil {
		keyFunc = func(c *aarv.Context) string { return c.RealIP() }
	}

	rl := &rateLimiter{
		cfg:        cfg,
		store:      newStore(cfg.EntryTTL),
		keyFunc:    keyFunc,
		burst:      cfg.Burst,
		statusCode: cfg.StatusCode,
		message:    cfg.Message,
	}
	if len(cfg.SkipPaths) > 0 {
		rl.skipPaths = make(map[string]struct{}, len(cfg.SkipPaths))
		for _, p := range cfg.SkipPaths {
			rl.skipPaths[p] = struct{}{}
		}
	}
	return rl
}

// decide runs the configured algorithm against the entry for key and
// returns the admission decision plus snapshot.
func (rl *rateLimiter) decide(key string) (admit bool, snap Snapshot) {
	now := time.Now()
	rl.store.withEntry(key, func(e *entry) {
		var (
			ok        bool
			remaining int
			reset     time.Time
		)
		switch rl.cfg.Algorithm {
		case SlidingWindow:
			ok, remaining, reset = slidingWindowDecide(e, now, rl.cfg.Limit, rl.cfg.Window)
		default:
			ok, remaining, reset = tokenBucketDecide(e, now, rl.cfg.Limit, rl.burst, rl.cfg.Window)
		}
		admit = ok
		snap = Snapshot{
			Limit:     rl.cfg.Limit,
			Remaining: remaining,
			Reset:     reset,
		}
		if !ok {
			ra := time.Until(reset)
			if ra < time.Second {
				ra = time.Second
			}
			snap.RetryAfter = ra
		}
	})
	return
}

func (rl *rateLimiter) middleware() aarv.Middleware {
	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if rl.shouldSkipNative(c) {
				return next(c)
			}
			key := rl.keyFunc(c)
			admit, snap := rl.decide(key)
			rl.setHeaders(c.Response().Header(), snap, !admit)
			if !admit {
				if rl.cfg.Handler != nil {
					return rl.cfg.Handler(c, snap)
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

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, hasCtx := aarv.FromRequest(r)
			if rl.shouldSkipStdlib(r.URL.Path, c, hasCtx) {
				next.ServeHTTP(w, r)
				return
			}
			var key string
			if hasCtx {
				key = rl.keyFunc(c)
			} else {
				key = remoteAddrKey(r.RemoteAddr)
			}
			admit, snap := rl.decide(key)
			rl.setHeaders(w.Header(), snap, !admit)
			if !admit {
				if rl.cfg.Handler != nil && hasCtx {
					if err := rl.cfg.Handler(c, snap); err != nil {
						writeJSONError(w, rl.statusCode, rl.message, requestIDOf(c, hasCtx))
					}
					return
				}
				writeJSONError(w, rl.statusCode, rl.message, requestIDOf(c, hasCtx))
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	return aarv.RegisterNativeMiddleware(m, native)
}

func (rl *rateLimiter) shouldSkipNative(c *aarv.Context) bool {
	if _, ok := rl.skipPaths[c.Path()]; ok {
		return true
	}
	if rl.cfg.Skipper != nil && rl.cfg.Skipper(c) {
		return true
	}
	return false
}

func (rl *rateLimiter) shouldSkipStdlib(path string, c *aarv.Context, hasCtx bool) bool {
	if _, ok := rl.skipPaths[path]; ok {
		return true
	}
	if rl.cfg.Skipper != nil && hasCtx && rl.cfg.Skipper(c) {
		return true
	}
	return false
}

func (rl *rateLimiter) setHeaders(h http.Header, snap Snapshot, denied bool) {
	h.Set("X-RateLimit-Limit", strconv.Itoa(snap.Limit))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(snap.Remaining))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(snap.Reset.Unix(), 10))
	if denied {
		secs := int(snap.RetryAfter / time.Second)
		if secs < 1 {
			secs = 1
		}
		h.Set("Retry-After", strconv.Itoa(secs))
	}
}

// remoteAddrKey extracts the host portion of an http.Request.RemoteAddr
// value for use as the rate-limit key when no aarv.Context is available.
// Uses net.SplitHostPort so IPv6 addresses ([::1]:1234, [2001:db8::1]:443)
// produce the bracketless host; falls back to the original input on
// error so a non-host:port string (rare) still keys deterministically.
func remoteAddrKey(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func requestIDOf(c *aarv.Context, hasCtx bool) string {
	if !hasCtx || c == nil {
		return ""
	}
	return c.RequestID()
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
	case http.StatusTooManyRequests:
		return "too_many_requests"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		return http.StatusText(status)
	}
}
