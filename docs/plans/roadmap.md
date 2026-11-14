# Roadmap

| | |
|---|---|
| Source | Brief §7 (phases), §8 (MVP); [architecture](../design/architecture.md) |
| Last updated | 2026-11-14 (Phase 4 done against fakes; real Cloudflare run and Linux metrics run open) |

Legend: `[x]` done and verified · `[~]` partially done · `[ ]` not started.
Tick items in the same change that completes them, and add a changelog entry.

## Status at a glance

| Phase | Scope | Status |
|-------|-------|--------|
| 0 | Android compatibility spike (parallel to Phase 1) | Started: cross-compile verified |
| 1 | Core foundation | **Done** |
| 1b | JWT authentication (MVP; the brief places it under §3.1) | **Done** |
| 2 | Device management | **Done** |
| 3 | Secure Agent | **Done** except host validation (systemd + polkit on a real Debian/Ubuntu host, open) |
| 4 | DDNS and system monitoring | **Done** except a run against the real Cloudflare and the real IP providers (open); metrics exercised on macOS only |
| 5 | Android integration | Binding surface only |
| 6 | Hardening | Not started |

## Next up (ordered)

1. **Phase 3 leftover — validate systemd + polkit on a real Debian/Ubuntu host** (a real power-off has never been exercised).
2. **Phase 0 — on-device Android spike**: slipped past Phase 3; do it before Phase 4 ends.
3. **Phase 4 leftover — one live DDNS run**: a Cloudflare token on a scratch zone and the default IP providers, to see the real API and rate-limit behaviour (everything so far ran against fakes); and the metrics on a Linux host.
4. Re-run the UI against the real Controller (Phase 2, 3 and 4 backends exist now, GAP-BE-01); regenerate the frontend types for the Phase 3 spec edits (`GET /activity`).

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

## Phase 1b — Authentication ✅

- [x] argon2id password hashing (`golang.org/x/crypto/argon2`) (REQ-AUTH-001..003)
- [x] JWT signing key created and stored sealed in `system_settings` (REQ-AUTH-004)
- [x] `auth.JWT` Authenticator (HS256, 15 min access token) replacing `RejectAll` (REQ-AUTH-005/006; `RejectAll` stays as the fail-closed default of `api.New`)
- [x] Refresh-token rotation with family reuse detection and a short reuse **grace window** (REQ-AUTH-008..010) (about 10 s: a token presented again within it returns the already issued pair instead of revoking the family; the UI cannot serialise refreshes across tabs on plain HTTP, GAP-INF-01)
- [x] `homecore user add|passwd|list` command (works on the DB file, no running Controller needed; no-echo prompt, stdin or `HOMECORE_PASSWORD`) replacing the first-run bootstrap: no default account, nothing logged; `controller.auth.bootstrap_admin_user` removed from config and docs (ADR-011, REQ-AUTH-015..018)
- [x] Handlers: `login`, `refresh`, `logout`, `me` (REQ-AUTH-007..012)
- [x] `audit_log` writes for login success/failure, logout, token reuse, account changes; reusable `internal/audit` writer (REQ-AUTH-014)
- [x] Tests: login rate limit (429), token expiry, refresh reuse → family revoked, reuse inside the grace window → same pair

## Phase 2 — Device management ✅

- [x] Device model and schema (`devices`, `agent_bindings`, `device_status_history`)
- [x] TCP probe + hysteresis tracker (`monitoring.Probe`, `monitoring.Tracker`)
- [x] WOL packet, MAC validation, directed-broadcast resolution (`wol`)
- [x] Command state machine (`commands.Transition`) + DB-enforced dedup
- [x] Devices repository + service (validation, `409` on duplicate name, agent binding persistence) (REQ-DEV-001..008)
- [x] Handlers: list/create/get/update/delete devices, probe, history (cursor pagination); honour `device_type`/`os`, clear-with-`""` on `PATCH`, and the fixed list order (REQ-API-017)
- [x] Migration `0002`: `device_type`, `os`, `last_latency_ms` on `devices`
- [x] Per-device wake/shutdown throttle, 1 request per second for all users (`middleware.DeviceThrottle`, REQ-API-018)
- [x] Migration `0003`: `users.role` dropped (ADR-011)
- [x] Monitor component: tick, due devices, worker pool, jitter, persist transitions, write `last_latency_ms` (REQ-MON-001..009)
- [x] Commands repository + type-generic engine + deadline job + restart recovery; `wake` endpoint and wake verification via the monitor. Idempotent: returns the active wake command; if the device is already online, completes at once (REQ-API-019 for wake, REQ-CMD-001..008)
- [x] `power_transition` derived in device responses (REQ-DEV-009)
- [x] Commands list/get handlers (REQ-CMD-009)
- [x] Retention job (`retention_days`) for commands, status history and DDNS sync history (REQ-RET-001)
- [x] "Wake many" (brief §3.4): **client-side fan-out**, no batch endpoint (ADR-014, REQ-CMD-012)
- [x] Spec conformance test helper (every response of the implemented operations validated against `api/openapi.yaml`)
- [x] Audit entries for device create/update/delete/agent removal/wake (REQ-DEV-010)
- [x] Follow-up: frontend generated types regenerated after the Phase 2 spec edits (`npm run gen:api`; `check:api` and 110 tests pass)

## Phase 3 — Secure Agent

