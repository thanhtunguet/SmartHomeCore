# Requirements: Controller HTTP API Foundation

| | |
|---|---|
| Brief | §3.1, §4, §6 (public API zone) |
| Code | `internal/api` (`server.go`, `middleware/`, `handlers/`, `gen/`), `api/openapi.yaml`, `web/`; authentication: [authentication.md](authentication.md) |
| Design | [architecture §6.1, §9, §10](../design/architecture.md), [ADR-006](../design/decisions.md#adr-006--spec-first-rest-api), [ADR-008](../design/decisions.md#adr-008--offline-swagger-ui) |
| Last updated | 2026-11-26 (`hostname` marked deprecated in the spec); 2026-11-23 (database-controlled OpenAPI docs and management API); 2026-11-18 (port forwarding endpoints, REQ-API-033); 2026-11-15 (alert channel endpoints, REQ-API-032; MCP endpoint; earlier: 2026-11-14 service account keys; legacy device endpoints; local interface names; Phase 4: DDNS endpoints, Cloudflare lookups, system metrics; earlier: Phase 3 shutdown, controller key, activity feed, agent status) |
| Status | **Implemented**: foundation, authentication, devices, commands (wake and shutdown), controller key, activity feed, DDNS (incl. Cloudflare lookups) and system metrics. No documented operation returns `501` any more |

## Functional requirements

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-API-001 | The Controller serves a JSON REST API under `/api/v1`, plus `/healthz`, `/readyz`, and optional `/api/openapi.json`, `/api/swagger.json`, `/api/docs`. Every error uses one envelope: `{"error":{"code":"…","message":"…"}}`, including `501 not_implemented` for documented but unbuilt operations. | Implemented (untested) | `TestRoutesAndPolicy`, `TestUnimplementedUsesErrorEnvelope` |
| REQ-API-002 | `GET /api/openapi.json` and its `/api/swagger.json` alias return the embedded spec (from `api/openapi.yaml`) as JSON when enabled; they are public and need no network access. The docs index (`/api/docs/`) and both spec endpoints are limited to 2 requests/second per client IP; static UI assets remain accessible so the page can load. Excess limited requests get `429 rate_limited` with `Retry-After: 1`. | Implemented (untested) | `TestHealthBodyAndSpec`; rate-limit coverage pending |
| REQ-API-003 | `GET /api/docs/` serves Swagger UI from pinned assets embedded in the binary (`swagger-ui-dist` 5.17.14, checksums in `web/swagger/VERSION`). No CDN, the online validator is disabled, no inline scripts. `/api/docs` redirects to `/api/docs/`. The page grants no permissions: API calls from it still need a bearer token. All docs/spec endpoints are disabled by default and served only while the persisted `openapi_enabled` setting is true. | Implemented (untested) | `TestRoutesAndPolicy`; setting and rate-limit coverage pending |
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
| REQ-API-034 | `GET/PUT /api/v1/system/settings/openapi` (any authenticated user) reads/writes the persisted `enabled` boolean controlling docs and spec endpoints; a missing setting is false. | Implemented (untested) | OpenAPI settings handler and migration coverage pending |
| REQ-API-016 | Static files: `assets/*` (content-hashed) are served with `Cache-Control: public, max-age=31536000, immutable`; `index.html` and the SPA fallback with `no-cache`. | Implemented | `TestStaticCacheHeaders` |
| REQ-API-017 | Device registry extras: `device_type` and `os` (enums, defaults `desktop`/`linux`) and read-only `last_latency_ms` on `Device`; `hostname` is **deprecated** (accepted, validated and returned, but ignored by probes; REQ-MON-010); an empty string clears `description`, `hostname`, `mac_address` and `wol_broadcast_address` on `PATCH`; `DELETE /devices/{id}/agent` removes the agent binding and is idempotent. List order is fixed: devices by name (case-insensitive), commands and status history newest first, all with keyset cursors (`limit` 1..200, `400` on a bad limit or cursor). Detail: [devices-and-commands.md](devices-and-commands.md) REQ-DEV-001..011, REQ-MON-006/007, REQ-CMD-009. | Implemented | `TestDeviceCRUDHappyPath`, `TestPatchClearsOptionalTextWithEmptyString`, `TestAgentBindingThroughTheAPI`, `TestListPaginationOverHTTP`, `TestProbeAndHistory`, `TestCommandListFiltersPaginationAndErrors` (every response checked against the spec by `specChecker`); `TestRemoveDeviceAgentPolicy`, `TestDeviceMetaColumnsDefaultAndConstrain` |
| REQ-API-018 | At most **one** `POST /devices/{id}/wake` or `/shutdown` per device per second, counted over all users and both types. Excess requests get `429 rate_limited` with `Retry-After: 1`. Enforced after authentication (anonymous requests do not use the slot); state is in memory. The UI applies the same guard (REQ-UI-018). | Implemented | `TestDeviceCommandThrottle`, `TestDeviceCommandThrottleAfterAuth`, `TestDeviceThrottle*` |
| REQ-API-019 | Wake/shutdown are idempotent and fail fast (ADR-012): an active command of the same type is returned (`202`); a device already online (wake) / offline (shutdown) gets a command that completes at once; `409` only for a missing MAC (wake) or agent binding (shutdown); agent dispatch is one attempt with a 2 s timeout plus at most one immediate retry; wake and shutdown of a device are independent. | Implemented. Wake: REQ-CMD-001..006; shutdown: REQ-SHD-003..012 (the Dispatcher retries once on a network error or 5xx with the same `command_id`; `409` for no agent binding) | `TestWakeLifecycleOverHTTP`, `TestWakeWithoutMACIs409`, `TestWakeSendFailureYieldsAFailedCommand`, `TestWakeTimesOutAtItsDeadline`, `commands.TestWakeIsIdempotent`; shutdown: `TestShutdownEndToEndWithARealAgent`, `TestShutdownOfAnOfflineDeviceCompletesWithoutContactingTheAgent`, `TestShutdownWithoutAnAgentBindingIs409`, `TestShutdownWithTheAgentDownFailsFast`, `agentctl.TestFiveHundredIsRetriedOnceWithTheSameCommandIDAndANewNonce` |
| REQ-API-020 | Cloudflare lookups for the DDNS form: `POST /ddns/cloudflare/zones` and `POST /ddns/cloudflare/zones/{zone_id}/records` take exactly one of `api_token` or `config_id` in the body (never in a URL), return zones / A+AAAA records ordered by name, store nothing, and map Cloudflare failures to `502 cloudflare_error`. They are the only way to choose a zone and record: `DdnsConfig` and `DdnsConfigCreate` require `record_id`, the Controller never resolves a record by name, and `PATCH` with a new `zone_id` but no `record_id` is `400`. Full DDNS behaviour: [ddns.md](ddns.md) REQ-DDNS-001..008. | **Implemented** (Phase 4) | `TestCloudflareLookupsOverHTTP`, `TestCloudflareLookupFailuresAre502`, `TestDDNSLifecycleOverHTTP`, `TestCreateDdnsConfigValidationOverHTTP`, `TestDDNSAndMetricsRequireAuthentication`, `ddns.TestLookups`; smoke test 2026-11-14 against a fake Cloudflare (not the real service) |
| REQ-API-021 | `AgentBinding` returns read-only `status` (`ready`/`unreachable`/`unknown`, always present on a bound device, `unknown` until the first contact), `version` and `last_ok_at` (absent until a health check or accepted shutdown succeeded). They are updated by every shutdown attempt and by the once-a-minute health check of devices seen online (REQ-SHD-010/011); the device API cannot write them. | Implemented | `TestAgentBindingThroughTheAPI`, `TestAgentStatusFromTheHealthJobOverHTTP`, `agentctl.TestHealth*`, `devices.TestAgentContactIsRecordedAndForgottenOnRebind` |
| REQ-API-023 | `GET /api/v1/activity` (`listActivity`, any authenticated user) is a read-only feed built **from `audit_log` only**, newest first (`at`, `id` descending), cursor-paginated (`limit` 1..200, `next_cursor` only when more exist; a bad limit, cursor, category or device id is `400`). Items: `id` (integer), `at`, `actor` (username snapshot, `cli`, `system`; absent when unknown), `category` (`auth`, `device`, `command`, `ddns`, `user`, `system`, derived from the action; `device.wake` and `device.shutdown` are `command`), `action`, `device_id`, `command_id`, `result` (`ok`, `failed` or an error code) and an English `message`. Filters `device_id` and `category`. Command outcomes are **not** duplicated here (`GET /commands/{id}` has them). Exposure: `remote_addr`, login failure reasons and every audit `details` field outside a whitelist (name, username, command id, status, error code, key id) are never returned. `audit_log` is not pruned (REQ-RET-002). | Implemented | `TestActivityFeedOverHTTP`, `activity.TestFeedOrderPaginationAndFilters`, `activity.TestCategorySQLMatchesCategoryOf`, `activity.TestFeedExposesOnlyWhitelistedData`, `activity.TestMessages`, `activity.TestBadRequests`; smoke test 2026-11-14 |
| REQ-API-025 | `GET /api/v1/system/metrics` (`getSystemMetrics`, any authenticated user) returns the host snapshot of [ddns.md](ddns.md) REQ-SYS-001..004: hostname, platform, kernel, uptime, `cpu_percent` (absent until two samples exist), `cpu_cores`, `load_average`, memory, disk, `local_ips`, optional `local_addresses` (interface-name/IP pairs), `process` (pid, uptime, RSS, goroutines, heap) and `restricted` (always present). One collection per 3 s however often it is polled. | Implemented | `TestSystemMetricsOverHTTP`, `TestSystemLocalAddressesOverHTTP`, `TestDDNSAndMetricsRequireAuthentication`, `system.TestCachedServesOneSnapshotPerTTL` |
| REQ-API-026 | **Legacy endpoints** (ADR-017) for apps that predate multi-device support: `GET /api/turn-on`, `GET /api/turn-off`, `GET /api/is-online`, each with the required query parameter `device` (a UUID is an id, anything else a name, compared case-insensitively; whitespace-only is `404`; missing is `400`). They need the normal `Authorization: Bearer` header (session token or a legacy token, REQ-AUTH-019); anonymous is `401`. `turn-on`/`turn-off` do what `POST /devices/{id}/wake`/`shutdown` do (idempotent, fail fast, `409` for no MAC / no agent binding, audited as `device.wake`/`device.shutdown` with the acting user and `via: legacy`) and answer `200` with the text `true`; they never wait for the machine. `is-online` answers `200` `true` when the stored status is `online`, else `false` (`offline`, `unknown`); unknown device is `404`. Text responses are `text/plain` with `Cache-Control: no-store`; errors use the JSON envelope. | Implemented | `TestLegacyTurnOnByNameAndByID`, `TestLegacyIsOnlineFollowsTheMonitor`, `TestLegacyErrorsUseTheEnvelope`, `TestLegacyTurnOffReachesTheAgent`, `TestPolicyMarksTheLegacyOperationsOnly`; `devices.TestResolveByIDOrName`; smoke test 2026-11-14 with the real binary |
| REQ-API-027 | The legacy `turn-on`/`turn-off` share the one-request-per-device-per-second slot of `/api/v1` wake/shutdown (REQ-API-018), however the device is spelled (name or id): the second request is `429 rate_limited` with `Retry-After: 1`. `is-online` is not throttled. A client that sends turn-on and turn-off back to back therefore gets `429` on the second. | Implemented | `TestLegacyThrottleIsSharedWithV1` |
| REQ-API-028 | A legacy-scoped token (`scp: legacy`) is accepted **only** on operations tagged `legacy` in the spec; on every other protected operation it gets `403 forbidden` even though the signature is valid. The policy is derived from the spec (`middleware.Requirement.Legacy`), so a new operation is closed to these tokens unless someone tags it. The `403` is not declared on the `/api/v1` operations (like the fail-closed policy `403`). | Implemented | `TestLegacyTokenIsScopedToTheLegacyEndpoints`, `TestPolicyMarksTheLegacyOperationsOnly` |
| REQ-API-029 | **Token scopes in the spec.** An operation lists the restricted credentials it accepts in the operation-level extension `x-token-scopes: [legacy, mcp]` (the `legacy` tag still implies `legacy`, REQ-API-028). `middleware.Authorize` derives this into `Requirement.Scopes`; a credential with a scope that is not listed gets `403 forbidden`, a session token is always accepted once authenticated, and a malformed extension aborts startup. An operation without the extension is session-only, so a new operation is closed to keys until it is listed. | Implemented | `TestPolicyMarksTheLegacyOperationsOnly`, `TestPolicyMCPScopeIsAnAllowlist`, `TestPolicyKeyManagementIsSessionOnly`, `TestScopesExtensionIsValidated`, `TestServiceKeyScopesAreEnforced` |
| REQ-API-030 | **Service account keys** (ADR-018): `POST /service-account-keys` (`201`), `GET /service-account-keys`, `DELETE /service-account-keys/{id}` (`204`), all session-only, and `POST /auth/service-key/rotate` (`200`, accepts `legacy` and `mcp` keys only, REQ-AUTH-024/025). Responses with a key are `Cache-Control: no-store`. Behaviour: [authentication.md](authentication.md) REQ-AUTH-021..027. | Implemented | `internal/api/servicekeys_test.go` (every response validated against the spec) |
| REQ-API-031 | The rotate endpoint is under `/api/v1/auth/`, so the per-IP limit of the credential endpoints applies (10/min, burst 5, `429 rate_limited`). | Implemented | `TestServiceKeyRotationIsRateLimitedLikeLogin` |
| REQ-API-022 | Accounts are managed by the `homecore user add\|passwd` command (Phase 1b), not by the API or the UI; there is no first-run bootstrap account. | Implemented (`homecore user add\|passwd\|list`, REQ-AUTH-015..018) | `TestUserAddPasswdListEndToEnd` |
| REQ-API-032 | **Alert channels** (REQ-ALERT-*): `GET/POST /alerts/channels` (`201`), `GET/PATCH/DELETE /alerts/channels/{id}` (`DELETE` `204`), `POST /alerts/channels/{id}/test` (`200`, REQ-ALERT-009). Every operation needs a session token (a scoped service account key gets `403`: no `x-token-scopes`), is validated against the spec, and a duplicate name is `409`. The secret is `writeOnly`; `has_secret` replaces it. Changes are audited (REQ-ALERT-010). | Implemented | `api.TestAlertChannelCRUDOverHTTP`, `TestAlertEndpointsRequireAuthentication`, `TestAlertEndpointsRefuseScopedKeys`, `TestAlertChannelTestEndpoint`, `TestAlertChannelAudit` |
| REQ-API-033 | **Port forwarding** (REQ-PF-*): `GET/POST /port-forwards` (`201`), `GET/PATCH/DELETE /port-forwards/{id}` (`DELETE` `204`). `PATCH {"enabled": bool}` is the on/off switch. Every operation needs a session token (a scoped service account key gets `403`: no `x-token-scopes`) and is validated against the spec. `400` for an invalid field (all problems listed), `409` for a duplicate name or an overlap with another **enabled** rule, `404` for an unknown id. A rule that cannot listen is still `201`/`200` with `status: error` and `last_error`, not an HTTP error. Changes are audited (REQ-PF-010). | Implemented | `api.TestPortForwardCRUDAndSwitchOverHTTP`, `TestPortForwardErrors`, `TestPortForwardThatCannotBindShowsItsErrorAndRecovers`, `TestPortForwardEndpointsRequireASession`, `TestPortForwardAudit` |
| REQ-API-024 | `GET /api/v1/system/controller-key` (`getControllerKey`, any authenticated user) returns exactly `key_id`, `algorithm` (`ed25519`) and the base64 `public_key` of the active Controller key (REQ-SHD-001/002); never the private key. `401` without a token. | Implemented | `TestControllerKeyEndpoint`, `TestAgentTrustingTheAPIKeyAcceptsTheDispatcher` |

## Endpoint implementation status

The full contract (brief §4 plus additions) is in `api/openapi.yaml`.

| Endpoints | Status |
|-----------|--------|
| `/healthz`, `/readyz`, `/api/openapi.json`, `/api/swagger.json`, `/api/docs` | Implemented; docs/spec routes default off and rate-limited (REQ-API-002/003) |
| `/api/v1/auth/login`, `/refresh`, `/logout`, `/me` | Implemented ([authentication.md](authentication.md)) |
| `GET/POST /api/v1/devices`, `GET/PATCH/DELETE /api/v1/devices/{id}` | Implemented (Phase 2) |
| `POST /api/v1/devices/{id}/probe`, `GET /api/v1/devices/{id}/history` | Implemented (Phase 2) |
| `POST /api/v1/devices/{id}/wake` | Implemented (Phase 2) |
| `POST /api/v1/devices/{id}/shutdown` | Implemented (Phase 3) |
| `GET /api/v1/activity` | Implemented (Phase 3; new operation `listActivity`) |
| `GET /api/v1/commands`, `GET /api/v1/commands/{id}` | Implemented (Phase 2) |
| `GET /api/turn-on`, `/api/turn-off`, `/api/is-online` | Implemented (legacy, outside `/api/v1`; ADR-017) |
| `/api/v1/system/info` | Implemented |
| `/api/v1/system/controller-key` | Implemented (Phase 3) |
| `/api/v1/system/metrics` | Implemented (Phase 4) |
| `DELETE /api/v1/devices/{id}/agent` | Implemented (Phase 2) |
| `GET/POST /api/v1/ddns`, `PATCH/DELETE /api/v1/ddns/{id}`, `POST /api/v1/ddns/{id}/sync` | Implemented (Phase 4) |
| `POST /api/v1/ddns/cloudflare/zones`, `POST /api/v1/ddns/cloudflare/zones/{zone_id}/records` | Implemented (Phase 4) |
| `GET/POST /api/v1/service-account-keys`, `DELETE /api/v1/service-account-keys/{id}`, `POST /api/v1/auth/service-key/rotate` | Implemented (ADR-018) |
| `POST /api/v1/mcp` | Implemented (ADR-020; [mcp.md](mcp.md)) |
| `GET/POST /api/v1/port-forwards`, `GET/PATCH/DELETE /api/v1/port-forwards/{id}` | Implemented (ADR-022; [port-forwarding.md](port-forwarding.md)) |
| `GET/POST /api/v1/alerts/channels`, `GET/PATCH/DELETE /api/v1/alerts/channels/{id}`, `POST /api/v1/alerts/channels/{id}/test` | Implemented (ADR-021; [alerts.md](alerts.md)) |

## Known gaps

- The per-IP limit on `POST /auth/*` is tested (`TestLoginRateLimit`); there are still no automated tests for the general 600/min limit, CORS, body limit, security headers, request timeout, TLS,
  the `503` readiness path, or the fail-closed policy path.
- `audit_log` is written for authentication and account changes (REQ-AUTH-014), device changes, wake and shutdown (REQ-DEV-010, REQ-SHD-009) and the creation of the Controller key; since Phase 4 also DDNS create, update, delete, manual sync and scheduled record updates (REQ-DDNS-008). The activity feed shows these audited entries only.
- `SystemMetrics` and `DdnsConfig` optional fields (`cpu_cores`, `load_average`, `kernel`, `restricted`, `process.pid/uptime_seconds`; `detected_ip`, `dns_value`, `last_checked_at`) are filled by Phase 4 (they are absent only where the platform cannot provide them, or before the first sync).
- The spec has no `GET /ddns/{id}` (it answers `405`); clients read the list.

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

## Spec changes in Phase 3

| Change | Why |
|--------|-----|
| New operation `GET /activity` (`listActivity`) with `ActivityItem`, `ActivityList`, `ActivityCategory` and a tag `activity` | GAP-API-05, REQ-API-023. Additive; the frontend is not changed by this work |
| `AgentBinding.status/version/last_ok_at` descriptions | now always filled (REQ-API-021); schema unchanged (still optional, readOnly) |
| `Command.error_code` description lists the Controller's codes | REQ-SHD-007 (`agent_unreachable`, `agent_timeout`, `agent_bad_response`, `agent_error`, `dispatch_error`, `wol_send_failed`, `interrupted`) |
| `getControllerKey` description | what the key is and where else it is printed (`homecore key show`) |
| "activity newest first" in the ordering convention | list ordering is fixed per endpoint |

`make generate` rewrote `internal/api/gen/api.gen.go`; a second run is byte-identical. The frontend's
generated types (`frontend/src/api/generated`) must be regenerated by the owner (`npm run gen:api`):
the new operation and types, plus doc comments, appear there.

## Spec change: legacy endpoints

| Change | Why |
|--------|-----|
| New operations `legacyTurnOn`, `legacyTurnOff`, `legacyIsOnline` (tag `legacy`), parameter `LegacyDevice`, responses `LegacyTrue` and `Forbidden`, a `text/plain` body, and a paragraph in `info.description` | REQ-API-026..028, ADR-017. Additive; no existing operation changed |

`make generate` rewrote `internal/api/gen/api.gen.go`; a second run is byte-identical. The frontend's
generated types were regenerated (`npm run gen:api`).

## Spec changes in Phase 4

None. `api/openapi.yaml` was already complete for DDNS and metrics (GAP-API-02/03/07 were specified in
the pre-Phase-4 spec work); `make generate` leaves `internal/api/gen` byte-identical
(`git diff --exit-code internal/api/gen` clean). Two deliberate consequences of the existing spec:
a duplicate record or a record that does not match is `400` (the spec has no `409` for DDNS), and a
failed sync is a normal `200` `DdnsSyncResult` with `result: failed`.

## Spec change: service account keys

| Change | Why |
|--------|-----|
| New operations `createServiceAccountKey`, `listServiceAccountKeys`, `revokeServiceAccountKey`, `rotateServiceAccountKey`, a tag `service-keys`, schemas `ServiceAccountKey`, `ServiceAccountKeyCreated`, `ServiceAccountKeyScope`, `CreateServiceAccountKeyRequest`, `RotateServiceAccountKeyRequest`, parameter `ServiceAccountKeyId`, and a paragraph in `info.description` | REQ-API-030, ADR-018. Additive |
| `x-token-scopes` on `getCurrentUser`, `listDevices`, `getDevice`, `wakeDevice`, `shutdownDevice`, `probeDevice`, `getDeviceHistory`, `listCommands`, `getCommand`, `getSystemMetrics`, `getSystemInfo`, `listActivity` (`mcp`), on the three legacy operations (`legacy`, `mcp`) and on rotation (`legacy`, `mcp`) | REQ-API-029. Behaviour for session tokens is unchanged |
| `403` added to the `mcp` operations above; `Forbidden` description widened to cover keys | a `legacy` key on an `mcp` operation is refused with `403`, which the conformance check requires to be declared |

`make generate` rewrote `internal/api/gen/api.gen.go`. The frontend's generated types
(`frontend/src/api/generated`) were **not** regenerated here (submodule untouched); `make ui-check-api`
will report them stale until the owner runs `npm run gen:api`. (Superseded the same day: the UI work for REQ-UI-023 regenerated them; `npm run check:api` is clean.)

## Spec change: MCP server

| Change | Why |
|--------|-----|
| New operation `callMcp` (`POST /api/v1/mcp`, tag `mcp`, `x-token-scopes: [legacy, mcp]`), responses `200`, `202`, `400` (JSON-RPC error object, not the envelope), `401`, `403`, and a paragraph in `info.description` | REQ-MCP-001..003, ADR-020. Additive; no existing operation changed |

`make generate` rewrote `internal/api/gen/api.gen.go`. The frontend's generated types
(`frontend/src/api/generated`) were **not** regenerated (submodule untouched); `make ui-check-api` will report
them stale until the owner regenerates them.

## Spec change: alerts

| Change | Why |
|--------|-----|
| New operations `listAlertChannels`, `createAlertChannel`, `getAlertChannel`, `updateAlertChannel`, `deleteAlertChannel`, `testAlertChannel`, a tag `alerts`, schemas `AlertChannel`, `AlertChannelCreate`, `AlertChannelUpdate`, `AlertChannelType`, `AlertEvent`, `AlertTestResult` and parameter `AlertChannelId` | REQ-API-032, REQ-ALERT-*, ADR-021. Additive; no existing operation changed |

`make generate` rewrote `internal/api/gen/api.gen.go`. The frontend's generated types
(`frontend/src/api/generated`) were **not** regenerated (submodule untouched); `make ui-check-api`
reports them stale until the owner runs `npm run gen:api`.

## Spec change: port forwarding

| Change | Why |
|--------|-----|
| New operations `listPortForwards`, `createPortForward`, `getPortForward`, `updatePortForward`, `deletePortForward`, a tag `port-forwards`, schemas `PortForward`, `PortForwardCreate`, `PortForwardUpdate`, `PortForwardProtocol` (`tcp`, `udp`, `universal`), `PortForwardStatus` (`running`, `stopped`, `error`) and parameter `PortForwardId` | REQ-API-033, REQ-PF-*, ADR-022. Additive; no existing operation changed. No new endpoint for the interface list: the UI reads `local_addresses` of `GET /system/metrics` |

`make generate` rewrote `internal/api/gen/api.gen.go`, and the frontend's generated types were regenerated
(`pnpm run gen:api`; `check:api` is clean).
