package session

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"maps"
)

// randReader is the source of cryptographic randomness used for session
// ID and CSRF token generation. Indirected through a package variable
// so tests can swap in a failing reader.
var randReader io.Reader = rand.Reader

// ErrSessionDestroyed is returned by mutating methods after Destroy has
// been called on the same session.
var ErrSessionDestroyed = errors.New("session: destroyed")

// Session is the per-request session view exposed to handlers. It is
// not safe for concurrent use across goroutines spawned from the same
// request — sessions are owned by the goroutine running the handler.
type Session struct {
	id    string
	oldID string // populated by Regenerate; deleted from store on save

	data  map[string]any
	flash map[string]any // pending writes for the next request
	csrf  string         // lazy-issued

	// consumed records flash keys read this request via ConsumeFlash;
	// non-empty consumed forces a save so the keys are dropped from the
	// persisted Stored.Flash on the next round-trip.
	consumed map[string]struct{}

	dirty       bool
	regenerated bool
	destroyed   bool

	// new is true when no entry was found in the store. It does NOT
	// trigger a save by itself; only mutations do. See middleware.go
	// shouldSave for the predicate.
	isNew bool

	// idLen carries the configured IDLength so Regenerate produces IDs
	// of the same byte width as the original. Zero falls back to
	// defaultIDLen at generation time.
	idLen int
}

func newSession(id string, isNew bool, idLen int) *Session {
	return &Session{
		id:    id,
		data:  map[string]any{},
		flash: map[string]any{},
		isNew: isNew,
		idLen: idLen,
	}
}

func sessionFromStored(id string, st *Stored, idLen int) *Session {
	s := &Session{
		id:    id,
		data:  cloneMap(st.Data),
		flash: cloneMap(st.Flash),
		csrf:  st.CSRF,
		idLen: idLen,
	}
	if s.data == nil {
		s.data = map[string]any{}
	}
	if s.flash == nil {
		s.flash = map[string]any{}
	}
	return s
}

// ID returns the current session identifier.
//
// For server-side stores (MemoryStore, session-redis, session-sql) the
// ID is a stable random string that survives across requests for the
// life of the session. After Regenerate it reflects the new ID.
//
// For CookieStore the ID is the encrypted cookie value itself, which
// changes on every save (the AES-GCM nonce is fresh per encryption).
// CookieStore IDs are therefore meaningful only within a single
// request — do NOT use them as a cross-request correlation key, log
// identifier, or join key against external systems. Use a stable
// application-level identifier (e.g. user ID stored via Set) instead.
func (s *Session) ID() string { return s.id }

// IsNew reports whether the session was created this request because no
// entry was found in the store.
func (s *Session) IsNew() bool { return s.isNew }

// Get returns the value stored under key.
func (s *Session) Get(key string) (any, bool) {
	if s.destroyed {
		return nil, false
	}
	v, ok := s.data[key]
	return v, ok
}

// Set stores value under key and marks the session dirty.
func (s *Session) Set(key string, value any) {
	if s.destroyed {
		return
	}
	s.data[key] = value
	s.dirty = true
}

// Delete removes key and marks the session dirty.
func (s *Session) Delete(key string) {
	if s.destroyed {
		return
	}
	if _, ok := s.data[key]; ok {
		delete(s.data, key)
		s.dirty = true
	}
}

// Flash stores a one-shot value that will be available to the next
// request via ConsumeFlash. The value persists across exactly one
// request boundary.
func (s *Session) Flash(key string, value any) {
	if s.destroyed {
		return
	}
	s.flash[key] = value
	s.dirty = true
}

// ConsumeFlash returns and removes a flash value set on a previous
// request. Reading a flash forces the session to be persisted at the
// end of this request so the value does not re-appear on the next.
func (s *Session) ConsumeFlash(key string) (any, bool) {
	if s.destroyed {
		return nil, false
	}
	v, ok := s.flash[key]
	if !ok {
		return nil, false
	}
	delete(s.flash, key)
	if s.consumed == nil {
		s.consumed = map[string]struct{}{}
	}
	s.consumed[key] = struct{}{}
	return v, true
}

// Regenerate assigns a new session ID, preserving all data. The
// previous ID is queued for deletion from the store on save. Use this
// after privilege changes (login, role escalation) to defeat session
// fixation attacks.
func (s *Session) Regenerate() error {
	if s.destroyed {
		return ErrSessionDestroyed
	}
	idLen := s.idLen
	if idLen <= 0 {
		idLen = defaultIDLen
	}
	id, err := generateID(idLen)
	if err != nil {
		return err
	}
	if s.oldID == "" {
		// Only stash the original ID; multiple Regenerate calls in one
		// request collapse to a single old-ID delete.
		s.oldID = s.id
	}
	s.id = id
	s.regenerated = true
	s.dirty = true
	return nil
}

// Destroy marks the session for removal. The store entry is deleted
// and the cookie is expired on the next save. Subsequent mutations on
// the session are no-ops.
func (s *Session) Destroy() {
	s.destroyed = true
}

// CSRFToken returns the per-session CSRF token, lazy-issuing one on
// first call. The token is persisted via the session save and survives
// subsequent requests until Regenerate or Destroy.
func (s *Session) CSRFToken() (string, error) {
	if s.destroyed {
		return "", ErrSessionDestroyed
	}
	if s.csrf != "" {
		return s.csrf, nil
	}
	tok, err := generateID(csrfTokenLen)
	if err != nil {
		return "", err
	}
	s.csrf = tok
	s.dirty = true
	return tok, nil
}

// toStored snapshots the in-memory session into a *Stored ready for
// persistence. Maps are cloned defensively so the stored copy cannot
// be mutated via subsequent session methods.
func (s *Session) toStored() *Stored {
	return &Stored{
		Data:  cloneMap(s.data),
		Flash: cloneMap(s.flash),
		CSRF:  s.csrf,
	}
}

const (
	defaultIDLen = 32 // raw bytes; base64url ~ 43 chars
	csrfTokenLen = 32
)

func generateID(rawLen int) (string, error) {
	buf := make([]byte, rawLen)
	if _, err := io.ReadFull(randReader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return maps.Clone(m)
}
