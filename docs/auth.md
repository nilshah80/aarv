# Auth guide

Aarv ships several auth plugins. Pick the smallest mechanism that
matches how callers prove identity, then put RBAC after authentication
when routes need role checks.

## Choosing a plugin

| Need | Use | Notes |
|---|---|---|
| Signed self-contained tokens | `plugins/jwt` | Validates alg allow-list, signature, exp/nbf/iat, issuer, audience, and custom claims. |
| Opaque bearer/reference tokens | `plugins/bearer` | Validator does DB/cache/OAuth introspection lookup and returns identity. |
| Service-to-service static key | `plugins/apikey` | Prefer header transport; query lookup is opt-in. |
| Browser/basic tooling prompt | `plugins/basicauth` | Use only over TLS. Good for internal/admin surfaces, not user login UX. |
| Signed request body + replay protection | `plugins/hmacauth` | Good for webhooks and trusted service calls. Use Redis nonce store for multi-instance deployments. |
| Cookie-tracked browser state | `plugins/session` | Session loading, flash, CSRF token storage. Pair with login handlers and RBAC as needed. |
| Role checks | `plugins/rbac` | Authorization only; must run after an auth plugin. |

## Recommended order

For token APIs:

```go
app.Use(
    aarv.Recovery(),
    requestid.New(),
    logger.New(),
    bodylimit.New(2<<20),
    jwt.New(jwtCfg),        // or bearer/apikey/basicauth/hmacauth
    authz.RequireRoles("user"),
)
```

For browser/session apps:

```go
store := session.NewMemoryStore()

app.Use(
    aarv.Recovery(),
    requestid.New(),
    session.New(session.Config{Store: store}),
)
```

Protect only the routes that need a logged-in user.

## JWT

Use JWTs when the token is self-contained and signed by your service or
identity provider.

```go
import "github.com/nilshah80/aarv/plugins/jwt"

secret := []byte("32-byte-minimum-secret-material!!")

app.Use(jwt.New(jwt.Config{
    HMACSecret: secret,
    Issuer:     "https://auth.example.com",
    Audience:   "users-api",
}))
```

Handlers can read claims as a map:

```go
claims, ok := jwt.From(c)
```

Or decode into a typed struct:

```go
type Claims struct {
    Subject string   `json:"sub"`
    Roles   []string `json:"roles"`
}

claims, ok := jwt.GetClaims[Claims](c)
```

Security notes:

- `alg=none` is rejected.
- Algorithm allow-lists are enforced before key resolution.
- HS* keys must be at least the hash output length.
- `KeyFunc` receives the JOSE header only; do not select keys from
  unverified claims unless you do that parsing deliberately.

## Bearer tokens

Use `bearer` for opaque tokens whose meaning lives in a database, cache,
or external identity provider.

```go
import "github.com/nilshah80/aarv/plugins/bearer"

app.Use(bearer.New(bearer.Config{
    Realm: "users-api",
    Validator: func(token string) (any, error) {
        user, ok := lookupToken(token)
        if !ok {
            return nil, aarv.ErrUnauthorized("invalid token")
        }
        return user, nil
    },
}))
```

`Config.Query` exists for deployments that truly require URL-borne
tokens, but it is disabled by default because URLs leak through logs,
browser history, and referers. When the auth header is present, it is
exclusive: malformed header plus valid query token still fails.

## API keys

Use API keys for simple service clients.

```go
app.Use(apikey.New(apikey.Config{
    Validator: apikey.StaticKeys(map[string]any{
        "key_prod_abc123": "billing-worker",
    }),
}))
```

The static helper hashes keys before in-memory lookup to avoid
length-based comparison leaks. For large deployments, keep key digests
in your credential store and write a validator around that store.

## Basic auth

Use Basic auth only over TLS.

```go
app.Use(basicauth.New(basicauth.Config{
    Realm:     "internal",
    Charset:   "UTF-8",
    Validator: basicauth.StaticCreds(map[string]string{"admin": "secret"}),
}))
```

For real user passwords, prefer bcrypt/argon2 in your validator rather
than storing plaintext or SHA-256 password digests.

## HMAC signed requests

Use `hmacauth` for webhooks or service-to-service calls where the whole
request must be signed and replay-protected.

Recommended order with idempotency:

```go
app.Use(
    requestid.New(),
    aarv.Recovery(),
    hmacauth.New(hmacCfg),
    idempotency.New(idempotencyCfg),
)
```

For multi-instance deployments, use
`plugins/hmacauth-redis` as the nonce store so replay protection is
shared across instances.

## RBAC

RBAC reads roles from whatever authentication plugin ran before it.

```go
authz := rbac.New(rbac.Config{
    RoleExtractor: func(c *aarv.Context) []string {
        claims, ok := jwt.From(c)
        if !ok {
            return nil
        }
        raw, _ := claims["roles"].([]any)
        roles := make([]string, 0, len(raw))
        for _, v := range raw {
            if s, ok := v.(string); ok {
                roles = append(roles, s)
            }
        }
        return roles
    },
})

app.Get("/admin", adminHandler,
    aarv.WithRouteMiddleware(authz.RequireRoles("admin")))
```

RBAC returns 403 on mismatch and does not include missing role names in
the response. That avoids leaking policy details.

## Problem Details for auth errors

Install `plugins/problem` as the global error handler when clients need
RFC 7807 responses:

```go
app := aarv.New(aarv.WithErrorHandler(problem.Handler(problem.Config{
    Type: "https://api.example.com/problems",
    Instance: func(c *aarv.Context) string { return c.Path() },
})))
```

Generic internal errors are masked. Validation errors are emitted as
422 with an `errors` extension.

