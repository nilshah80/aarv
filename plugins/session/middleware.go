package session

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/nilshah80/aarv"
)

// SessionMaxAge is a sentinel value for Config.MaxAge meaning "emit a
// browser-session cookie" — no Max-Age / Expires attributes, the
// browser drops the cookie when the window closes. Use a negative
// duration; any value < 0 has the same effect.
const SessionMaxAge = -1 * time.Second

// DefaultMaxAge is applied when Config.MaxAge is the zero value.
const DefaultMaxAge = 24 * time.Hour

// ErrStoreRequired is returned by New when Config.Store is nil.
var ErrStoreRequired = errors.New("session: Config.Store is required (use NewCookie for the stateless backend)")

// Config configures the session middleware. Zero-valued fields take
// the documented defaults; bool fields use `Disable*` form so the
// secure default is the zero value.
type Config struct {
	// Store is the server-side persistence backend. Required for New.
	// Ignored by NewCookie.
	Store Store

	// CookieName is the cookie name carrying the session ID. Default
	// "_session".
	CookieName string

	// MaxAge controls both the cookie's HTTP Max-Age and the server-
	// side TTL passed to Store.Save. Zero takes DefaultMaxAge (24h);
	// any negative value (or SessionMaxAge) emits a browser-session
	// cookie with no expiry attributes and uses DefaultMaxAge as the
	// store TTL.
	MaxAge time.Duration

	// IDLength is the raw byte length of generated session IDs.
	// Default 32 (43-char base64url string).
	IDLength int

	// CookiePath is the cookie's Path attribute. Default "/".
	CookiePath string

	// CookieDomain is the cookie's Domain attribute. Default "" (host-only).
	CookieDomain string

	// DisableSecure clears the Secure cookie attribute. Default false
	// (Secure is set). Disable for local-development HTTP only.
	DisableSecure bool

	// DisableHTTPOnly clears the HttpOnly cookie attribute. Default
	// false (HttpOnly is set). Session cookies should almost never
	// expose to JavaScript.
	DisableHTTPOnly bool

	// CookieSameSite controls SameSite. Default http.SameSiteLaxMode.
	CookieSameSite http.SameSite

	// Skipper bypasses the middleware when it returns true.
	Skipper func(*aarv.Context) bool

	// ErrorHandler runs for load-time errors (Store.Get failure) before
	// the handler executes. The middleware proceeds with a fresh
	// session if ErrorHandler returns nil. Default: log and proceed.
	ErrorHandler func(*aarv.Context, error) error

	// SaveErrorHandler is invoked for commit-time errors (Store.Save /
	// Store.Delete / cookie encode failure). Side-effect only — by the
	// time it fires, headers may already be flushed and the response
	// cannot be recovered. Default: log via the request-scoped logger
	// and swallow.
	SaveErrorHandler func(*aarv.Context, error)

	// Logger is used when a request-scoped logger is unavailable
	// (stdlib mount with no aarv.Context). Default slog.Default().
	Logger *slog.Logger
}

// CookieConfig configures the stateless cookie backend. The crypto
// fields and Key are mandatory; the rest mirror Config and follow the
// same defaults.
type CookieConfig struct {
	// Key is the AES-256 master key (exactly 32 bytes). Required.
	Key []byte

	CookieName       string
	MaxAge           time.Duration
	IDLength         int
	CookiePath       string
	CookieDomain     string
	DisableSecure    bool
	DisableHTTPOnly  bool
	CookieSameSite   http.SameSite
	Skipper          func(*aarv.Context) bool
	ErrorHandler     func(*aarv.Context, error) error
	SaveErrorHandler func(*aarv.Context, error)
	Logger           *slog.Logger
}

// normalized is the resolved configuration shared across both the
// store-backed and cookie-backed middleware paths.
type normalized struct {
	cookieName      string
	maxAge          time.Duration // store TTL; >0 always
	cookieMaxAgeSec int           // negative for session cookie
	idLength        int           // raw bytes for generated IDs
	cookiePath      string
	cookieDomain    string
	cookieSecure    bool
	cookieHTTPOnly  bool
	cookieSameSite  http.SameSite
	skipper         func(*aarv.Context) bool
	errFn           func(*aarv.Context, error) error
	saveErrFn       func(*aarv.Context, error)
	logger          *slog.Logger
}

