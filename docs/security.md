# Security guide

Aarv provides security-focused middleware, but deployment security is still an
application responsibility. This guide covers the non-authentication security
plugins and how to compose them with the auth/session guides.

For authentication and authorization, see [`docs/auth.md`](auth.md). For TLS,
cert reload, autocert, mTLS, and h2c, see [`docs/tls.md`](tls.md).

## Recommended baseline

```go
app.Use(
    aarv.Recovery(),
    requestid.New(),
    secure.New(),
    cors.New(cors.Config{
        AllowOrigins:     []string{"https://app.example.com"},
        AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-CSRF-Token"},
        AllowCredentials: true,
    }),
    bodylimit.New(2<<20),
)
```

For browser apps with cookies:

```go
app.Use(
    session.New(session.Config{Store: store}),
    csrf.New(csrf.DefaultConfig()),
)
```

## Security headers

`plugins/secure` sets common browser security headers:

- `X-XSS-Protection`
- `X-Content-Type-Options`
- `X-Frame-Options`
- `Strict-Transport-Security`
- `Content-Security-Policy`
- `Referrer-Policy`
- `Permissions-Policy`
- cross-origin isolation headers

```go
app.Use(secure.New(secure.Config{
    ContentSecurityPolicy: "default-src 'self'; frame-ancestors 'none'",
    HSTSMaxAge:            365 * 24 * 60 * 60,
    HSTSIncludeSubdomains: true,
}))
```

Send HSTS only on HTTPS responses. For local development or internal tools,
use a deliberately relaxed config instead of weakening production defaults.

## CORS

`plugins/cors` handles preflight requests and response headers.

```go
app.Use(cors.New(cors.Config{
    AllowOrigins:     []string{"https://app.example.com"},
    AllowCredentials: true,
    MaxAge:           600,
}))
```

The plugin panics if `AllowCredentials` is true with wildcard origin `"*"`.
That combination is unsafe and invalid for credentialed browser requests.

Use `AllowOriginFunc` when allowed origins are tenant-specific, but keep the
function deterministic and cheap.

## CSRF

`plugins/csrf` uses the double-submit cookie pattern. Safe methods default to
`GET`, `HEAD`, `OPTIONS`, and `TRACE`; unsafe methods require a matching token
from the configured header or form field.

```go
app.Use(csrf.New(csrf.Config{
    CookieName:     "_csrf",
    HeaderName:     "X-CSRF-Token",
    CookieSecure:   true,
    CookieSameSite: http.SameSiteLaxMode,
}))
```

`CookieHTTPOnly` defaults to false for SPA ergonomics so browser JavaScript can
copy the CSRF cookie value into `X-CSRF-Token`. Server-rendered apps can set
`CookieHTTPOnly: true` and inject the token into HTML or forms via
`csrf.Token(c)`.

Pair CSRF with session cookies on browser write routes. API clients using
bearer tokens usually do not need CSRF.

## IP filtering

`plugins/ipfilter` supports allowlist and denylist modes. CIDRs are parsed at
startup; invalid entries panic.

```go
app.Use(ipfilter.New(ipfilter.Config{
    Mode:  ipfilter.ModeAllowlist,
    CIDRs: []string{"10.0.0.0/8", "192.168.0.0/16"},
}))
```

In allowlist mode, empty or unparseable client IPs fail closed. In denylist
mode, they fail open. If the app sits behind proxies, configure
`WithTrustedProxies` or provide a custom `IPFunc` so source IP resolution is
correct.

## Sanitization

`plugins/sanitize` is a submodule because it uses Unicode normalization from
`golang.org/x/text`.

```bash
go get github.com/nilshah80/aarv/plugins/sanitize@v0.9.0
```

It sanitizes JSON string values by stripping HTML, normalizing Unicode, and
running custom sanitizers.

```go
app.Use(sanitize.New(sanitize.Config{
    StripHTML:        true,
    NormalizeUnicode: true,
    MaxBodyBytes:     1 << 20,
    SkipFields:       []string{"password", "token", "signature"},
}))
```

Sanitization is not validation and is not a substitute for output encoding.
Use it only where mutating request input is acceptable.

## Encryption middleware

`plugins/encrypt` decrypts request bodies and encrypts response bodies using
AES-256-GCM. Payloads use `application/encrypted` and base64 encoding.

```go
key, err := loadStableEncryptionKey()
if err != nil {
    return err
}

mw, err := encrypt.New(key)
if err != nil {
    return err
}
app.Use(mw)
```

The middleware fully reads request bodies and buffers full responses. Do not
use it on large uploads, downloads, streaming, SSE, or unbounded responses.
Prefer TLS for transport encryption; use this plugin only when an application
payload encryption contract is required.

## Secure cookies

Aarv core includes signed and encrypted cookie helpers on `Context`. Session
cookies are handled by `plugins/session`; see [`docs/session.md`](session.md)
for cookie store and session fixation guidance.

## Production checklist

- Use TLS for all browser sessions and credentialed APIs.
- Keep CORS origins explicit when credentials are allowed.
- Use CSRF protection on cookie-authenticated browser writes.
- Put body limits before sanitization and encryption.
- Exclude secrets from sanitization and verbose logging.
- Protect admin/debug routes with auth plus network controls.
- Treat security headers as defense-in-depth, not as primary access control.
