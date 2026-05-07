# TLS and HTTP/2 guide

This page is the Phase 13 TLS/HTTP2 entry point. The full operational guide
lives in [`docs/tls.md`](tls.md); keep that document as the source of truth
for detailed behavior.

Use this short checklist when deciding how to expose a production service.

## Public HTTPS

Use `ListenTLS` with Aarv's effective TLS config. It floors TLS to 1.2 even
when a weaker caller config is supplied, and it enables HTTP/2 by default
through Go's `net/http` behavior.

```go
app := aarv.New(
    aarv.WithReadHeaderTimeout(5 * time.Second),
    aarv.WithReadTimeout(10 * time.Second),
    aarv.WithWriteTimeout(30 * time.Second),
    aarv.WithIdleTimeout(60 * time.Second),
)

if err := app.ListenTLS(":443", "/etc/aarv/server.crt", "/etc/aarv/server.key"); err != nil {
    log.Fatal(err)
}
```

Disable HTTP/2 only when you have a concrete compatibility or operational
reason.

```go
app := aarv.New(aarv.WithDisableHTTP2(true))
```

## Certificate reload

Use `WithCertReload` when certificates are rotated on disk by an external
process.

```go
app := aarv.New(aarv.WithCertReload(30 * time.Second))
err := app.ListenTLS(":443", "/etc/aarv/server.crt", "/etc/aarv/server.key")
```

The initial certificate load is synchronous. Invalid reloads keep serving the
last valid certificate. Do not combine `WithCertReload` with your own
`TLSConfig.GetCertificate`; Aarv returns `ErrCertReloadConflict`.

## mTLS

Use `ListenMutualTLS` when clients must present certificates. Server
cert/key reload is supported; the client CA file is loaded once at startup.
Restart the listener to rotate trust roots.

```go
err := app.ListenMutualTLS(
    ":443",
    "/etc/aarv/server.crt",
    "/etc/aarv/server.key",
    "/etc/aarv/client-ca.pem",
)
```

## Automatic certificates

Use `plugins/autocert` for Let's Encrypt issuance and renewal. Persist the
cache directory across restarts, set listener timeouts explicitly, and put
HSTS on HTTPS responses with `plugins/secure`.

## h2c

Use `plugins/h2c` only for internal cleartext HTTP/2 behind a trusted TLS
terminator, sidecar, or service mesh. It provides no confidentiality,
integrity, or client authentication by itself.

For streaming HTTP/2 workloads, avoid a fixed `WriteTimeout` on the server;
bound stream lifetime in application logic instead.

## See also

- [`docs/tls.md`](tls.md) for the complete TLS, mTLS, cert reload, autocert,
  and h2c guide.
- [`examples/cert-hot-reload/`](../examples/cert-hot-reload/) for reload
  behavior.
- [`examples/autocert-letsencrypt/`](../examples/autocert-letsencrypt/) for
  Let's Encrypt.
- [`examples/h2c-internal/`](../examples/h2c-internal/) for internal h2c.
