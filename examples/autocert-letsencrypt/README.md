# autocert-letsencrypt example

Serves an aarv App over HTTPS with certificates issued by Let's Encrypt
via `golang.org/x/crypto/acme/autocert`. Pairs the HTTPS listener with
an HTTP→HTTPS redirect listener that also satisfies the ACME HTTP-01
challenge.

## Run

The example defaults to Let's Encrypt **staging** so misconfigured runs
do not consume your prod issuance quota. Browsers will reject staging
certificates — that is expected.

```bash
go run . -domain example.com -email ops@example.com
```

You need a publicly-reachable host with `:80` and `:443` open and a DNS
A record pointing the supplied `-domain` at this host. ACME HTTP-01
needs `:80`; TLS-ALPN-01 needs `:443`.

## Production switch

Once staging works end-to-end, opt into production explicitly:

```bash
go run . -domain example.com -email ops@example.com -prod
```

Production has a **strict rate limit** — see
https://letsencrypt.org/docs/rate-limits/. Only flip `-prod` once you
have verified staging runs without errors.

## What to verify

- `curl -k https://example.com/` returns the JSON greeting (`-k` because
  staging certs are untrusted).
- `curl -I http://example.com/` returns a 308 to `https://example.com/`.
- `./.autocert-cache/` contains files named after your domain after the
  first successful handshake.
