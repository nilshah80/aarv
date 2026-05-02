# Vendored UI assets

This module embeds two third-party UI bundles via `go:embed`. The bundles
checked in here are the real upstream dist files at the versions pinned
below; they ship as-is in tagged releases.

## Swagger UI

| Field           | Value                                                  |
|-----------------|--------------------------------------------------------|
| Upstream        | https://github.com/swagger-api/swagger-ui              |
| Pinned version  | v5.17.14                                               |
| License         | Apache License 2.0                                     |
| License path    | `assets/swagger-ui/LICENSE`                            |
| Files embedded  | `swagger-ui.css`, `swagger-ui-bundle.js`, `swagger-initializer.js` |

### Update procedure

1. Download the desired Swagger UI release tarball from
   https://github.com/swagger-api/swagger-ui/releases.
2. Extract the `dist/` directory.
3. Copy `swagger-ui.css` and `swagger-ui-bundle.js` into
   `assets/swagger-ui/` (overwrite the placeholders).
4. **Do NOT replace `swagger-initializer.js`.** Our copy is intentionally
   custom: it reads the spec URL from a `data-spec-url` attribute on its
   own `<script>` tag so the page handler can pass any URL without
   editing JS at runtime. Keeping it custom also keeps the CSP simple
   (`script-src 'self'`) — no per-script-hash bookkeeping when the dist
   is updated.
5. Copy the upstream `LICENSE` file from the repository root into
   `assets/swagger-ui/LICENSE` (overwrite our placeholder header).
6. Update the "Pinned version" cell above.
7. `cd plugins/openapi-ui && go test -race ./...` — the asset-existence
   tests will fail loudly if any required file is missing.

## ReDoc

| Field           | Value                                                  |
|-----------------|--------------------------------------------------------|
| Upstream        | https://github.com/Redocly/redoc                       |
| Pinned version  | v2.1.5                                                 |
| License         | MIT License                                            |
| License path    | `assets/redoc/LICENSE`                                 |
| Files embedded  | `redoc.standalone.js`                                  |

### Update procedure

1. Download `redoc.standalone.js` from the desired ReDoc release at
   https://github.com/Redocly/redoc/releases (or via CDN).
2. Place it at `assets/redoc/redoc.standalone.js` (overwrite the placeholder).
3. Copy the upstream `LICENSE` file into `assets/redoc/LICENSE`.
4. Update the "Pinned version" cell above.
5. `cd plugins/openapi-ui && go test -race ./...`.

## Why are LICENSE files committed?

Both upstream projects redistribute their code under licenses that
require the license text to travel with any redistribution. Vendoring
the dist directly inside our module counts as redistribution, so the
LICENSE files MUST be present alongside the binary content. The
asset-existence tests fail if any LICENSE is missing or empty, to catch
accidental removal during an asset update.

## Content-Security-Policy

Both viewer pages can run with `script-src 'self'` because:

- The Swagger UI initializer is served as an external script
  (`swagger-initializer.js`), not inlined into the HTML.
- The ReDoc bundle is served as an external script
  (`redoc.standalone.js`).

If your CSP requires `script-src` hash allowlisting instead, generate
the hashes from the dist bytes and include them in your CSP header
(`script-src 'sha256-...'`). The hashes change every time you update
the dist, so prefer `'self'` if you can.
