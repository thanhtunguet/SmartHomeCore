# Requirements: Web UI (frontend)

| | |
|---|---|
| Brief | §3.1 (static frontend serving), §3.3–3.6 (user-facing flows) |
| Code | `frontend/` (git submodule, HomeCoreUI: React 19, Vite 8, Tailwind 4), `web/embed.go`, `web/placeholder/`, `Makefile` (`ui-*`, `build-full`) |
| Design | [architecture §6.1, §10](../design/architecture.md), [ADR-009](../design/decisions.md#adr-009--frontend-as-a-git-submodule-embedded-into-the-binary), [ADR-010](../design/decisions.md#adr-010--ui-session-tokens-in-localstorage-and-polling-instead-of-push) |
| Report | [frontend-api-integration.md](../reports/frontend-api-integration.md) (gaps and inconsistencies, IDs `GAP-*`, `INC-*`) |
| Last updated | 2026-11-12 (second pass: server-side device type/OS, `/system/info`, viewer gating) |
| Status | UI wired to the REST contract and smoke-tested against a spec mock (first pass; second pass type-checked and built only); **not yet end-to-end against the real Controller** (business endpoints return `501`) |

"Smoke test" below means the headless-Chromium run against a throwaway mock of `api/openapi.yaml`,
plus the real Controller for health, `501` and `401`. Neither is in the repository (REQ-UI-016).

## Delivery

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-UI-001 | The UI is the `frontend/` submodule. `make ui-build` installs from `package-lock.json` and builds into `web/static/`, which is embedded in the binary (`make build-full`). `web/static/*` is git-ignored except `.gitkeep`. | Implemented | Build run, recorded in changelog |
| REQ-UI-002 | When `web/static/index.html` does not exist (UI not built), the Controller serves a one-page placeholder, never an empty 404, and `go build`/`go test` work on a fresh checkout without Node. | Implemented | `web/embed_test.go` (`TestStaticAlwaysHasIndex`, run with and without a built UI) |
| REQ-UI-003 | The built UI works under the Controller's CSP (`default-src 'self'`, no inline scripts, no `data:` fonts, no external fonts or scripts). Fonts are bundled; `assetsInlineLimit` is `0`. | Implemented | Smoke test under the real CSP (no violations) |
| REQ-UI-004 | `make ui-dev` runs Vite on `:3000` and proxies `/api`, `/healthz`, `/readyz` to `HOMECORE_API_URL` (default `http://127.0.0.1:8080`). The UI always calls same-origin relative URLs; no CORS is needed. | Implemented (untested) | — |

## Authentication and session

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-UI-005 | Nothing but the login screen renders until a session exists. Login uses `POST /auth/login`; `401` → "invalid credentials", `429` → retry notice, `501` → "sign-in is not implemented on this Controller yet". | Implemented | Smoke test (mock: all; real Controller: `501`) |
| REQ-UI-006 | The access token is refreshed silently before expiry and once on a `401`; the rotated refresh token replaces the old one. A failed refresh returns to the login screen. Refreshes are serialised across tabs (Web Locks when available) and the session is shared through `localStorage`. | Implemented | Smoke test (3 refreshes in about 60 s with 20 s tokens; reload keeps session). Plain-HTTP fallback and multi-tab race untested |
| REQ-UI-007 | Logout calls `POST /auth/logout` and clears local session data. | Implemented | Smoke test (mock) |
| REQ-UI-008 | `viewer` sessions cannot create, edit, delete, sync DDNS or open DDNS data, and do not see wake/shutdown actions (cards, table, detail sheet, dashboard, command palette); the UI hides or refuses those actions. The server remains the enforcement point. | Implemented | Smoke test (mock viewer) for the CRUD/DDNS part; hiding of wake/shutdown only type-checked and built, see REQ-UI-017 |

## Features

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-UI-009 | Devices: list (all cursor pages followed), create, edit, delete, manual probe against `/devices*`. Fields and limits follow the spec (MAC optional, interval 5–3600 s, timeout 100–10000 ms). Device type and OS are stored by the Controller (`device_type`, `os`); values kept in `localStorage` by earlier builds are pushed once by an admin session and then deleted. Removing the shutdown binding calls `DELETE /devices/{id}/agent`. The last probe latency comes from `last_latency_ms` unless a fresher probe was made in the session. | Changed | Smoke test (mock) for the unchanged parts; the new fields were only type-checked and built |
| REQ-UI-010 | Wake: `POST /devices/{id}/wake` then poll `GET /commands/{id}` until terminal; `completed` = online, `timed_out` = packet sent but host not seen online, `failed` = error text; `409`/`403` messages are shown. Requests return immediately (`202`); the UI never blocks on the machine. | Implemented | Smoke test (mock: completed, timed out) |
| REQ-UI-011 | Shutdown: confirmation, `POST /devices/{id}/shutdown`, follow the command through `sent`, `acknowledged`, `completed` (offline verified) or `failed`. The shutdown form binds an Agent (UUID, port, scheme) and shows the Controller Ed25519 public key; no per-device shared secret exists in the UI. | Implemented | Smoke test (mock: offline verified, `409` in form) |
| REQ-UI-012 | Dashboard, Devices, Activity, System and Settings read live API data and show "—" for figures the API does not return. No mock data ships in the bundle. | Implemented | Smoke test (mock, real Controller for `/healthz`) |
| REQ-UI-013 | DDNS screen manages `/ddns` records (admin only): create, edit, enable/disable, delete, manual sync; the API token is write-only and never displayed. Zone ID and record name are typed manually (GAP-API-02). | Implemented | Smoke test (mock: create and sync) |
| REQ-UI-014 | Activity lists the Controller's command history plus session-local events; reloading drops the session-local events (GAP-API-05). | Partial | Smoke test (mock) |

## Behaviour and quality

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-UI-015 | Data refreshes by polling, never by WebSocket/SSE: devices 10 s, commands 15 s, metrics 10 s, DDNS 30 s, health 60 s. Polling pauses in hidden tabs and stops on logout. Errors show a status banner and keep the last data. | Implemented | Smoke test (mock) |
| REQ-UI-016 | Frontend automated tests and CI type-check/build. | **Planned** | — (GAP-INF-06, GAP-INF-07) |
| REQ-UI-017 | Wake and shutdown buttons are disabled while the device's `power_transition` is `waking` or `shutting_down`. Settings shows the read-only effective server configuration from `GET /system/info` when available. System and DDNS views show the optional metrics and `detected_ip`/`dns_value` fields when the Controller reports them and "—" otherwise. | Implemented (untested) | `tsc --noEmit` and `make ui-build` only; not exercised in a browser |

## Known gaps

All tracked, with IDs and suggested fixes, in
[frontend-api-integration.md](../reports/frontend-api-integration.md). The ones that matter most:

- The Controller implements none of the business endpoints (GAP-BE-01..03); real end-to-end is untested.
- Agent health and an activity/audit endpoint do not exist server-side (GAP-API-09, 05). Device type/OS and `GET /system/info` do now (REQ-API-015/017).
- Wire types are hand-written and can drift from `api/openapi.yaml` (GAP-INF-06).