// MinIDLength is the smallest accepted Config.IDLength / CookieConfig.IDLength
// value. Anything in (0, MinIDLength) panics at New / NewCookie so a
// footgun-low value (e.g. 1, which produces 2-char IDs trivially
// brute-forced) cannot ship to production. Matches the CSRF plugin's
// 16-byte token-length minimum (plugins/csrf/csrf.go panics the same
// way for TokenLength < 16). IDLength == 0 takes the default (32).
const MinIDLength = 16

// validateIDLength panics when configured IDLength is positive but
// below MinIDLength. Zero is accepted because applyDefaults will
// substitute defaultIDLen (32). Negative values are equally accepted
// for the same reason. The intent is to refuse only the in-between
// range that a caller could plausibly type but should never ship.
func validateIDLength(n int) {
	if n > 0 && n < MinIDLength {
		panic("session: IDLength must be 0 (use default) or >= MinIDLength (16)")
	}
}

// New returns middleware backed by a server-side Store. Panics if
// cfg.Store is nil or cfg.IDLength is in (0, MinIDLength); both
// surface at app boot rather than first request.
func New(cfg Config) aarv.NativeMiddleware {
	if cfg.Store == nil {
		panic(ErrStoreRequired)
	}
	validateIDLength(cfg.IDLength)
	n := normalizeConfig(cfg)
	backend := &storeBackend{store: cfg.Store}
	return buildMiddleware(backend, n)
}

// NewCookie returns middleware backed by an encrypted-cookie store.
// Panics on invalid Key (must be 32 bytes) or IDLength in
// (0, MinIDLength), so misconfiguration surfaces at app boot.
func NewCookie(cfg CookieConfig) aarv.NativeMiddleware {
	validateIDLength(cfg.IDLength)
	cs, err := NewCookieStore(cfg.Key)
	if err != nil {
		panic(err)
	}
	n := normalizeCookieConfig(cfg)
	return buildMiddleware(cs, n)
}

func normalizeConfig(cfg Config) *normalized {
	n := &normalized{
		cookieName:     cfg.CookieName,
		idLength:       cfg.IDLength,
		cookiePath:     cfg.CookiePath,
		cookieDomain:   cfg.CookieDomain,
		cookieSecure:   !cfg.DisableSecure,
		cookieHTTPOnly: !cfg.DisableHTTPOnly,
		cookieSameSite: cfg.CookieSameSite,
		skipper:        cfg.Skipper,
		errFn:          cfg.ErrorHandler,
		saveErrFn:      cfg.SaveErrorHandler,
		logger:         cfg.Logger,
	}
	applyDurationDefaults(n, cfg.MaxAge)
	applyDefaults(n)
	return n
}

func normalizeCookieConfig(cfg CookieConfig) *normalized {
	n := &normalized{
		cookieName:     cfg.CookieName,
		idLength:       cfg.IDLength,
		cookiePath:     cfg.CookiePath,
		cookieDomain:   cfg.CookieDomain,
		cookieSecure:   !cfg.DisableSecure,
		cookieHTTPOnly: !cfg.DisableHTTPOnly,
		cookieSameSite: cfg.CookieSameSite,
		skipper:        cfg.Skipper,
		errFn:          cfg.ErrorHandler,
		saveErrFn:      cfg.SaveErrorHandler,
		logger:         cfg.Logger,
	}
	applyDurationDefaults(n, cfg.MaxAge)
	applyDefaults(n)
	return n
}

func applyDurationDefaults(n *normalized, configured time.Duration) {
	switch {
	case configured == 0:
		n.maxAge = DefaultMaxAge
		n.cookieMaxAgeSec = int(DefaultMaxAge.Seconds())
	case configured < 0:
		// Browser-session cookie: no Max-Age attribute. Server-side
		// TTL still uses DefaultMaxAge so the store entry doesn't live
		// forever — closing the browser is a UX signal, not a security
		// one, and we should still bound server retention.
		n.maxAge = DefaultMaxAge
		n.cookieMaxAgeSec = -1 // sentinel for buildSessionCookie
	default:
		n.maxAge = configured
		n.cookieMaxAgeSec = int(configured.Seconds())
	}
}

