# Roadmap

| | |
|---|---|
| Source | Brief §7 (phases), §8 (MVP); [architecture](../design/architecture.md) |
| Last updated | 2026-11-14 (decisions: no roles, idempotent commands + throttle, Cloudflare lookups, user CLI) |

Legend: `[x]` done and verified · `[~]` partially done · `[ ]` not started.
Tick items in the same change that completes them, and add a changelog entry.

## Status at a glance

| Phase | Scope | Status |
|-------|-------|--------|
| 0 | Android compatibility spike (parallel to Phase 1) | Started: cross-compile verified |
| 1 | Core foundation | **Done** |
| 1b | JWT authentication (MVP; the brief places it under §3.1) | Next |
| 2 | Device management | Building blocks done |
| 3 | Secure Agent | Agent side done; Controller side pending |
| 4 | DDNS and system monitoring | Interfaces only |
| 5 | Android integration | Binding surface only |
| 6 | Hardening | Not started |

## Next up (ordered)

1. **Phase 1b — JWT auth**: unblocks every protected endpoint.
2. **Phase 2 — device CRUD + monitor job**: first user-visible feature.
3. **Phase 2 — WOL command flow**: the MVP "wake" story end to end.
4. **Phase 3 — Controller key management + dispatcher**: the MVP "shutdown" story end to end.
5. **Phase 0 — on-device Android spike**: should not slip past Phase 2.

## Phase 0 — Android compatibility spike

- [x] `modernc.org/sqlite` cross-compiles with `CGO_ENABLED=0` for android/arm64 (ADR-004)
- [x] `mobile` package compiles for android/arm64 and android/arm
- [ ] Build the AAR with `make android-aar` (gomobile + NDK)
- [ ] Minimal Kotlin host: foreground service calling `mobile.Start/Stop`, `LogSink` → Logcat
- [ ] SQLite WAL works on device (arm64 **and** 32-bit armeabi-v7a TV box)
- [ ] WOL broadcast from Android reaches the LAN
- [ ] 24 h run on a TV box; measure RSS and battery/CPU

## Phase 1 — Core foundation ✅

- [x] Go module, Makefile, `.gitignore`, example + dev configs
- [x] Config loader: YAML + env + flags, strict validation (REQ-RT-003/004)
- [x] Structured logging with slog (REQ-RT-006)
- [x] Lifecycle and modes `controller|agent|both`, graceful shutdown (REQ-RT-001/002/005)
- [x] HTTP server with chi and the middleware chain (REQ-API-009..014)
- [x] Spec-first OpenAPI + codegen, spec-derived authorization policy (REQ-API-006/008)
- [x] Offline Swagger UI at `/api/docs`, spec at `/api/openapi.json` (REQ-API-002/003)
- [x] Health and readiness endpoints (REQ-API-004)
- [x] SQLite + versioned migrations + full initial schema (REQ-ST-*)
- [x] Master key + AES-GCM sealing (REQ-SEC-001/002)
- [x] Static frontend serving with SPA fallback (REQ-API-005)
- [x] Frontend submodule wired to the REST contract: login, devices, wake, shutdown, DDNS, activity, system; embedded build with placeholder fallback (REQ-UI-001..015, ADR-009/010). Verified against a mock only; re-run against the real Controller per phase

## Phase 1b — Authentication

- [ ] argon2id password hashing (`golang.org/x/crypto/argon2`)
- [ ] JWT signing key created and stored sealed in `system_settings`
- [ ] `auth.JWT` Authenticator (HS256, 15 min access token) replacing `RejectAll`
- [ ] Refresh-token rotation with family reuse detection and a short reuse **grace window** (about 10 s: a token presented again within it returns the already issued pair instead of revoking the family; the UI cannot serialise refreshes across tabs on plain HTTP, GAP-INF-01)
- [ ] `homecore user add|passwd` command (works on the DB file, no running Controller needed; prompts for or reads the password from stdin) replacing the first-run bootstrap: no default account, nothing logged. Then remove `controller.auth.bootstrap_admin_user` from config and `HOMECORE_ADMIN_PASSWORD` from the docs (ADR-011)
- [ ] Handlers: `login`, `refresh`, `logout`, `me`
- [ ] `audit_log` writes for login success/failure
- [ ] Tests: login rate limit (429), token expiry, refresh reuse → family revoked, reuse inside the grace window → same pair

## Phase 2 — Device management

