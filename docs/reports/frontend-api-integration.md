# Report: Frontend ↔ API integration

| | |
|---|---|
| Date | 2026-11-12 (first pass); resolution log in §9 (second pass, same day) |
| Scope | `frontend/` (git submodule, HomeCoreUI) wired to the Controller REST API (`api/openapi.yaml` 0.1.0) |
| Related | [web-ui requirements](../requirements/web-ui.md), [ADR-009/010](../design/decisions.md), [changelog](../changelogs/2026-11-12-frontend-api-integration.md) |
| Status | UI side wired and smoke-tested against a spec-conformant mock. **Controller business endpoints still return 501**, so the end-to-end path against the real Controller is not yet possible. |

## 1. Summary

The submodule arrived as a complete UI that ran only on mock data (`mockData.ts` + `localStorage`).
It is now a client of the REST API. The Controller side is the blocker: of the 19 documented paths
only `/healthz`, `/readyz`, `/api/openapi.json` and `/api/docs` work; login, devices, commands,
metrics and DDNS answer `501` and every protected route answers `401` (`auth.RejectAll`).

What changed in the UI is listed in §2. What does **not** line up between the UI and the API is
listed in §4 (API/spec gaps), §5 (inconsistencies found in the original UI, mostly fixed) and §6
(infrastructure). Each item has an ID so the roadmap and changelogs can refer to it.

Severity: **High** = blocks a user-visible flow or breaks a stated requirement · **Medium** = feature
degraded or workaround in place · **Low** = cosmetic or future-proofing.

## 2. What was wired

| Area | Before | Now |
|------|--------|-----|
| Auth | No login, no roles | Login screen, JWT access + rotating refresh token, silent refresh (early and on `401`), logout, `viewer` read-only gating, session shared across tabs |
| Devices | Mock list in `localStorage` | `GET/POST/PATCH/DELETE /devices` (cursor pages followed), `POST /devices/{id}/probe`; polled every 10 s |
| Wake-on-LAN | Timer-driven simulation | `POST /devices/{id}/wake` → follow `GET /commands/{id}` until terminal; `completed` = online, `timed_out` = "packet sent, not online", `failed` = error text |
| Shutdown | Timer-driven simulation, HMAC token | `POST /devices/{id}/shutdown` → follow command (`sent` → signature check, `acknowledged` → waiting offline, `completed` = offline verified); agent binding = Agent ID + port + scheme; Controller Ed25519 public key shown for `trusted_controllers` |
| Activity | Mock events | Controller command history (`GET /commands`) merged with events raised in the current browser session |
| System | Random-walk fake telemetry | `GET /system/metrics` polled every 10 s; missing figures shown as "—" |
| DDNS | One fake Cloudflare account with zone/record pickers | `GET/POST/PATCH/DELETE /ddns`, `POST /ddns/{id}/sync` (admin only); several records supported; manual Zone ID |
| Settings | Fake server settings | Account, controller info (`/healthz`), controller key, browser-local defaults, links to `/api/docs` |
| Delivery | Standalone Vite app | `make ui-build` builds into `web/static` (embedded by `go:embed`); `make ui-dev` proxies `/api` to a local Controller; placeholder page when the UI is not built |

Code map: `frontend/src/api/` (client, endpoints, mappers, wire types), `context/AuthContext.tsx`,
`context/AppContext.tsx`, `components/auth/LoginScreen.tsx`.

## 3. Blocking: the Controller does not implement the API yet

| ID | Sev | Finding | Evidence | Needed |
|----|-----|---------|----------|--------|
| GAP-BE-01 | High | No operation except health/docs is implemented | `curl -X POST /api/v1/auth/login` → `501`; `GET /api/v1/devices` → `401` with any token | Roadmap Phase 1b (JWT), Phase 2 (devices, commands), Phase 3 (controller key, shutdown), Phase 4 (DDNS, metrics) |
| GAP-BE-02 | High | `501` responses have an **empty body**, not the error envelope that REQ-API-001 promises for every error | `curl -i` shows `HTTP/1.1 501` with `Content-Length: 0` (no `Content-Type`) | Make `gen.Unimplemented` answer through `httpx.WriteError(…, "not_implemented", …)`. The UI already copes (it falls back to the status text) |
| GAP-BE-03 | High | Login cannot succeed, so the UI stops at the login screen with the message "Sign-in is not implemented on this Controller yet" | Real-Controller smoke test (§7) | Phase 1b |

Until these land, the full flow was exercised only against a mock (§7). **Re-run the UI smoke test
against the real Controller as each phase completes**; the mock encodes my reading of the spec, not
the Controller's behaviour.

