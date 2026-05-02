# TLS guide

Aarv's TLS surface aims for "hardened by default" without claiming
regulatory compliance — that is your deployment's job, not the
framework's. This document covers what aarv guarantees, what it
deliberately does not, and the operational caveats around hot reload,
autocert, and h2c.

## Defaults

`App.TLSConfig()` (and `MutualTLSConfig()`) return a `*tls.Config` with:

- `MinVersion >= tls.VersionTLS12`. If the caller-supplied
  `WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS13})` chooses a
  stronger floor, that wins; weaker choices are floored back to 1.2.
- `NextProtos = ["http/1.1"]` exactly when `WithDisableHTTP2(true)` is
  set. The slice is **forced** to that exact value rather than filtered
  for "h2" — a nil or empty `NextProtos` lets `net/http` auto-configure
  HTTP/2 implicitly, so filtering alone is insufficient.

Both methods return a `tls.Config.Clone()`. Top-level fields are
independent; pointer fields (`ClientCAs`, `Certificates` slice
elements) follow stdlib clone semantics — treat nested objects as
shared.

## `WithCertReload`

Re-reads cert/key files on a poll interval (default 30s, minimum 1s)
and serves the latest loaded certificate via
`tls.Config.GetCertificate`. Polling compares `(ModTime, Size)` for
both files; either changing on either file triggers a reload.

```go
app := aarv.New(
    aarv.WithBanner(true),
    aarv.WithCertReload(30 * time.Second),
)
app.ListenTLS(":443", "/etc/aarv/server.crt", "/etc/aarv/server.key")
```

### Operational guarantees

- **Initial load is synchronous and blocking.** A bad cert/key path
  returns an error from `ListenTLS` before any traffic is served and
  before `OnStartup` hooks fire.
- **Malformed reload preserves the previous cert.** If the watched files
  change to invalid PEM (truncated write, partial overwrite), the
  reloader logs a `WARN` and the in-memory certificate stays valid.
- **One-shot lifecycle.** `CertReloader` cannot be restarted after
  `Stop` (or after the context passed to `Start` is canceled). Construct
  a new reloader if you need to reload again.
- **mTLS reloads server cert/key only.** The `clientCAFile` argument to
  `ListenMutualTLS` is loaded once at startup; trust roots do not
  hot-reload. Restart the listener to rotate the client CA.

### Conflict policy

Setting both `WithCertReload` and a caller-supplied
`TLSConfig.GetCertificate` returns `aarv.ErrCertReloadConflict` from
`ListenTLS` / `ListenMutualTLS` before serving. Pick one mechanism.

### Plain `Listen` warning

Setting `WithCertReload` on a plain `Listen` (HTTP) call logs one
`WARN` and otherwise no-ops. The reload only applies to the TLS
listeners.

## HSTS

HSTS belongs on **HTTPS responses**, not on the HTTP→HTTPS redirect
handler — `Strict-Transport-Security` over plain HTTP is
[explicitly ignored by browsers](https://datatracker.ietf.org/doc/html/rfc6797#section-8.1).
Use `plugins/secure` to set the header on your HTTPS responses:

```go
import "github.com/nilshah80/aarv/plugins/secure"

app.Use(secure.New(secure.Config{
    HSTSMaxAge:            365 * 24 * time.Hour,
    HSTSIncludeSubdomains: true,
    HSTSPreload:           true, // only after submitting to hstspreload.org
}))
```

`plugins/autocert.RedirectConfig` intentionally has no HSTS field.

## autocert

[`plugins/autocert`](../plugins/autocert/) wraps `golang.org/x/crypto/acme/autocert` and integrates it into the aarv lifecycle.
Three operational notes worth pinning down:

### Cache directory

- Created with mode `0700`. Mode is best-effort across platforms;
  POSIX semantics on `umask` apply.
- **Persistence matters.** In ephemeral container environments, mount
  a persistent volume at `Config.CacheDir`, otherwise every restart
  re-issues — and Let's Encrypt will rate-limit you.
- Each domain gets a file named after the domain. Backup the cache dir
  alongside your application data.

### Slowloris timeouts

Both the autocert HTTPS listener (via `Config.ReadTimeout` etc.) and
the redirect listener (via `RedirectConfig.ReadHeaderTimeout` etc.)
have **conservative defaults** for the redirect listener (5s / 10s /
60s) and **no defaults** for the HTTPS listener — set them explicitly
on `autocert.Config` to match the rest of your App.

Zero values on the autocert HTTPS listener mean **NO timeout**, which
exposes you to slowloris. The plugin documents this on each field.

### OCSP stapling

Aarv does **not** claim OCSP stapling. autocert handles cert issuance
and renewal, but OCSP behavior depends on certificate data and runtime
behavior of `tls.Config.GetCertificate`. Verify per-deployment via
e.g. `openssl s_client -status -connect host:443` if you require it.

## h2c (HTTP/2 cleartext)

[`plugins/h2c`](../plugins/h2c/) serves HTTP/2 over plain TCP for
service-mesh / sidecar deployments where TLS terminates upstream.

**Threat model: cleartext, internal mesh ONLY.** No confidentiality,
no integrity, no authentication. Never expose an h2c listener to the
public internet — run it only behind a trusted TLS terminator on a
private network.

### Memory bound on the first request

The upstream `x/net/http2/h2c` library reads the entire FIRST request
on each connection into memory before invoking the handler. The plugin
wraps the result in `http.MaxBytesHandler` bounded by
`Config.MaxFirstRequestBytes` (default 1 MiB). Set to a negative value
to opt out — only when running behind a trusted reverse proxy.

### `WriteTimeout` and streaming

`WriteTimeout` terminates HTTP/2 connections after the configured
duration regardless of stream activity. For long-lived gRPC
server-streaming or bidirectional streams, **leave `WriteTimeout`
zero** and bound stream lifetime via your application logic instead.

## Lifecycle and shutdown

`App.ListenServer` (and the `Listen` / `ListenTLS` /
`ListenMutualTLS` shortcuts) implements one signal-aware lifecycle:

```
setServer → OnStartup → ensureReady → banner → serve
        ↓ signal | serve return
        OnShutdown registry hooks → legacy shutdown hooks
        srv.Shutdown(ctx with ShutdownTimeout)
        cleanup() (transport-coupled, e.g. CertReloader.Stop)
```

`OnShutdown` hooks fire **before** `srv.Shutdown` so they can act
while the listener is still open (drain dependencies, emit
"shutting down" notices). Transport-coupled cleanup runs **after**
`srv.Shutdown` returns so it cannot race in-flight TLS handshakes.

## See also

- [`examples/autocert-letsencrypt/`](../examples/autocert-letsencrypt/) — end-to-end Let's Encrypt
- [`examples/cert-hot-reload/`](../examples/cert-hot-reload/) — `WithCertReload` walkthrough
- [`examples/h2c-internal/`](../examples/h2c-internal/) — h2c server + Go client snippet
