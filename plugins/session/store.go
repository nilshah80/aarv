package session

import (
	"errors"
	"net/http"
	"time"

	"github.com/nilshah80/aarv"
)

// Stored is the wire/persistence shape of a session. Backends MUST
// deep-copy Data and Flash on both Save and Get to defeat map aliasing
// races between concurrent requests for the same session ID.
//
// CSRF is intentionally separate from Data so it cannot be read or
// overwritten by handler code via Session.Get/Set.
type Stored struct {
	Data  map[string]any `json:"data,omitempty"`
	Flash map[string]any `json:"flash,omitempty"`
	CSRF  string         `json:"csrf,omitempty"`
}

// Store is the persistence contract for server-side session backends.
// Implementations include MemoryStore (default for dev/single-node)
// and the optional plugins/session-redis and plugins/session-sql
// backends.
//
// CookieStore intentionally does NOT implement Store — it operates on
// request/response cookies rather than an opaque ID, and exposing it
// through this interface would let callers misuse it where a server-
// side store is required.
type Store interface {
	// Get returns the stored session for id, or (nil, nil) when the
	// entry is missing or has expired. Errors short-circuit to the
	// configured ErrorHandler.
	Get(id string) (*Stored, error)

	// Save persists s under id with the given TTL. A zero TTL means
	// "no expiry" — backends without infinite retention should treat
	// it as a long default.
	Save(id string, s *Stored, ttl time.Duration) error

	// Delete removes id from the backend. Calling Delete on a missing
	// key must succeed (return nil).
	Delete(id string) error
}

// ErrInvalidKey is returned by NewCookieStore when the master key is
// not exactly 32 bytes.
var ErrInvalidKey = errors.New("session: cookie store key must be 32 bytes")

// sessionBackend is the internal load/save contract that drives the
// middleware. Both server-side stores (via storeBackend) and the
// stateless cookie path (via cookieStoreBackend) implement it.
//
// The methods take the request and response writer explicitly because
// the stdlib middleware path may run with c == nil (a non-aarv mount).
// Callers must pass the wrapped writer so cookies land alongside
// in-flight handler responses.
//
// freshSession is the canonical "no usable inbound state" Session
// constructor for the backend. It must honor cfg.idLength and apply
// any backend-specific framing the load path would have used (e.g.
// the cookie-store id semantics). The middleware's load-error
// fallback routes through this method rather than handcrafting a
// session via generateID + newSession so backend-specific behavior
// stays in one place.
type sessionBackend interface {
	load(c *aarv.Context, r *http.Request, cfg *normalized) (*Session, error)
	freshSession(cfg *normalized) (*Session, error)
	save(c *aarv.Context, w http.ResponseWriter, sess *Session, cfg *normalized) error
	destroy(c *aarv.Context, w http.ResponseWriter, sess *Session, cfg *normalized) error
}
