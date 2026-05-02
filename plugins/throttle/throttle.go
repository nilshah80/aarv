// Package throttle provides concurrency-limiting middleware for the aarv
// framework.
//
// A throttle bounds the number of in-flight requests passing through it.
// Requests beyond MaxConcurrent either fail fast (when QueueSize == 0) or
// queue briefly and wait for a slot (when QueueSize > 0 and QueueTimeout is
// set). Queue admission and slot admission are tracked with separate token
// channels: the queue token is released as soon as the goroutine either
// acquires a slot or its wait times out, never held for the duration of
// the handler. This bounds queue depth at exactly QueueSize regardless of
// handler latency.
//
// # Slot release on every exit path
//
// Slot release happens in a deferred function so handler errors and panics
// do not leak slots. The release fires before the panic propagates, so
// Recovery middleware composed outside the throttle observes the panic
// unchanged.
package throttle

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
)

// LimitHandler is invoked when admission is denied (queue full, queue
// timeout, or no-queue contention). When non-nil, it preempts StatusCode
// and Message — the handler is responsible for writing the response.
type LimitHandler func(*aarv.Context) error

// Skipper bypasses the throttle entirely. OR-combined with SkipPaths.
type Skipper func(*aarv.Context) bool

// Config holds throttle configuration. MaxConcurrent is required (> 0).
type Config struct {
	MaxConcurrent int
	QueueSize     int
	QueueTimeout  time.Duration
	StatusCode    int
	Message       string
	Handler       LimitHandler
	Skipper       Skipper
	SkipPaths     []string
}

// DefaultConfig returns a zero-shaped Config; the caller must set
// MaxConcurrent before passing through New.
func DefaultConfig() Config {
	return Config{
		StatusCode: http.StatusServiceUnavailable,
		Message:    "service unavailable",
	}
}

type throttle struct {
	slots        chan struct{} // capacity = MaxConcurrent
	queue        chan struct{} // capacity = QueueSize; nil when QueueSize == 0
	queueTimeout time.Duration
	statusCode   int
	message      string
	handler      LimitHandler
	skipper      Skipper
	skipPaths    map[string]struct{}
}

// New constructs throttle middleware. Panics on MaxConcurrent <= 0.
func New(cfg Config) aarv.Middleware {
	if cfg.MaxConcurrent <= 0 {
		panic("throttle: MaxConcurrent must be > 0")
	}
	if cfg.QueueSize < 0 {
		panic("throttle: QueueSize must be >= 0")
	}

	t := &throttle{
		slots:        make(chan struct{}, cfg.MaxConcurrent),
		queueTimeout: cfg.QueueTimeout,
		statusCode:   cfg.StatusCode,
		message:      cfg.Message,
		handler:      cfg.Handler,
		skipper:      cfg.Skipper,
	}
	if cfg.QueueSize > 0 {
		t.queue = make(chan struct{}, cfg.QueueSize)
	}
	if t.statusCode == 0 {
		t.statusCode = http.StatusServiceUnavailable
	}
	if t.message == "" {
		t.message = "service unavailable"
	}
	if len(cfg.SkipPaths) > 0 {
		t.skipPaths = make(map[string]struct{}, len(cfg.SkipPaths))
		for _, p := range cfg.SkipPaths {
			t.skipPaths[p] = struct{}{}
		}
	}

	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if t.shouldSkipNative(c) {
				return next(c)
			}
			ok := t.acquire()
			if !ok {
				if t.handler != nil {
					return t.handler(c)
				}
				return aarv.NewError(t.statusCode, codeForStatus(t.statusCode), t.message)
			}
			defer t.release()
			return next(c)
		}
	})

	m := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, hasCtx := aarv.FromRequest(r)
			if t.shouldSkipStdlib(r.URL.Path, c, hasCtx) {
				next.ServeHTTP(w, r)
				return
			}
			ok := t.acquire()
			if !ok {
				if t.handler != nil && hasCtx {
					if err := t.handler(c); err != nil {
						writeJSONError(w, t.statusCode, t.message, requestIDOf(c, hasCtx))
					}
					return
				}
				writeJSONError(w, t.statusCode, t.message, requestIDOf(c, hasCtx))
				return
			}
			defer t.release()
			next.ServeHTTP(w, r)
		})
	})

	return aarv.RegisterNativeMiddleware(m, native)
}

// acquire admits the calling goroutine into a slot. Fast path: when a
// slot is immediately available the queue is bypassed entirely — no
// queue token is taken, so QueueSize never bounds the burst-success
// rate. Slow path: the queue token is held only while waiting for a
// slot (or the timeout), then released. Returns true on admission.
func (t *throttle) acquire() bool {
	// Fast path: slot immediately available.
	select {
	case t.slots <- struct{}{}:
		return true
	default:
	}
	if t.queue == nil {
		// No-queue mode and no slot: fail fast.
		return false
	}

	// Slow path: claim a queue token.
	select {
	case t.queue <- struct{}{}:
	default:
		// Queue full.
		return false
	}

	// Release the queue token on every exit path.
	defer func() { <-t.queue }()

	// Wait for a slot up to QueueTimeout. A zero timeout still races
	// the slot send against an immediate timer fire — effectively
	// "give up if no slot is right there", consistent with the
	// QueueSize == 0 fast-path failure case.
	timer := time.NewTimer(t.queueTimeout)
	defer timer.Stop()
	select {
	case t.slots <- struct{}{}:
		return true
	case <-timer.C:
		return false
	}
}

// release returns a slot. Always paired with a successful acquire.
func (t *throttle) release() { <-t.slots }

func (t *throttle) shouldSkipNative(c *aarv.Context) bool {
	if _, ok := t.skipPaths[c.Path()]; ok {
		return true
	}
	if t.skipper != nil && t.skipper(c) {
		return true
	}
	return false
}

func (t *throttle) shouldSkipStdlib(path string, c *aarv.Context, hasCtx bool) bool {
	if _, ok := t.skipPaths[path]; ok {
		return true
	}
	if t.skipper != nil && hasCtx && t.skipper(c) {
		return true
	}
	return false
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
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusTooManyRequests:
		return "too_many_requests"
	default:
		return http.StatusText(status)
	}
}