// applyDefaults fills cookieName, cookiePath, idLength, cookieSameSite,
// and logger with documented defaults when the corresponding config
// field is the zero value.
func applyDefaults(n *normalized) {
	if n.cookieName == "" {
		n.cookieName = "_session"
	}
	if n.cookiePath == "" {
		n.cookiePath = "/"
	}
	if n.idLength <= 0 {
		n.idLength = defaultIDLen
	}
	if n.cookieSameSite == 0 {
		n.cookieSameSite = http.SameSiteLaxMode
	}
	if n.logger == nil {
		n.logger = slog.Default()
	}
}

// buildSessionCookie constructs the Set-Cookie value. When expire is
// true the cookie is overwritten with Max-Age=-1 so the client deletes
// it. cookieMaxAgeSec < 0 (and !expire) means "browser session
// cookie" — no Max-Age, no Expires.
func buildSessionCookie(cfg *normalized, value string, expire bool) *http.Cookie {
	c := &http.Cookie{
		Name:     cfg.cookieName,
		Value:    value,
		Path:     cfg.cookiePath,
		Domain:   cfg.cookieDomain,
		Secure:   cfg.cookieSecure,
		HttpOnly: cfg.cookieHTTPOnly,
		SameSite: cfg.cookieSameSite,
	}
	switch {
	case expire:
		c.MaxAge = -1
		c.Expires = time.Unix(1, 0)
	case cfg.cookieMaxAgeSec > 0:
		c.MaxAge = cfg.cookieMaxAgeSec
		c.Expires = time.Now().Add(time.Duration(cfg.cookieMaxAgeSec) * time.Second)
	}
	return c
}

// --- backend implementations ---

type storeBackend struct{ store Store }

func (b *storeBackend) load(c *aarv.Context, r *http.Request, cfg *normalized) (*Session, error) {
	cookie, err := r.Cookie(cfg.cookieName)
	if err != nil || cookie == nil || cookie.Value == "" {
		return b.freshSession(cfg)
	}
	st, err := b.store.Get(cookie.Value)
	if err != nil {
		return nil, err
	}
	if st == nil {
		// Cookie present but no store entry: forged, expired, or
		// rotated key. DO NOT reuse the client-supplied id — that
		// would let an attacker plant `_session=known-id` and fixate
		// the session at login time when the handler calls Save under
		// the attacker-chosen id. Mint a fresh random id instead.
		return b.freshSession(cfg)
	}
	return sessionFromStored(cookie.Value, st, cfg.idLength), nil
}

func (b *storeBackend) freshSession(cfg *normalized) (*Session, error) {
	id, err := generateID(cfg.idLength)
	if err != nil {
		return nil, err
	}
	return newSession(id, true, cfg.idLength), nil
}

func (b *storeBackend) save(c *aarv.Context, w http.ResponseWriter, sess *Session, cfg *normalized) error {
	if sess.regenerated && sess.oldID != "" {
		// Best-effort: report old-ID delete failure but continue; the
		// stale entry will TTL-expire even if delete failed.
		if err := b.store.Delete(sess.oldID); err != nil {
			reportSaveErr(c, cfg, err)
		}
	}
	if err := b.store.Save(sess.id, sess.toStored(), cfg.maxAge); err != nil {
		return err
	}
	http.SetCookie(w, buildSessionCookie(cfg, sess.id, false))
	return nil
}

// destroy attempts Store.Delete first (for both the current id and
// any regenerated-but-not-yet-saved oldID, so a Regenerate-then-
// Destroy sequence in one request leaves no usable server entry
// behind), then ALWAYS writes the expiration cookie so a logout
// cannot silently leave the browser holding a still-valid cookie
// when the backend is unhealthy. Delete errors are returned for
// SaveErrorHandler reporting; if both deletes fail the most recent
// error is returned.
func (b *storeBackend) destroy(c *aarv.Context, w http.ResponseWriter, sess *Session, cfg *normalized) error {
	var delErr error
	if sess.oldID != "" && sess.oldID != sess.id {
		delErr = b.store.Delete(sess.oldID)
	}
	if err := b.store.Delete(sess.id); err != nil {
		delErr = err
	}
	http.SetCookie(w, buildSessionCookie(cfg, "", true))
	return delErr
}

// --- session writer wrapper ---

