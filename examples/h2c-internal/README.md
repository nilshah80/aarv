# h2c-internal example

Runs aarv as an HTTP/2 cleartext (h2c) server. h2c is for **internal
mesh / sidecar** deployments only — TLS must terminate upstream. Never
expose an h2c listener to the public internet.

## Run

```bash
go run . -addr 127.0.0.1:8080
```

## Talk to it

### curl (HTTP/2 prior-knowledge)

```bash
curl --http2-prior-knowledge http://127.0.0.1:8080/
```

curl's `--http2-prior-knowledge` is the cleartext-h2c equivalent of
`--http2`. The response should report `"proto": "HTTP/2.0"`.

### Go client

See `client_snippet.go` (excluded from the build via the `clientsnippet`
build tag — copy the body into your own client). Browsers cannot do
prior-knowledge h2c, so a browser request to this listener completes as
HTTP/1.1 (`"proto": "HTTP/1.1"`).

## Notes

- `WriteTimeout` is intentionally left unset in `Config`. Setting it
  terminates long-lived gRPC server-streaming or bidirectional streams
  once the timer fires; bound stream lifetime in your handler instead.
- `MaxFirstRequestBytes` defaults to 1 MiB to bound the
  `x/net/http2/h2c` first-request memory exposure on each connection.
