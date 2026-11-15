# 2026-11-15 — nginx template: static frontend + `/api` proxy

| | |
|---|---|
| Author | agent |
| Scope | `deploy/nginx/homecore.conf.template`, `deploy/README.md`, `docs/changelogs/` |
| Requirements | none (deployment aid; no Controller behaviour changed) |

## Added
- `deploy/nginx/homecore.conf.template` (envsubst variables `SERVER_NAME`, `WEB_ROOT`, `BACKEND_ADDR`; also works as an
  official-image `/etc/nginx/templates/*.template`):
  - gzip for text/JSON/SVG types (`gzip_vary`, `gzip_proxied any`); fonts and images are not recompressed.
  - `/assets/` (Vite content hashes): `public, max-age=31536000, immutable`, 404 when missing; `index.html`: `no-cache`;
    other root files: 1 hour; SPA fallback via `try_files`.
  - `^~ /api/` proxy with WebSocket upgrade (`map` on `$http_upgrade`, HTTP/1.1, 1 h timeouts), upstream keep-alive,
    `X-Forwarded-*`; `/healthz` and `/readyz` proxied.
  - Controller's security headers repeated (nginx does not inherit `add_header` into locations that set their own).
- `deploy/README.md`: nginx section.

## Verification
- Ran in a `nginx:stable-alpine` container (nginx 1.30.6) with the real `frontend/dist` build and a netcat stub backend:
  `nginx -t` OK; hashed asset → gzip + single `Cache-Control: public, max-age=31536000, immutable`; `index.html` and an
  SPA route → `no-cache`; missing `/assets/nope.js` → 404; `/api/v1/*` JSON gzip-compressed; backend received `Host`,
  `X-Real-IP`, `X-Forwarded-*`; a WebSocket handshake reached the backend with `Upgrade: websocket` /
  `Connection: upgrade`, and a plain request carried no `Connection` header; `*.png` under `/assets/` and `/api/` is not
  captured by the extension location.
- **Not run**: against the real Controller, TLS block, a completed WebSocket session (the Controller has no WS endpoint),
  other nginx versions.

## Known gaps / follow-ups
- The Controller's own 30 s request timeout (`chimw.Timeout`) would need a carve-out for a future WebSocket route.
- `gzip_static`/Brotli not enabled (Vite output has no precompressed files; Brotli needs a module).
