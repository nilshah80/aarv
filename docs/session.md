# Session guide

`plugins/session` provides cookie-tracked sessions for browser-oriented
apps and API flows that need server-side state, flash messages,
regeneration, logout, or session-bound CSRF tokens.

## Store choice

| Backend | Use when | Trade-offs |
|---|---|---|
| `MemoryStore` | Development, tests, single-process services | Fast and simple, but state is lost on restart and not shared across instances. |
| `CookieStore` | Stateless deployments with small, JSON-compatible session data | No server state, but payload is limited and values round-trip through JSON. |
| Future Redis/SQL store | Multi-instance browser sessions | The `Store` interface is designed for this, but those submodules are deferred. |

## MemoryStore quickstart

```go
import "github.com/nilshah80/aarv/plugins/session"

store := session.NewMemoryStore()
app.Use(session.New(session.Config{
    Store: store,
}))
```

Use a janitor when you want periodic cleanup in long-running processes:

```go
store, stop := session.NewMemoryStoreWithJanitor(time.Minute)
app.OnShutdown(func(ctx interface{ Done() <-chan struct{} }) error {
    return stop()
})
```

## CookieStore quickstart

CookieStore encrypts the entire `Stored` payload into the cookie value
using AES-256-GCM. The key must be exactly 32 bytes and should come from
a secret manager or KMS.

```go
key := []byte("0123456789abcdef0123456789abcdef")

app.Use(session.NewCookie(session.CookieConfig{
    Key: key,
}))
```

CookieStore values must be JSON-compatible:

- integers decode as `float64`
- `time.Time` decodes as an RFC3339 string
- `[]byte` encodes as base64
- funcs, channels, cycles, and unexported fields do not work

Keep CookieStore payloads small. The plugin rejects encoded payloads
above 3.5 KiB so the browser cookie stays under typical 4 KiB limits.

## Secure defaults

By default session cookies are:

- `Secure`
- `HttpOnly`
- `SameSite=Lax`
- `Path=/`
- name `_session`
- max age 24 hours

Disable `Secure` only for local HTTP development:

```go
session.New(session.Config{
    Store:         store,
    DisableSecure: true,
})
```

## Login and logout

Regenerate the session ID after privilege changes, especially login.
That defeats session fixation.

```go
app.Post("/login", aarv.BindReq(func(c *aarv.Context, req LoginReq) error {
    s := session.MustFrom(c)
    s.Set("user_id", "u_1")
    s.Set("roles", []string{"user"})
    if err := s.Regenerate(); err != nil {
        return err
    }
    return c.NoContent(http.StatusNoContent)
}))
```

Destroy on logout:

```go
app.Post("/logout", func(c *aarv.Context) error {
    session.MustFrom(c).Destroy()
    return c.NoContent(http.StatusNoContent)
})
```

Destroy writes an expiration cookie even if server-side deletion fails,
so the browser is not left holding a still-valid cookie during backend
outages.

## Reading session data

```go
app.Get("/me", func(c *aarv.Context) error {
    s := session.MustFrom(c)
    userID, ok := s.Get("user_id")
    if !ok {
        return aarv.ErrUnauthorized("login required")
    }
    return c.JSON(http.StatusOK, map[string]any{"user_id": userID})
})
```

Use `session.From(c)` when missing middleware is a normal branch. Use
`session.MustFrom(c)` when missing middleware is a wiring bug.

## Flash messages

Flash values are available on the next request and are removed after
they are consumed.

```go
session.MustFrom(c).Flash("notice", "profile updated")

msg, ok := session.MustFrom(c).ConsumeFlash("notice")
```

Consuming a flash value forces a save so it does not reappear.

## CSRF token

`Session.CSRFToken()` lazy-issues a token bound to the session lifetime:

```go
token, err := session.MustFrom(c).CSRFToken()
if err != nil {
    return err
}
```

The CSRF plugin can use this as the source for session-bound CSRF
tokens. The token is stored outside user-visible `Session.Get/Set`
data, so `_csrf` is not a reserved application key.

## Error handling

Load-time failures run before the handler and can stop the request:

```go
session.New(session.Config{
    Store: store,
    ErrorHandler: func(c *aarv.Context, err error) error {
        return aarv.ErrServiceUnavailable("session unavailable")
    },
})
```

Commit-time failures may happen after headers have been written, so they
are reported as side effects only:

```go
session.New(session.Config{
    Store: store,
    SaveErrorHandler: func(c *aarv.Context, err error) {
        c.Logger().Error("session save failed", "err", err)
    },
})
```

## Production notes

- Use TLS whenever session cookies are enabled.
- Keep the default `Secure` and `HttpOnly` attributes in production.
- Regenerate after login and role elevation.
- Do not use `Session.ID()` as a cross-request correlation key with
  CookieStore; it changes on every save.
- Prefer server-side stores for complex typed values or large session
  payloads.