- [x] Device model and schema (`devices`, `agent_bindings`, `device_status_history`)
- [x] TCP probe + hysteresis tracker (`monitoring.Probe`, `monitoring.Tracker`)
- [x] WOL packet, MAC validation, directed-broadcast resolution (`wol`)
- [x] Command state machine (`commands.Transition`) + DB-enforced dedup
- [ ] Devices repository + service (validation, `409` on duplicate name)
- [ ] Handlers: list/create/get/update/delete devices, probe, history (cursor pagination); honour `device_type`/`os`, clear-with-`""` on `PATCH`, and the fixed list order (REQ-API-017)
- [x] Migration `0002`: `device_type`, `os`, `last_latency_ms` on `devices`
- [x] Per-device wake/shutdown throttle, 1 request per second for all users (`middleware.DeviceThrottle`, REQ-API-018)
- [x] Migration `0003`: `users.role` dropped (ADR-011)
- [ ] Monitor component: tick, due devices, worker pool, jitter, persist transitions, write `last_latency_ms`
- [ ] Commands repository + dispatcher; `wake` endpoint and wake verification via monitor. Idempotent: return the active wake command; if the device is already online, complete at once (REQ-API-019)
- [ ] `power_transition` derived in device responses
- [ ] Commands list/get handlers
- [ ] Retention job (`retention_days`)
- [ ] "Wake many" (brief §3.4): decide between a batch endpoint and client-side fan-out

## Phase 3 — Secure Agent

- [x] Agent protocol v1: Ed25519 sign/verify, replay protection (REQ-AG-002..007, 013)
- [x] Agent listener, idempotency, cooldown, acknowledge-before-execute (REQ-AG-001, 008, 009)
- [x] Linux power adapter via systemctl + polkit; dry-run elsewhere (REQ-AG-011)
- [x] systemd units and polkit rule (`deploy/`)
- [x] `devtools keygen|send`
- [ ] Controller key management: generate on first run into `controller_keys` (sealed); `GET /api/v1/system/controller-key`
- [ ] Controller dispatcher for `shutdown`: signed POST, 2 s timeout, at most one immediate retry with the same `command_id` (fail fast, ADR-012), map agent error codes into the command; already offline → complete without contacting the agent
- [ ] Device ↔ agent binding in the device API, including `DELETE /devices/{id}/agent`
- [ ] `audit_log` for shutdown requests (with the acting user)
- [ ] `AgentBinding.status`, `version`, `last_ok_at` filled from the dispatcher's last contact (REQ-API-021, GAP-API-09)
- [ ] `GET /api/v1/activity` (cursor list fed by `audit_log` and command history). Cheap once the audit writes exist; what the UI will do with it is not decided yet, so add it to the spec only when it is picked up (GAP-API-05)
- [ ] Validate systemd + polkit on a real Debian/Ubuntu host
- [ ] Integration test: `both` mode with a fake `PowerController`

## Phase 4 — DDNS and system monitoring

- [ ] Public-IP discovery with ≥ 2 providers and quorum
- [ ] Cloudflare client (`net/http`, no SDK); get/update record
- [ ] Syncer job with exponential backoff; `ddns_sync_history`
- [ ] DDNS handlers (CRUD + manual sync); token sealed, never returned
- [ ] Cloudflare lookups for the form dropdowns: `POST /ddns/cloudflare/zones` and `.../zones/{zone_id}/records` (token from the body or from `config_id`; `502 cloudflare_error`); `record_id` is required and stored in `cf_record_id`; no lookup by name; `PATCH` changing `zone_id` without `record_id` → `400` (REQ-API-020, GAP-API-02)
- [ ] gopsutil metrics provider; `GET /api/v1/system/metrics`, filling the optional fields (`cpu_cores`, `load_average`, `kernel`, `restricted`, `process.pid/uptime_seconds`)
- [ ] DDNS state fields `detected_ip`, `dns_value`, `last_checked_at` filled by the syncer

## Phase 5 — Android integration

- [ ] Kotlin lifecycle wrapper (foreground service, boot receiver, restart recovery)
- [ ] Master key from Android Keystore passed to `mobile.Start`
- [ ] Android `MetricsProvider` callback
- [ ] Test on phone and TV box

## Phase 6 — Hardening

- [ ] Integration and security test suites (rate limit, CORS, body limit, TLS, fail-closed policy)
- [ ] Resource benchmark: idle RSS < 30 MB, idle CPU ≈ 0 %
- [ ] Backup/restore endpoint (`DB.Backup`) and restore procedure
- [ ] CI pipeline: lint, test, race, `check-generated`, `cross`
- [ ] Release pipeline for all targets
- [ ] Windows agent: power adapter + Windows Service

## Backlog (known gaps from requirement docs)

| Item | Source |
|------|--------|
| Cap the agent's accepted-`command_id` map (the protocol spec says caches are bounded) | REQ-AG-008 gap |
| Signed agent acknowledgements (protocol v2) | agent-protocol §7 |
| Signed agent announce for automatic IP updates | ADR-002 |
| Tests for CLI flag precedence | REQ-RT-003 gap |
| Test `/readyz` 503 path and `WithTx` rollback | REQ-API-004, REQ-ST-004 |
| Re-run the UI against the real Controller as Phases 1b/2/3/4 land (UI is verified only against a mock) | GAP-BE-01..03 |
| Hostname field in the device form (once hostname-based probing exists) | GAP-API-15 |
| Run the frontend checks (`make ui-check`) in CI once a CI pipeline exists | GAP-INF-06, 07, REQ-UI-016 |
| User management beyond the CLI (list/disable users, UI for changing the password) | GAP-API-14 |
| Release pipeline must run `make ui-build` before `make cross` | GAP-INF-10 |
