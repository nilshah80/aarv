# Aarv autocert plugin

`github.com/nilshah80/aarv/plugins/autocert` integrates `golang.org/x/crypto/acme/autocert` with Aarv's server lifecycle.

## Install

```sh
go get github.com/nilshah80/aarv/plugins/autocert
```

## TLS listener

`Listen` creates an ACME manager internally and serves HTTPS with ACME TLS-ALPN-01 support.
The snippets below assume the plugin is imported as `aarvautocert` and
`golang.org/x/crypto/acme/autocert` is imported as `autocert`.

```go
app := aarv.New()

err := aarvautocert.Listen(app, ":443", aarvautocert.Config{
	HostPolicy: autocert.HostWhitelist("example.com"),
	Email:      "ops@example.com",
	CacheDir:   "/var/lib/aarv-autocert",
})
```

Use Let's Encrypt staging while testing:

```go
DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
```

Switch to production only after DNS, ports, and persistence are correct. Production ACME endpoints are rate-limited.

## HTTP-01 challenge and redirect

HTTP-01 requires the same manager on both the HTTPS listener and the HTTP redirect listener. Build the manager explicitly and pass it to both:

```go
cfg := aarvautocert.Config{
	HostPolicy: autocert.HostWhitelist("example.com"),
	Email:      "ops@example.com",
	CacheDir:   "/var/lib/aarv-autocert",
}

mgr, err := aarvautocert.Manager(cfg)
if err != nil {
	log.Fatal(err)
}

redirectSrv := aarvautocert.RedirectServer(":80", aarvautocert.RedirectConfig{
	ACMEHandler: mgr,
})

ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
go func() {
	<-ctx.Done()
	_ = redirectSrv.Shutdown(context.Background())
}()
go func() {
	if err := redirectSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("redirect server: %v", err)
	}
}()

err = aarvautocert.ListenWithManager(app, ":443", mgr, cfg)
```

`ListenRedirect` is a simple blocking convenience for deployments that manage shutdown elsewhere.

## Cache directory

The cache directory is created with `0700` permissions on best effort basis. In containers, mount it on persistent storage. If the cache is ephemeral, the service may re-issue certificates after every restart and can hit ACME rate limits.

## Redirect host handling

When `RedirectConfig.TargetHost` is empty, the redirect uses `r.Host`. If the service is behind a proxy, normalize trusted forwarded headers before this handler. The redirect handler rejects control characters to prevent malformed `Location` headers, but does not try to validate every possible DNS or split-horizon hostname.

## TLS settings

The plugin starts from `app.TLSConfig()`, so Aarv's TLS 1.2 floor and HTTP/2 disable policy are preserved. `ConfigureTLS` may tune fields such as cipher suites or curves, but the plugin re-applies the TLS 1.2 minimum and never re-enables HTTP/2 when the app disabled it.