// sessionWriter intercepts the first WriteHeader/Write to commit the
// session before any response bytes leave the server. commit is
// idempotent (sync.Once-gated) so the post-handler fallback in the
// middleware can re-call it without double-writing the cookie.
//
// Implements Unwrap() http.ResponseWriter so http.ResponseController
// (Go 1.20+) walks through to the underlying Flusher / Hijacker /
// Pusher / SetReadDeadline. For callers that type-assert directly
// without going through ResponseController, wrapWriter further composes
// sessionWriter with conditional Hijacker / Pusher variants so the
// assertion succeeds when (and only when) the underlying writer
// supports the interface — see wrapWriter for the variant matrix.
type sessionWriter struct {
	http.ResponseWriter
	commitFn func()
	once     sync.Once
}

func newSessionWriter(w http.ResponseWriter, commit func()) *sessionWriter {
	return &sessionWriter{ResponseWriter: w, commitFn: commit}
}

func (sw *sessionWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }

func (sw *sessionWriter) WriteHeader(status int) {
	sw.commit()
	sw.ResponseWriter.WriteHeader(status)
}

func (sw *sessionWriter) Write(b []byte) (int, error) {
	sw.commit()
	return sw.ResponseWriter.Write(b)
}

func (sw *sessionWriter) commit() { sw.once.Do(sw.commitFn) }

// Flush forwards to the underlying Flusher when present. SSE handlers
// rely on this to flush per-event.
func (sw *sessionWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		// Ensure cookies land before any flushed bytes reach the client.
		sw.commit()
		f.Flush()
	}
}

// --- conditional Hijacker / Pusher forwarding ---

// wrapWriter wraps w with sessionWriter and, conditionally, with
// http.Hijacker and http.Pusher methods that forward to the underlying
// writer. The returned http.ResponseWriter satisfies Hijacker / Pusher
// if and only if w does — direct type-assertions on the wrapper match
// the underlying writer's capabilities, so WebSocket upgraders
// (gorilla/websocket, nhooyr/websocket) and HTTP/2 push callers that
// don't go through http.ResponseController still work.
//
// The second return value is the inner *sessionWriter, exposed so the
// middleware can call commit() directly after next returns regardless
// of which variant was selected.
func wrapWriter(w http.ResponseWriter, commit func()) (http.ResponseWriter, *sessionWriter) {
	sw := newSessionWriter(w, commit)
	_, hj := w.(http.Hijacker)
	_, ps := w.(http.Pusher)
	switch {
	case hj && ps:
		return swHijackerPusher{sw}, sw
	case hj:
		return swHijacker{sw}, sw
	case ps:
		return swPusher{sw}, sw
	default:
		return sw, sw
	}
}

// swHijacker exposes Hijack() so WebSocket upgraders can take over the
// connection. commit() runs first so any pending Set-Cookie is in
// w.Header() before the upgrader serializes the handshake response.
type swHijacker struct{ *sessionWriter }

func (s swHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	s.commit()
	return s.ResponseWriter.(http.Hijacker).Hijack()
}

// swPusher exposes Push() for HTTP/2 server push. commit is NOT called
// here because Push initiates a separate request/response stream that
// does not flush headers on the original response.
type swPusher struct{ *sessionWriter }

func (s swPusher) Push(target string, opts *http.PushOptions) error {
	return s.ResponseWriter.(http.Pusher).Push(target, opts)
}

// swHijackerPusher composes both forwarders for writers that implement
// both interfaces (rare in practice; HTTP/2 connections do not support
// Hijack, so this branch primarily exists for custom writers).
type swHijackerPusher struct{ *sessionWriter }

func (s swHijackerPusher) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	s.commit()
	return s.ResponseWriter.(http.Hijacker).Hijack()
}

func (s swHijackerPusher) Push(target string, opts *http.PushOptions) error {
	return s.ResponseWriter.(http.Pusher).Push(target, opts)
}

// --- middleware glue ---

