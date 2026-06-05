# Resilience guide

Aarv resilience plugins protect services from slow clients, oversized bodies,
concurrency spikes, rate abuse, duplicate retries, and expensive replay
patterns. Use them deliberately and in the right order.

## Recommended order

```go
app.Use(
    aarv.Recovery(),
    requestid.New(),
    bodylimit.New(2<<20),
    timeout.Context(5*time.Second),
    throttle.New(throttle.Config{MaxConcurrent: 256, QueueSize: 512, QueueTimeout: 100 * time.Millisecond}),
    ratelimit.New(ratelimit.Config{Limit: 100, Window: time.Minute}),
)
```

For signed write APIs with idempotency:

```go
app.Use(
    requestid.New(),
    aarv.Recovery(),
    bodylimit.New(2<<20),
    hmacauth.New(hmacCfg),
    idempotency.New(idempotencyCfg),
)
```

Authenticate before idempotency so unauthenticated clients cannot occupy the
idempotency key space.

## Body limits

Use `plugins/bodylimit` globally and override individual routes when needed.

```go
app.Use(bodylimit.New(2 << 20))

app.Post("/upload", uploadHandler,
    aarv.WithRouteMaxBodySize(25<<20),
)
```

Body limits should run before middleware or handlers that read the request
body, including binders, HMAC signing, encryption, sanitization, and uploads.

## Timeouts

`plugins/timeout` has two forms:

- `timeout.Context(d)` adds a request context deadline. It is lightweight and
  works best when handlers and downstream calls honor `c.Context()`.
- `timeout.New(d)` enforces a deadline by running the handler in another
  goroutine and blocking late writes. It is stricter and heavier.

Prefer context deadlines for normal APIs:

```go
app.Use(timeout.Context(3 * time.Second))
```

Use the enforced form only when handlers cannot reliably respect context
deadlines.

## Throttle

`plugins/throttle` limits in-flight requests. `MaxConcurrent` is required.

```go
app.Use(throttle.New(throttle.Config{
    MaxConcurrent: 128,
    QueueSize:     256,
    QueueTimeout:  100 * time.Millisecond,
    SkipPaths:     []string{"/health", "/ready", "/live"},
}))
```

If `QueueSize` is zero, excess requests fail fast. If a queue is configured,
requests wait briefly for a slot. Slots are released on handler success, error,
and panic.

Use throttle to protect CPU, memory, thread pools, and downstream dependencies.

## Rate limiting

`plugins/ratelimit` supports token-bucket and sliding-window algorithms.

```go
app.Use(ratelimit.New(ratelimit.Config{
    Algorithm: ratelimit.TokenBucket,
    Limit:     100,
    Window:    time.Minute,
    Burst:     100,
    KeyFunc: func(c *aarv.Context) string {
        return c.RealIP()
    },
    SkipPaths: []string{"/health", "/ready", "/live"},
}))
```

The middleware sets `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and
`X-RateLimit-Reset` on responses. Denials also include `Retry-After`.

Use `NewWithCleanup` only if you want a periodic janitor goroutine. Register
the returned stop function with shutdown hooks.

```go
mw, stop := ratelimit.NewWithCleanup(cfg)
app.Use(mw)
app.OnShutdown(func(ctx interface{ Done() <-chan struct{} }) error {
    return stop()
})
```

Use `plugins/ratelimit-redis` for distributed limits across instances.

```bash
go get github.com/nilshah80/aarv/plugins/ratelimit-redis@v0.9.0
```

Prefer authenticated client IDs as keys when available, with IP fallback.

## Idempotency

`plugins/idempotency` caches responses for requests with an
`Idempotency-Key` header. On retry, it replays the cached status, body, and
allowed headers with `Idempotency-Replayed: true`.

```go
app.Use(idempotency.New(idempotency.Config{
    TTL:             24 * time.Hour,
    RequireKey:      true,
    HashRequestBody: true,
    CacheStatuses:   []int{200, 201, 202, 204, 409},
}))
```

Important contracts:

- `SafeMethods == nil` bypasses `GET`, `HEAD`, and `OPTIONS`.
- An empty non-nil `SafeMethods` slice means every method participates.
- `CacheStatuses == nil` caches 2xx and 3xx.
- An empty non-nil `CacheStatuses` slice caches nothing.
- `HashRequestBody` rejects same-key retries with different bodies using
  `idempotency.PayloadMismatchErrorCode`.

Use per-route TTLs when cache duration differs by operation:

```go
app.Post("/payments", createPayment,
    aarv.WithRouteIdempotencyTTL(48*time.Hour),
)
```

Use `plugins/idempotency-redis` for multi-instance deployments.

```bash
go get github.com/nilshah80/aarv/plugins/idempotency-redis@v0.9.0
```

## Redis-backed stores

Redis-backed rate limiting, HMAC nonce storage, and idempotency storage are
submodules:

```bash
go get github.com/nilshah80/aarv/plugins/hmacauth-redis@v0.9.0
go get github.com/nilshah80/aarv/plugins/idempotency-redis@v0.9.0
go get github.com/nilshah80/aarv/plugins/ratelimit-redis@v0.9.0
```

Use key prefixes per service or environment so staging, production, and
multiple apps do not share key spaces accidentally.

## Production checklist

- Enforce body limits before body-reading middleware.
- Prefer context timeouts when handlers honor context cancellation.
- Use throttle for concurrency and rate limit for request frequency.
- Use Redis stores when multiple instances must share limits or replay state.
- Authenticate before idempotency and distributed replay protection.
- Keep health and metrics paths out of limiting middleware unless desired.
- Document client retry behavior for idempotent write endpoints.
