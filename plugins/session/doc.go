// Package session provides cookie-tracked HTTP session middleware for the
// aarv framework. It pairs naturally with the CSRF (10.3) and Basic/Bearer
// auth plugins.
//
// # Quick start
//
//	store := session.NewMemoryStore()
//	app.Use(session.New(session.Config{Store: store}))
//
//	app.Get("/me", func(c *aarv.Context) error {
//	    s := session.MustFrom(c)
//	    name, _ := s.Get("name")
//	    return c.JSON(200, map[string]any{"name": name})
//	})
//
//	app.Post("/login", func(c *aarv.Context) error {
//	    s := session.MustFrom(c)
//	    s.Set("name", "alice")
//	    if err := s.Regenerate(); err != nil {
//	        return err
//	    }
//	    return c.JSON(200, "ok")
//	})
//
// For a stateless backend (no server-side store), use NewCookie:
//
//	mw := session.NewCookie(session.CookieConfig{Key: my32ByteKey})
//	app.Use(mw)
//
// # Lifecycle
//
// On every request the middleware loads the session (or creates an empty
// one if none exists), wraps the response writer, and runs the next
// handler. The session is persisted only if the handler mutated it
// (Set/Delete/Flash/Regenerate/CSRFToken first issuance) or consumed a
// flash message; clean reads emit no Set-Cookie. Persistence happens on
// the first WriteHeader/Write so cookies land in the response headers
// even when the handler streams via c.JSON, c.Blob, c.Text, etc.
//
// # Save-error semantics
//
// Load-time failures (Store.Get error, decode error) flow through
// Config.ErrorHandler before the handler runs, so the handler can
// produce a controlled response. Commit-time failures (Store.Save /
// Store.Delete error) cannot recover the response — by the time they
// fire, headers may already be flushed. They are reported to
// Config.SaveErrorHandler if set, otherwise logged via the
// request-scoped logger and swallowed.
//
// # CookieStore value semantics
//
// CookieStore JSON-marshals session data into the encrypted cookie
// value. Session values must be JSON-compatible (no channels, funcs,
// unexported fields, or cyclic structures) and undergo a JSON round-
// trip on every request:
//
//   - int / int64 decode as float64 (JSON has no integer type)
//   - time.Time decodes as an RFC3339 string
//   - struct fields with `json:"-"` are dropped
//   - []byte encodes as a base64 string
//
// Callers needing typed round-trip fidelity should use MemoryStore (or
// a session-redis / session-sql backend) and keep CookieStore values to
// primitive strings or numeric scalars.
//
// CookieStore enforces a 3.5 KiB encoded payload guard at Save time to
// stay safely under the ~4 KiB browser cookie limit; oversize payloads
// surface as a SaveErrorHandler call.
//
// Session.ID() under CookieStore is the encrypted cookie value and
// changes on every save (the AES-GCM nonce is fresh per encryption).
// IDs are therefore stable within one request only and must not be
// used as a cross-request correlation key. For server-side backends
// the ID is a stable random string for the life of the session.
//
// # CSRF integration
//
// Session.CSRFToken() lazy-issues a per-session token and persists it on
// the next save. The CSRF plugin (10.3) reads it via session.From(c) and
// CSRFToken() to source a token bound to the session lifetime, providing
// stronger guarantees than the stateless double-submit cookie default.
package session