func buildMiddleware(backend sessionBackend, cfg *normalized) aarv.NativeMiddleware {
	native := aarv.MiddlewareFunc(func(next aarv.HandlerFunc) aarv.HandlerFunc {
		return func(c *aarv.Context) error {
			if cfg.skipper != nil && cfg.skipper(c) {
				return next(c)
			}
			sess, err := backend.load(c, c.Request(), cfg)
			if err != nil {
				if cfg.errFn != nil {
					if hErr := cfg.errFn(c, err); hErr != nil {
						return hErr
					}
				} else {
					reportLoadErr(c, cfg, err)
				}
				if sess == nil {
					fs, fErr := backend.freshSession(cfg)
					if fErr != nil {
						return fErr
					}
					sess = fs
				}
			}
			c.Set(sessionContextKey, sess)

			orig := c.Response()
			wrapped, sw := wrapWriter(orig, makeCommit(c, orig, sess, backend, cfg))
			c.SetResponse(wrapped)
			defer c.SetResponse(orig)

			handlerErr := next(c)
			sw.commit()
			return handlerErr
		}
	})

	stdlib := aarv.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, hasCtx := aarv.FromRequest(r)
			if hasCtx {
				// Re-bind c to the (possibly upstream-mutated) r before
				// the skipper, the backend load (which reads r.Cookie),
				// and the downstream handler all observe c. An upstream
				// stdlib middleware may have done URL rewriting, header
				// injection, body decompression, prefix stripping, or
				// r.WithContext(...). BindRequest swaps the entire
				// *http.Request on c (not just the context), and
				// returns the request the rest of the chain must use
				// — same pattern as plugins/rbac and plugins/bearer.
				r = c.BindRequest(r)
			}
			if hasCtx && cfg.skipper != nil && cfg.skipper(c) {
				next.ServeHTTP(w, r)
				return
			}
			sess, err := backend.load(c, r, cfg)
			if err != nil {
				if cfg.errFn != nil && hasCtx {
					if hErr := cfg.errFn(c, err); hErr != nil {
						// Caller signaled "stop" — the framework's error
						// pipeline is downstream of this middleware on the
						// stdlib path, so write a generic 500 here. Most
						// users will use the native path.
						http.Error(w, "session error", http.StatusInternalServerError)
						return
					}
				} else {
					reportLoadErr(c, cfg, err)
				}
				if sess == nil {
					fs, fErr := backend.freshSession(cfg)
					if fErr != nil {
						http.Error(w, "session error", http.StatusInternalServerError)
						return
					}
					sess = fs
				}
			}

			wrapped, sw := wrapWriter(w, makeCommit(c, w, sess, backend, cfg))
			if hasCtx {
				c.Set(sessionContextKey, sess)
				orig := c.Response()
				c.SetResponse(wrapped)
				defer c.SetResponse(orig)
			}
			next.ServeHTTP(wrapped, r)
			sw.commit()
		})
	})

	return aarv.RegisterNativeMiddleware(stdlib, native)
}

// makeCommit returns the commit closure attached to the writer wrapper.
// It captures *Session by reference so the handler's mutations are
// visible at flush time.
func makeCommit(c *aarv.Context, w http.ResponseWriter, sess *Session, backend sessionBackend, cfg *normalized) func() {
	return func() {
		switch {
		case sess.destroyed:
			if err := backend.destroy(c, w, sess, cfg); err != nil {
				reportSaveErr(c, cfg, err)
			}
		case shouldSave(sess):
			if err := backend.save(c, w, sess, cfg); err != nil {
				reportSaveErr(c, cfg, err)
			}
		}
	}
}

// shouldSave is the persistence predicate. A newly-issued session that
// the handler never wrote to is NOT saved — clean reads emit no
// Set-Cookie and incur no Store.Save.
func shouldSave(s *Session) bool {
	return s.dirty || len(s.consumed) > 0 || s.regenerated
}

// reportLoadErr handles the default ErrorHandler-absent case. When a
// request-scoped logger is reachable it carries request_id; otherwise
// the configured logger (slog.Default by default) is used.
func reportLoadErr(c *aarv.Context, cfg *normalized, err error) {
	pickLogger(c, cfg).Error("session: load failed", "err", err)
}

// reportSaveErr is the SaveErrorHandler-absent path. Always logs;
// never returns the error to the caller (see package docs for why
// commit-time errors cannot be recovered).
func reportSaveErr(c *aarv.Context, cfg *normalized, err error) {
	if cfg.saveErrFn != nil {
		cfg.saveErrFn(c, err)
		return
	}
	pickLogger(c, cfg).Error("session: save failed", "err", err)
}

func pickLogger(c *aarv.Context, cfg *normalized) *slog.Logger {
	if c != nil {
		return c.Logger()
	}
	return cfg.logger
}
