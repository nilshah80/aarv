package session

import "github.com/nilshah80/aarv"

// sessionContextKey is the *aarv.Context store key under which the
// per-request *Session is attached. Hardcoded so From always finds it.
const sessionContextKey = "session"

// From returns the *Session attached to c by the middleware, or
// (nil, false) when the middleware has not run for this request.
func From(c *aarv.Context) (*Session, bool) {
	if c == nil {
		return nil, false
	}
	v, ok := c.Get(sessionContextKey)
	if !ok {
		return nil, false
	}
	s, ok := v.(*Session)
	return s, ok
}

// MustFrom returns the *Session attached to c, or panics if no session
// is reachable. Use in handlers that strictly require the middleware
// upstream — the panic surfaces missing wiring during development
// rather than a confusing nil-deref later.
func MustFrom(c *aarv.Context) *Session {
	s, ok := From(c)
	if !ok {
		panic("session: middleware not installed for this route")
	}
	return s
}