- [x] Agent protocol v1: Ed25519 sign/verify, replay protection (REQ-AG-002..007, 013)
- [x] Agent listener, idempotency, cooldown, acknowledge-before-execute (REQ-AG-001, 008, 009)
- [x] Linux power adapter via systemctl + polkit; dry-run elsewhere (REQ-AG-011)
- [x] systemd units and polkit rule (`deploy/`)
- [x] `devtools keygen|send`
- [x] Controller key management: generated on first run into `controller_keys` (sealed); `GET /api/v1/system/controller-key`; `homecore key show` (REQ-SHD-001, 002)
- [x] Controller dispatcher for `shutdown` (`agentctl.ShutdownDispatcher`, registered in `internal/app`, no engine change): signed POST, 2 s timeout, at most one immediate retry with the same `command_id`, agent error codes mapped into the command; already offline → completes without contacting the agent (REQ-SHD-003..008)
- [x] Device ↔ agent binding in the device API, including `DELETE /devices/{id}/agent` (done in Phase 2: stored in `agent_bindings`, REQ-DEV-006; `agent.status`/`version`/`last_ok_at` filled in Phase 3)
- [x] `audit_log` for shutdown requests (with the acting user), `device.shutdown` (REQ-SHD-009)
- [x] `AgentBinding.status`, `version`, `last_ok_at` filled from every dispatcher contact and a once-a-minute health job (REQ-SHD-010, 011, REQ-API-021, GAP-API-09)
- [x] `GET /api/v1/activity` (`listActivity`, cursor list fed by `audit_log`; REQ-API-023, GAP-API-05)
- [ ] Validate systemd + polkit on a real Debian/Ubuntu host
- [x] Integration tests: real Controller + real Agent with a fake `PowerController` (`internal/api/shutdown_test.go`), and `app.Run` in `both` mode (`internal/app/app_test.go`)

## Phase 4 — DDNS and system monitoring

- [x] Public-IP discovery with ≥ 2 providers and quorum (REQ-DDNS-001, ADR-016)
- [x] Cloudflare client (`net/http`, no SDK); get/update record (REQ-DDNS-002)
- [x] Syncer job with exponential backoff; `ddns_sync_history` (REQ-DDNS-006/007)
- [x] DDNS handlers (CRUD + manual sync); token sealed, never returned (REQ-DDNS-003..005, REQ-SEC-003)
- [x] Cloudflare lookups for the form dropdowns: `POST /ddns/cloudflare/zones` and `.../zones/{zone_id}/records` (token from the body or from `config_id`; `502 cloudflare_error`); `record_id` is required and stored in `cf_record_id`; no lookup by name; `PATCH` changing `zone_id` without `record_id` → `400` (REQ-API-020, GAP-API-02)
- [x] gopsutil metrics provider; `GET /api/v1/system/metrics`, filling the optional fields (`cpu_cores`, `load_average`, `kernel`, `restricted`, `process.pid/uptime_seconds`) (REQ-SYS-001..004, GAP-API-07)
- [x] DDNS state fields `detected_ip`, `dns_value`, `last_checked_at` filled by the syncer (migration `0006`, GAP-API-03)
- [x] DDNS audit actions in the activity feed (REQ-DDNS-008)
- [ ] Run once against the real Cloudflare API and the real public-IP providers
- [ ] Run the metrics on a Linux host (only macOS was run; Linux is covered by `make cross`)

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
| Make the audit write for `device.shutdown` blocking (refuse the action when it cannot be recorded), if the owner wants that stricter rule | ADR-015, REQ-SHD-009 |
| Controller key rotation (insert a new key, retire the old, let agents trust both) | REQ-SHD known gaps |
| Health-job interval as a config key; pin or accept self-signed agent certificates for `https` bindings | REQ-SHD known gaps |
| Activity feed from sources other than `audit_log` (monitor transitions) once the UI needs them; DDNS changes and record updates are already audited | ADR-015 |
| Cap the agent's accepted-`command_id` map (the protocol spec says caches are bounded) | REQ-AG-008 gap |
| Signed agent acknowledgements (protocol v2) | agent-protocol §7 |
| Signed agent announce for automatic IP updates | ADR-002 |
| Tests for CLI flag precedence | REQ-RT-003 gap |
| Test `/readyz` 503 path and `WithTx` rollback | REQ-API-004, REQ-ST-004 |
| Re-run the UI against the real Controller as Phases 3/4 land (UI is verified only against a mock; Phase 2 verified the API shapes with curl against `mappers.ts`, not the running UI) | GAP-BE-01..03 |
| Hostname field in the device form (once hostname-based probing exists) | GAP-API-15 |
| Run the frontend checks (`make ui-check`) in CI once a CI pipeline exists | GAP-INF-06, 07, REQ-UI-016 |
| `homecore user disable\|enable\|delete` (the `users.disabled` flag is honoured but only settable with SQL); UI for changing the password | GAP-API-14, REQ-AUTH known gaps |
| Cut off access tokens at logout/password change (token version or `jti` deny-list), if the 15 min window ever matters | REQ-AUTH known gaps |
| Per-account login lockout (only per-IP limit exists); separate rate-limit bucket for `refresh`/`logout` | REQ-AUTH known gaps |
| Account creation on Android (no CLI): decide in Phase 5 how the host app calls `auth.Accounts` through `mobile` | REQ-RT known gaps |
| Release pipeline must run `make ui-build` before `make cross` | GAP-INF-10 |
| Persist the monitor's consecutive-failure counters across restarts (today only the status survives) | REQ-MON-004 known gap |
| Shorten the deadline sweep (5 s) or complete deadline exactly via a timer, if second-level accuracy of `timed_out` ever matters | REQ-CMD-006 |
| Endpoint for `ddns_sync_history` (the UI sees only the `last_*` fields and the activity feed) | REQ-DDNS known gaps |
| Sub-tick backoff (the syncer ticks every 5 s, so a 1 s retry waits for the next tick); make the tick a config key if it matters | REQ-DDNS known gaps |
| Windows metrics (gopsutil compiles for it, untested; load average is restricted there) | REQ-SYS-001 |