## 4. API / spec gaps exposed by the UI

| ID | Sev | Gap | What the UI does now | Suggested change |
|----|-----|-----|----------------------|------------------|
| GAP-API-01 | Medium | `Device` has no **type** (desktop/NAS/TV…) or **OS**; the UI uses both for icons and labels | Stored per browser in `localStorage` keyed by device id (`api/deviceMeta.ts`): not shared between browsers/users, lost when browser data is cleared, defaults to desktop/linux | Add optional `device_type` and `os` (or a free-form `tags`/`metadata`) to `Device`, `DeviceCreate`, `DeviceUpdate`; add a migration |
| GAP-API-02 | Medium | No way to **list Cloudflare zones/records** | Zone ID and record name are typed by hand; the old "pick zone, pick record" dropdowns are gone | `GET /ddns/zones` and `GET /ddns/zones/{id}/records` (token supplied once, or from an existing config), or accept zone *name* and resolve it server-side |
| GAP-API-03 | Medium | No **detected public IP** and no **current DNS value**; `DdnsConfig` only has `last_ip` | Dashboard "Public IPv4" shows the last IP pushed; the "WAN IP changed, sync needed" comparison cannot be shown | Add `detected_ip`, `dns_value`, `last_checked_at` to `DdnsConfig` (or `GET /system/public-ip`) |
| GAP-API-04 | Medium | `PATCH /devices/{id}` cannot **unset** anything: no way to remove an agent binding, clear the MAC, broadcast or description | Disabling "remote shutdown" in the form keeps the old binding (the form says so); clearing fields sends `""`, which the server may reject | Define merge-patch semantics with `null` (e.g. `"agent": null`), or add `DELETE /devices/{id}/agent` |
| GAP-API-05 | Medium | No **activity/audit endpoint** although the `audit_log` table exists (REQ-API-008 gap) | Activity = command history + events of this browser session; probes, device edits, DDNS actions and other users' actions are lost on reload | `GET /api/v1/activity` (cursor, filters by device/category), fed by `audit_log` and status history |
| GAP-API-06 | Low | No **settings/info endpoint**: server name, listen address, monitoring defaults, session lifetime, retention live in YAML | UI Settings shows only what `/healthz` and `/system/metrics` give; defaults for new devices are browser-local | Read-only `GET /system/info` (version, mode, listen, defaults); decide whether any setting should become writable |
| GAP-API-07 | Low | `SystemMetrics` is a subset of what the System screen was designed for: no CPU core count, kernel, per-interface MAC/Rx/Tx, PID, process uptime, load average, "restricted metrics" list for Android | Those tiles were removed; platform string is split into OS/arch | Extend `SystemMetrics` (Phase 4, `gopsutil`) or drop the tiles from the design |
| GAP-API-08 | Low | `Device` has no **last latency** or **consecutive failures** | Latency is shown only after a manual probe in the current session | Add `last_latency_ms` (and optionally `consecutive_failures`) to `Device` |
| GAP-API-09 | Medium | No **agent health/version**; the UI used to show "Agent ready/unreachable" and a version | "Agent status" is inferred: bound + device online = ready, bound + offline = unreachable. It is a guess, labelled "Bound (host online)" | Add `agent.status`, `agent.version`, `agent.last_ok_at` to `AgentBinding` once the dispatcher exists |
| GAP-API-10 | Medium | **Command semantics are not in the spec**: what `completed` means per type, when `acknowledged` occurs, default deadlines, and what `409` on wake/shutdown means (already active vs. no MAC vs. no agent vs. already online?) | UI relies on [architecture §8.2](../design/architecture.md#82-command-lifecycle) and prints the server's `message` for any error | Copy the §8.2 state table into the `Command` schema description; enumerate `409` causes per operation |
| GAP-API-11 | Medium | **Error codes are not enumerated** (`error_code` on commands: `clock_skew`, `agent_rejected`, …) | Raw `error`/`error_code` text is displayed | Document the code list (agent-protocol §errors) in the spec as an enum or table |
| GAP-API-12 | Low | **List ordering and totals are unspecified**: no `total`, no documented sort for devices/commands/history | UI assumes devices in server order and sorts activity by timestamp itself | State the order in the spec (e.g. devices by name, commands newest first); consider `total` |
| GAP-API-13 | Low | `allow_viewer_wake` appears in [architecture §9](../design/architecture.md#9-rest-api) ("viewers can wake") but `wakeDevice` in the spec is admin-only | UI follows the spec: viewers cannot wake | Pick one; update the spec or the architecture table and the config key |
| GAP-API-14 | Low | No **user/password management** (change password, list users) | Old Settings fields for username and "session timeout" were removed | Backlog; needed before exposing beyond a home LAN |
| GAP-API-15 | Low | `Device.hostname` exists in the API but not in the UI form | Not editable from the UI | Add a field to the form when hostname-based probing is implemented |
| GAP-API-16 | Low | DDNS is **admin only**, so viewers see nothing about it (not even status on the dashboard) | Viewers get an "administrators only" notice and an empty dashboard card | Decide whether a secret-free read-only summary should be viewer-visible |

## 5. Inconsistencies found in the original UI

Fixed in this change unless stated otherwise.

| ID | Sev | Inconsistency | Resolution |
|----|-----|---------------|------------|
| INC-UI-01 | High | Shutdown form asked for a per-device **HMAC SHA-256 secret** with "Rotate Secret"; the product uses **Ed25519** with no shared secret (ADR-001). It also used `agentHost` and default port **8080**; the API binds `agent_id` + port (default **9750**) on the device's own IP | Form now takes Agent ID (UUID), port, scheme and shows the Controller public key to paste into `trusted_controllers`. HMAC text removed from sheet and flow logs |
| INC-UI-02 | Medium | **MAC required** in the form; the API requires only name, IP and monitor port | MAC optional; WOL is "enabled" exactly when a MAC is set (the API has no separate flag) |
| INC-UI-03 | Medium | UI defaults differ from the API: timeout 1500 ms / min 200 (API 2000 / min 100), DDNS interval 10 min default and 5–60 min (API 300 s default, minimum 60 s, up to 24 h), WOL broadcast hard-coded `192.168.1.255` (API: optional, derived) | Form limits and defaults follow the spec; blank broadcast means "auto" |
| INC-UI-04 | Medium | Mock activity referenced endpoints not in the spec (`/devices/{id}/wol`, `PUT /ddns/zones/…/records/…`) | Mock removed; activity details use real paths |
| INC-UI-05 | Medium | Settings screen exposed server-side configuration the product keeps in YAML (HTTP port, bind address, allowed subnets, session timeout, agent secret token, debug logging, "simulate Android host", language) | Replaced by account/controller/key panels and browser-local defaults, with a note pointing to the YAML file |
| INC-UI-06 | Medium | Wake/shutdown modals treated `progress == 100` as success and had no failure or timeout state (spinners ran forever) | Modals use the flow `step`; failures and `timed_out` render in red and stop spinners |
| INC-UI-07 | Low | Hard-coded mock values leaked into screens: `192.168.1.2`, `eth0 · Port :3000`, `Linux 5.15 ARM64`, subnet `192.168.1.0/24`, load average `0.24, 0.18, 0.15`, "Zero unexpected reboots", "eMMC / Flash Storage", curl snippet host `192.168.1.2:3000` | Replaced by live values or removed |
| INC-UI-08 | Low | "Audit log persistent on host disk" and "Clear Log" implied a server audit log that does not exist | Texts corrected; button is "Clear Session Events" |
| INC-UI-09 | Medium | **Google Fonts** links are blocked by the Controller's CSP (`default-src 'self'`); Vite also inlines small fonts as `data:` URIs, which the same CSP blocks (found by running the built UI under the real CSP) | Fonts bundled with `@fontsource/*`; `build.assetsInlineLimit: 0` |
| INC-UI-10 | Medium | `package.json` could not be installed (`esbuild ^0.25` vs Vite 8 peer range) and carried unused AI Studio leftovers: `@google/genai`, `express`, `dotenv`, `tsx`, `@types/express`, `autoprefixer`, `metadata.json` (Gemini capability), `.env.example` with `GEMINI_API_KEY`, `DISABLE_HMR` config, dev server on `0.0.0.0`, package name `react-example` | Removed; package renamed `homecore-ui`; dev server binds localhost; `package-lock.json` added |
| INC-UI-11 | Low | Viewport meta disabled zoom (`maximum-scale=1`, `user-scalable=no`), an accessibility problem | Removed |
| INC-UI-12 | Low | Wake/shutdown buttons stay enabled while `power_transition` is `waking`/`shutting_down` | **Not fixed**: the field is mapped (`Device.powerTransition`) but not yet used to disable buttons |
| INC-UI-13 | Low | A viewer sees admin actions (wake, shutdown) in several places | Add/Edit/Delete/Sync are hidden; wake/shutdown stay visible but refuse with a message (server enforces `403` anyway). Hiding them everywhere is a follow-up |

## 6. Infrastructure and process

| ID | Sev | Finding | Notes / recommendation |
|----|-----|---------|------------------------|
| GAP-INF-01 | Medium | **Refresh-token rotation vs. multiple tabs.** Rotation with reuse detection revokes the whole family if two tabs refresh with the same token. The UI shares one session in `localStorage` and serialises refresh with the Web Locks API, but `navigator.locks` exists only in **secure contexts**, and the Controller is normally reached over plain `http://192.168.x.x` | On plain HTTP the lock is unavailable (the code falls back to the shared-storage check, which narrows but does not close the race). Give the Controller a short reuse **grace window** (e.g. 10 s) when it implements refresh rotation (Phase 1b) |
| GAP-INF-02 | Medium | Tokens are in `localStorage` (XSS-readable). The API has no cookie flow | Accepted for a LAN appliance, mitigated by the strict CSP; recorded as [ADR-010](../design/decisions.md). Revisit if the Controller is exposed to the Internet |
| GAP-INF-03 | Low | Same secure-context limit affects the **clipboard API**: "Copy" buttons for the controller key do nothing over plain HTTP | The key is rendered as selectable text; consider enabling TLS on the Controller (`controller.tls`) for the UI |
| GAP-INF-04 | Low | Static handler sends no `Cache-Control`/`ETag` (embedded files have a zero mod-time, so no `Last-Modified` either) | Add `immutable` for `/assets/*` (hashed names) and `no-cache` for `index.html` |
| GAP-INF-05 | Low | Polling cost per open tab: devices 10 s, commands 15 s, metrics 10 s, DDNS 30 s, health 60 s (paused in hidden tabs). The device list is refetched in full each time | Fine for <10 users; add `ETag`/`If-None-Match` or `updated_since` if it matters (no WebSocket/SSE by scope) |
| GAP-INF-06 | Medium | Wire types in `frontend/src/api/schema.ts` are **hand-written**. `openapi-typescript` needs TypeScript 5 as a peer, the UI uses TypeScript 7; nothing detects spec drift | Backlog: generate from `api/openapi.yaml` in CI (`make check-generated` style) or pin TypeScript |
| GAP-INF-07 | Medium | The frontend has **no automated tests**; the smoke test used here (headless Chromium against a mock) is not in the repository | Backlog: keep a contract mock or, better, run the UI e2e against the real Controller in CI once Phase 1b/2 land |
| GAP-INF-08 | Medium | The submodule URL is **SSH** (`git@github.com:thanhtunguet/HomeCoreUI.git`); clones without that key cannot fetch it. The UI changes of this work sit uncommitted inside the submodule | Commit and push them in `frontend/` first, then commit the new gitlink in this repo. Consider an HTTPS URL for CI |
| GAP-INF-09 | Low | The UI has no router (tab state only): no deep links, no browser Back | Acceptable for the MVP; the SPA fallback is ready if routing is added |
| GAP-INF-10 | Low | `make build` does not build the UI (CI/cross builds need no Node); `make build-full` does | Release pipeline (Phase 6) must run `make ui-build` before `make cross` |

## 7. Verification

| Check | Result |
|-------|--------|
| `cd frontend && npx tsc --noEmit` | Pass |
| `make ui-build` (Vite 8 production build into `web/static`) | Pass, no inline scripts in `index.html` |
| `make test`, `make lint`, `make cross` | Pass (7 targets) |
| `go test ./internal/api ./web` with the UI built **and** with only the placeholder | Pass both ways |
| Real Controller (`homecore --mode controller`) serving the embedded UI | `GET /` 200 with CSP headers; assets 200; `/healthz` 200; login page shows the real version; login → 501 explained in the UI; no CSP violations |
| UI against a throwaway **mock** of the spec (Node, 2 users, cursor pages of 1, 20 s access tokens), driven by headless Chromium | 19/19 checks passed: login errors, role gating (viewer), cursor paging, wake → completed, wake → timed out, shutdown → offline verified, 409 shown in the form, device create, DDNS create + sync, activity from commands, metrics + controller key, silent token refresh (3 refreshes in ~60 s), session survives reload, logout |
| UI against the real Controller beyond login | **Not possible** (GAP-BE-01) |
| Not run | Browsers other than Chromium; Android WebView / TV browsers; plain-HTTP secure-context fallbacks on a real LAN host; load/soak; accessibility audit |

The mock and the Playwright script were kept out of the repository (see GAP-INF-07).

## 8. Appendix: UI field ↔ API field

| UI (`src/types`) | API | Notes |
|------------------|-----|-------|
| `Device.ip` | `ip_address` | |
| `Device.mac` / `wol.macAddress` | `mac_address` | Upper-cased in the UI, sent lower-case `aa:bb:cc:dd:ee:ff` |
| `Device.wol.enabled` | derived: `mac_address` present | No wire field |
| `wol.broadcastAddress` | `wol_broadcast_address` | Blank = auto |
| `wol.port` | `wol_port` | Default 9 |
| `monitoring.enabled` | `enabled` | |
| `monitoring.port` / `protocolPreset` | `monitor_port` / derived | Preset derived from the port (22 SSH, 3389 RDP, 80 HTTP, 443 HTTPS, else custom) |
| `monitoring.intervalSeconds` / `timeoutMs` | `monitor_interval_seconds` / `monitor_timeout_ms` | 5–3600 s, 100–10000 ms |
| `monitoring.lastCheckedAt` | `last_checked_at` | |
| `monitoring.lastLatencyMs` | `ProbeResult.latency_ms` | Session only (GAP-API-08) |
| `Device.lastSeen` (text) | `last_seen_at` | Formatted client-side |
| `Device.status` / `powerTransition` | `status` / `power_transition` | |
| `shutdown.enabled` | derived: `agent` present | |
| `shutdown.agentId` / `agentPort` / `agentScheme` | `agent.agent_id` / `port` / `scheme` | Host = device IP |
| `shutdown.agentStatus` | derived | Inferred (GAP-API-09) |
| `Device.type` / `os` | none | Browser-local (GAP-API-01) |
| `DDNSConfig.*` | `DdnsConfig` (`zone_id`, `record_name`, `record_type`, `proxied`, `ttl`, `interval_seconds`, `enabled`, `has_api_token`, `last_ip`, `last_synced_at`, `last_error`, `last_error_at`) | `status` derived: updating / disabled / error / synced / never synced |
| `ActivityEvent` | `Command` (+ session events) | `wake` → category `wol` |
| `SystemMetrics` | `SystemMetrics` | Subset (GAP-API-07) |

## 9. Resolution log (second pass, 2026-11-12)

Additive changes that needed no product decision. Everything is optional or defaulted, so clients
and mocks written against the first pass keep working. Details: [changelog](../changelogs/2026-11-12-api-gap-quick-wins.md).

| ID | Result | How |
|----|--------|-----|
| GAP-BE-02 | Closed | `501` goes through the error envelope (`not_implemented`); `TestUnimplementedUsesErrorEnvelope` |
| GAP-INF-04 | Closed | `immutable` for `assets/*`, `no-cache` for everything else; REQ-API-016 |
| INC-UI-12 | Closed | Wake/shutdown buttons disabled while `power_transition` is not `none` (REQ-UI-017) |
| INC-UI-13 | Closed | Wake/shutdown hidden from viewers in all five places (REQ-UI-008) |
| GAP-API-01 | Closed in spec, DB and UI; handlers pending (Phase 2) | `device_type`, `os`; migration `0002`; legacy `localStorage` values pushed once by an admin session |
| GAP-API-08 | Closed in spec, DB and UI; monitor pending (Phase 2) | `Device.last_latency_ms` |
| GAP-API-04 | Closed in spec and UI; handlers pending | `""` clears text fields; `DELETE /devices/{id}/agent` (the UI no longer says the binding stays) |
| GAP-API-06 | Closed | `GET /system/info` implemented, shown read-only in Settings |
| GAP-API-07 | Spec and UI done; provider pending (Phase 4) | optional `cpu_cores`, `load_average`, `kernel`, `restricted`, `process.pid/uptime_seconds` |
| GAP-API-03 | Spec and UI done; syncer pending (Phase 4) | optional `detected_ip`, `dns_value`, `last_checked_at`; "WAN IP changed" hint shown when both IPs exist |
| GAP-API-10 | Partly closed | Command state table and the unavoidable `409` preconditions are in the spec. **Open:** already-online/offline and opposite-command cases |
| GAP-API-11 | Closed | Agent error codes documented as pass-through values (deliberately not an `enum`, so new codes do not break clients) |
| GAP-API-12 | Closed | Order stated in the spec; no `total` by design |
| GAP-INF-01 | Specified, not implemented | Grace window added to the Phase 1b checklist |

Still open and needing a decision or later phase: GAP-API-02, 05, 09, 13, 14, 15, 16 and GAP-INF-06..10.
