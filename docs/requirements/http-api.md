# Requirements: Controller HTTP API Foundation

| | |
|---|---|
| Brief | §3.1, §4, §6 (public API zone) |
| Code | `internal/api` (`server.go`, `middleware/`, `handlers/`, `gen/`), `api/openapi.yaml`, `web/`; authentication: [authentication.md](authentication.md) |
| Design | [architecture §6.1, §9, §10](../design/architecture.md), [ADR-006](../design/decisions.md#adr-006--spec-first-rest-api), [ADR-008](../design/decisions.md#adr-008--offline-swagger-ui) |
| Last updated | 2026-11-14 (Phase 2: device, probe, history, wake and command endpoints implemented) |
| Status | **Implemented** foundation, authentication, devices and commands (wake); shutdown, system metrics and DDNS endpoints planned (return `501` with the error envelope) |

## Functional requirements

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-API-001 | The Controller serves a JSON REST API under `/api/v1`, plus `/healthz`, `/readyz`, `/api/openapi.json`, `/api/docs`. Every error uses one envelope: `{"error":{"code":"…","message":"…"}}`, including `501 not_implemented` for documented but unbuilt operations. | Implemented | `TestRoutesAndPolicy`, `TestUnimplementedUsesErrorEnvelope` |
| REQ-API-002 | `GET /api/openapi.json` returns the embedded spec (from `api/openapi.yaml`) as JSON; it is public and needs no network access. | Implemented | `TestHealthBodyAndSpec` |
| REQ-API-003 | `GET /api/docs/` serves Swagger UI from pinned assets embedded in the binary (`swagger-ui-dist` 5.17.14, checksums in `web/swagger/VERSION`). No CDN, the online validator is disabled, no inline scripts. `/api/docs` redirects to `/api/docs/`. The page grants no permissions: API calls from it still need a bearer token. `controller.api_docs: false` disables spec and UI. | Implemented | `TestRoutesAndPolicy` (page + asset) |
| REQ-API-004 | `GET /healthz` (public) returns `{"status":"ok","time":<server clock>,"version":…}`. `GET /readyz` (public) returns `200 ready` only if the DB answers a ping within 2 s and the schema is at the latest migration; otherwise `503 not_ready` with per-check details. | Implemented | `TestRoutesAndPolicy`, `TestHealthBodyAndSpec` (503 path untested) |
| REQ-API-005 | Unmatched non-API `GET`/`HEAD` paths serve the embedded frontend (`web/static`) with SPA fallback to `index.html`. Unknown `/api/*` paths return a JSON `404`. When the UI has not been built (`web/static/index.html` missing) a placeholder page is served instead; see [REQ-UI-001/002](web-ui.md). | Implemented | `TestRoutesAndPolicy`, `web/embed_test.go` |
| REQ-API-006 | **Spec-first**: routes, path/query binding and models are generated from `api/openapi.yaml` by `oapi-codegen` into `internal/api/gen` (committed; regeneration is deterministic). Operations without an implementation return `501`. Malformed parameters (e.g. a non-UUID id) return `400 bad_request`. | Implemented | `TestRoutesAndPolicy` (501, 400); `make check-generated` |
| REQ-API-007 | Protected operations require `Authorization: Bearer <token>`. A missing token gets `401` with `WWW-Authenticate: Bearer realm="homecore"`; a rejected token gets `401` with `error="invalid_token"`. Tokens are checked by an `auth.Authenticator`. | Implemented. The Authenticator is `auth.Service` (JWT, REQ-AUTH-005/006); `auth.RejectAll` remains only as the fail-closed fallback when none is given | `TestRoutesAndPolicy` (test authenticator), `TestProtectedRoutesWithRealAuthenticator` (real JWT: 501 with a token, 401 without), `TestAccessTokenExpiryAndRefresh`, `TestLoginAndMe` (`invalid_token` challenge) |
| REQ-API-008 | Authorization comes from each operation's `security` in the spec: `security: []` = public; `bearerAuth: []` = any authenticated user. **There are no roles** (ADR-011): a scope on `bearerAuth`, or an unknown scheme, aborts startup. A route without a policy entry is denied with `403` (fail closed). | Changed (roles removed 2026-11-14) | `TestRoutesAndPolicy` (any user passes), `TestPolicyFromSpec*`; fail-closed path untested |
| REQ-API-009 | Every response carries `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and a same-origin `Content-Security-Policy`. | Implemented (untested) | — |
| REQ-API-010 | Every request gets a request id and one access-log line (method, path, status, bytes, duration, client IP, request id; never headers or bodies). Panics become `500 internal`. Handlers time out after 30 s. Server limits: read-header 5 s, read 30 s, write 60 s, idle 120 s, headers 64 KiB. | Implemented | Smoke-test log; timeouts untested |
| REQ-API-011 | CORS is off by default (same-origin only). With `cors_allowed_origins` set, only exact-match origins get CORS headers; preflight returns `204`. | Implemented (untested) | — |
| REQ-API-012 | Per-client-IP rate limiting: all routes 600/min (burst 100); `POST /api/v1/auth/*` additionally 10/min (burst 5). Excess gets `429 rate_limited` with `Retry-After: 60`. The client IP is the TCP peer, unless the peer is in `controller.trusted_proxies`, in which case `X-Forwarded-For` is used. | Implemented | `TestLoginRateLimit` (the `/auth/*` limit, through the real router); the 600/min limit and `trusted_proxies` are untested |
| REQ-API-013 | Request bodies are limited to 1 MiB. | Implemented (untested) | — |
| REQ-API-014 | Optional TLS on the Controller listener (`controller.tls.cert_file` + `key_file`). | Implemented (untested) | — |
| REQ-API-015 | `GET /api/v1/system/info` (any authenticated user) returns the version, mode and effective non-secret defaults (listen, TLS on/off, token lifetimes, retention, monitor defaults, command deadlines). It never returns keys, secrets or file-system paths. | Implemented | `TestSystemInfo` |
| REQ-API-016 | Static files: `assets/*` (content-hashed) are served with `Cache-Control: public, max-age=31536000, immutable`; `index.html` and the SPA fallback with `no-cache`. | Implemented | `TestStaticCacheHeaders` |
| REQ-API-017 | Device registry extras: `device_type` and `os` (enums, defaults `desktop`/`linux`) and read-only `last_latency_ms` on `Device`; an empty string clears `description`, `hostname`, `mac_address` and `wol_broadcast_address` on `PATCH`; `DELETE /devices/{id}/agent` removes the agent binding and is idempotent. List order is fixed: devices by name (case-insensitive), commands and status history newest first, all with keyset cursors (`limit` 1..200, `400` on a bad limit or cursor). Detail: [devices-and-commands.md](devices-and-commands.md) REQ-DEV-001..011, REQ-MON-006/007, REQ-CMD-009. | Implemented | `TestDeviceCRUDHappyPath`, `TestPatchClearsOptionalTextWithEmptyString`, `TestAgentBindingThroughTheAPI`, `TestListPaginationOverHTTP`, `TestProbeAndHistory`, `TestCommandListFiltersPaginationAndErrors` (every response checked against the spec by `specChecker`); `TestRemoveDeviceAgentPolicy`, `TestDeviceMetaColumnsDefaultAndConstrain` |
| REQ-API-018 | At most **one** `POST /devices/{id}/wake` or `/shutdown` per device per second, counted over all users and both types. Excess requests get `429 rate_limited` with `Retry-After: 1`. Enforced after authentication (anonymous requests do not use the slot); state is in memory. The UI applies the same guard (REQ-UI-018). | Implemented | `TestDeviceCommandThrottle`, `TestDeviceCommandThrottleAfterAuth`, `TestDeviceThrottle*` |
| REQ-API-019 | Wake/shutdown are idempotent and fail fast (ADR-012): an active command of the same type is returned (`202`); a device already online (wake) / offline (shutdown) gets a command that completes at once; `409` only for a missing MAC (wake) or agent binding (shutdown); agent dispatch is one attempt with a 2 s timeout plus at most one immediate retry; wake and shutdown of a device are independent. | **Partial.** Wake is **Implemented** (REQ-CMD-001..006, `TestWakeLifecycleOverHTTP`, `TestWakeWithoutMACIs409`, `TestWakeSendFailureYieldsAFailedCommand`, `TestWakeTimesOutAtItsDeadline`, `commands.TestWakeIsIdempotent`); shutdown dispatch is Planned (Phase 3, `501`) | `TestWakeLifecycleOverHTTP`, `TestShutdownIsStillNotImplemented` |
| REQ-API-020 | Cloudflare lookups for the DDNS form: `POST /ddns/cloudflare/zones` and `POST /ddns/cloudflare/zones/{zone_id}/records` take exactly one of `api_token` or `config_id` in the body (never in a URL), return zones / A+AAAA records ordered by name, store nothing, and map Cloudflare failures to `502 cloudflare_error`. They are the only way to choose a zone and record: `DdnsConfig` and `DdnsConfigCreate` require `record_id`, the Controller never resolves a record by name, and `PATCH` with a new `zone_id` but no `record_id` is `400`. | **Planned** (spec done; Phase 4) | — |
| REQ-API-021 | `AgentBinding` returns read-only `status` (`ready`/`unreachable`/`unknown`), `version` and `last_ok_at` from the dispatcher's last contact; absent until the dispatcher reports them. | **Planned** (spec done; Phase 3) | — |
| REQ-API-022 | Accounts are managed by the `homecore user add\|passwd` command (Phase 1b), not by the API or the UI; there is no first-run bootstrap account. | Implemented (`homecore user add\|passwd\|list`, REQ-AUTH-015..018) | `TestUserAddPasswdListEndToEnd` |

## Endpoint implementation status

The full contract (brief §4 plus additions) is in `api/openapi.yaml`.

| Endpoints | Status |
|-----------|--------|
| `/healthz`, `/readyz`, `/api/openapi.json`, `/api/docs` | Implemented |
| `/api/v1/auth/login`, `/refresh`, `/logout`, `/me` | Implemented ([authentication.md](authentication.md)) |
| `GET/POST /api/v1/devices`, `GET/PATCH/DELETE /api/v1/devices/{id}` | Implemented (Phase 2) |
| `POST /api/v1/devices/{id}/probe`, `GET /api/v1/devices/{id}/history` | Implemented (Phase 2) |
| `POST /api/v1/devices/{id}/wake` | Implemented (Phase 2) |
| `POST /api/v1/devices/{id}/shutdown` | Planned (Phase 3, `501`) |
| `GET /api/v1/commands`, `GET /api/v1/commands/{id}` | Implemented (Phase 2) |
| `/api/v1/system/info` | Implemented |
| `/api/v1/system/metrics`, `/api/v1/system/controller-key` | Planned (Phase 3/4) |
| `DELETE /api/v1/devices/{id}/agent` | Implemented (Phase 2; the binding is stored now, the dispatcher is Phase 3) |
| `/api/v1/ddns*` incl. `/ddns/cloudflare/*` lookups | Planned (Phase 4) |

## Known gaps

- The per-IP limit on `POST /auth/*` is tested (`TestLoginRateLimit`); there are still no automated tests for the general 600/min limit, CORS, body limit, security headers, request timeout, TLS,
  the `503` readiness path, or the fail-closed policy path.
- `audit_log` is written for authentication and account changes (REQ-AUTH-014) and for device changes and wake (REQ-DEV-010); shutdown entries arrive with Phase 3.
- `SystemMetrics` and `DdnsConfig` gained optional fields (`cpu_cores`, `load_average`, `kernel`, `restricted`, `process.pid/uptime_seconds`; `detected_ip`, `dns_value`, `last_checked_at`) that the Phase 4 providers must fill; until then they are absent.

## Spec changes in Phase 2

`api/openapi.yaml` was changed only where the implementation needed the contract to say more; no
operation, field or status code was removed or renamed.

| Change | Why |
|--------|-----|
| `400 bad_request` added to `listDevices`, `getDeviceHistory` and `listCommands` | a bad `limit` (`0`, over 200) or a foreign cursor is rejected; the handlers always did and the responses now validate |
| `409 conflict` added to `updateDevice` | renaming onto an existing name (case-insensitive) or binding an `agent_id` that belongs to another device |
| Two DDNS descriptions quoted (`record_id`, `last_checked_at` on `DdnsConfig`) | unquoted commas in YAML flow mappings made `DdnsConfig` an invalid schema for validators (kin-openapi reported extra sibling fields); the generated Go type is unchanged, only the embedded spec text |

`make generate` rewrote `internal/api/gen/api.gen.go` accordingly (it embeds the spec), so
`git diff --exit-code internal/api/gen` differs from the last commit until this change is committed;
a second `make generate` is byte-identical. The frontend's generated types
(`frontend/src/api/generated`) were not regenerated here (the submodule is read-only for this work)
and are stale for the same points.
