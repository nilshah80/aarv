# cert-hot-reload example

Demonstrates `aarv.WithCertReload`: the framework polls the cert/key
files passed to `ListenTLS` and re-loads them on `(mtime, size)` change
without restarting the listener. Malformed reloads preserve the previous
certificate and log a warning.

## Requirements

- Unix shell + OpenSSL (`gencerts.sh` is bash + openssl). Windows users
  can install OpenSSL via Git Bash or WSL.

## Run

```bash
./gencerts.sh
go run . -addr 127.0.0.1:8443 -interval 2s
```

In another terminal:

```bash
# Regenerate the cert — the running server logs "cert reloaded" within
# one poll interval, without dropping connections.
./gencerts.sh

# Verify with a fresh handshake:
curl -k https://127.0.0.1:8443/
```

## What to verify

- Initial `curl -k https://127.0.0.1:8443/` succeeds.
- After `./gencerts.sh` the server logs `level=INFO msg="cert reloaded"`
  within `-interval`.
- Overwriting `certs/server.crt` with garbage logs a WARN line and
  subsequent requests still complete with the previous certificate
  (the reloader rejects the malformed bytes and keeps the in-memory
  certificate stable).
