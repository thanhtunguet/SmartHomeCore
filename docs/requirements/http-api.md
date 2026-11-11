# Requirements: Controller HTTP API Foundation

| | |
|---|---|
| Brief | §3.1, §4, §6 (public API zone) |
| Code | `internal/api` (`server.go`, `middleware/`, `handlers/`, `gen/`), `api/openapi.yaml`, `web/` |
| Design | [architecture §6.1, §9, §10](../design/architecture.md), [ADR-006](../design/decisions.md#adr-006--spec-first-rest-api), [ADR-008](../design/decisions.md#adr-008--offline-swagger-ui) |
| Status | **Implemented** foundation; business endpoints planned (return `501`) |

## Functional requirements

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-API-001 | The Controller serves a JSON REST API under `/api/v1`, plus `/healthz`, `/readyz`, `/api/openapi.json`, `/api/docs`. Every error uses one envelope: `{"error":{"code":"…","message":"…"}}`. | Implemented | `TestRoutesAndPolicy` |
| REQ-API-002 | `GET /api/openapi.json` returns the embedded spec (from `api/openapi.yaml`) as JSON; it is public and needs no network access. | Implemented | `TestHealthBodyAndSpec` |
| REQ-API-003 | `GET /api/docs/` serves Swagger UI from pinned assets embedded in the binary (`swagger-ui-dist` 5.17.14, checksums in `web/swagger/VERSION`). No CDN, the online validator is disabled, no inline scripts. `/api/docs` redirects to `/api/docs/`. The page grants no permissions: API calls from it still need a bearer token. `controller.api_docs: false` disables spec and UI. | Implemented | `TestRoutesAndPolicy` (page + asset) |
| REQ-API-004 | `GET /healthz` (public) returns `{"status":"ok","time":<server clock>,"version":…}`. `GET /readyz` (public) returns `200 ready` only if the DB answers a ping within 2 s and the schema is at the latest migration; otherwise `503 not_ready` with per-check details. | Implemented | `TestRoutesAndPolicy`, `TestHealthBodyAndSpec` (503 path untested) |
| REQ-API-005 | Unmatched non-API `GET`/`HEAD` paths serve the embedded frontend (`web/static`) with SPA fallback to `index.html`. Unknown `/api/*` paths return a JSON `404`. | Implemented | `TestRoutesAndPolicy` |
| REQ-API-006 | **Spec-first**: routes, path/query binding and models are generated from `api/openapi.yaml` by `oapi-codegen` into `internal/api/gen` (committed; regeneration is deterministic). Operations without an implementation return `501`. Malformed parameters (e.g. a non-UUID id) return `400 bad_request`. | Implemented | `TestRoutesAndPolicy` (501, 400); `make check-generated` |
| REQ-API-007 | Protected operations require `Authorization: Bearer <token>`. A missing token gets `401` with `WWW-Authenticate: Bearer realm="homecore"`; a rejected token gets `401` with `error="invalid_token"`. Tokens are checked by an `auth.Authenticator`. | **Partial.** Middleware implemented; the only Authenticator is `RejectAll` until JWT lands | `TestRoutesAndPolicy` (with a test authenticator) |
| REQ-API-008 | Authorization comes from each operation's `security` in the spec: `security: []` = public; `bearerAuth: []` = any authenticated user; `bearerAuth: [admin]` = admin only (admin satisfies every role). A route without a policy entry is denied with `403` (fail closed). Unknown schemes or roles in the spec abort startup. | Implemented | `TestRoutesAndPolicy` (viewer 403 on create/shutdown, admin allowed); fail-closed path untested |
| REQ-API-009 | Every response carries `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and a same-origin `Content-Security-Policy`. | Implemented (untested) | — |
| REQ-API-010 | Every request gets a request id and one access-log line (method, path, status, bytes, duration, client IP, request id; never headers or bodies). Panics become `500 internal`. Handlers time out after 30 s. Server limits: read-header 5 s, read 30 s, write 60 s, idle 120 s, headers 64 KiB. | Implemented | Smoke-test log; timeouts untested |
| REQ-API-011 | CORS is off by default (same-origin only). With `cors_allowed_origins` set, only exact-match origins get CORS headers; preflight returns `204`. | Implemented (untested) | — |
| REQ-API-012 | Per-client-IP rate limiting: all routes 600/min (burst 100); `POST /api/v1/auth/*` additionally 10/min (burst 5). Excess gets `429 rate_limited` with `Retry-After: 60`. The client IP is the TCP peer, unless the peer is in `controller.trusted_proxies`, in which case `X-Forwarded-For` is used. | Implemented (untested) | — |
| REQ-API-013 | Request bodies are limited to 1 MiB. | Implemented (untested) | — |
| REQ-API-014 | Optional TLS on the Controller listener (`controller.tls.cert_file` + `key_file`). | Implemented (untested) | — |

## Endpoint implementation status

The full contract (brief §4 plus additions) is in `api/openapi.yaml`.

| Endpoints | Status |
|-----------|--------|
| `/healthz`, `/readyz`, `/api/openapi.json`, `/api/docs` | Implemented |
| `/api/v1/auth/*` | Planned (Phase 1b, JWT) |
| `/api/v1/devices*` (CRUD, wake, shutdown, probe, history) | Planned (Phase 2/3) |
| `/api/v1/commands*` | Planned (Phase 2) |
| `/api/v1/system/metrics`, `/api/v1/system/controller-key` | Planned (Phase 3/4) |
| `/api/v1/ddns*` | Planned (Phase 4) |

## Known gaps

- No JWT implementation: every protected endpoint currently answers `401`.
- No automated tests for rate limiting, CORS, body limit, security headers, request timeout, TLS,
  the `503` readiness path, or the fail-closed policy path.
- `audit_log` writes for privileged operations are not implemented (table exists).
