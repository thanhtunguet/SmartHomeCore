# Requirements Overview

| | |
|---|---|
| Source | [`BRIEF.md`](../../BRIEF.md) (Technical Brief v0.1, Vietnamese) |
| Last reviewed | 2026-11-14 (service account keys) |
| Owner | Project maintainers |

This document turns the brief into the project's requirements baseline. It records which brief
points are **implemented**, which are **changed** by a design decision, and which are still
**planned**. Areas that are implemented and tested have their own requirement documents. The
others are tracked here and in the [roadmap](../plans/roadmap.md).

## 1. Product summary

HomeCore is a lightweight, cross-platform home infrastructure service that runs 24/7 on hardware
already in the house (Linux box, Android TV box, phone). One Go code base provides two roles:

| Role | Responsibility |
|------|----------------|
| **Controller** | Device registry, online monitoring, Wake-on-LAN, shutdown dispatch, Cloudflare DDNS, REST API |
| **Agent** | Small service on managed Linux/Windows machines that verifies and executes shutdown commands from the Controller |

One build supports both roles; configuration selects `controller`, `agent` or `both`.

## 2. Goals and constraints

| ID | Goal | Status |
|----|------|--------|
| G-1 | Small scale: usually fewer than 10 users | **Met by design.** SQLite, single process, no clustering. |
| G-2 | Stable 24/7 operation under low load | **Partial.** Graceful lifecycle and systemd `Restart=on-failure` are in place; no soak test yet. |
| G-3 | Low CPU and RAM when idle | **Not measured.** Benchmark planned for Phase 6 (target: idle RSS < 30 MB). |
| G-4 | Maximum shared business logic between Android and Linux | **Partial.** One Go module; gomobile binding surface exists; AAR not built yet. |
| G-5 | Easy to cross-compile, deploy and maintain | **Met.** `CGO_ENABLED=0` builds for 7 targets (`make cross`). |
| G-6 | REST API documented with OpenAPI and Swagger UI at `/api/docs` | **Met.** Spec-first, offline Swagger UI. |
| G-7 | Home-appropriate security without creating a remote-control hole | **Met** (Phase 3). Agent security, JWT authentication and Controller key management (sealed Ed25519 key, signed shutdown dispatch, audit) are done; only the systemd/polkit host validation is open. |

## 3. Platforms

| Platform | Role | Delivery | Status |
|----------|------|----------|--------|
| Android phone | Controller | Native library in Android app | **Partial.** `mobile` package compiles for android/arm64; AAR + Kotlin host not built |
| Android TV Box | Controller | Native library in launcher | **Partial.** Same as above; 32-bit `armeabi-v7a` unverified |
| Linux | Controller / Agent / Both | Standalone binary + systemd | **Implemented.** Units and polkit rule shipped; not yet validated on a real host |
| Windows | Agent (later phase) | Binary + Windows Service | **Planned.** Compiles; power adapter missing (falls back to dry-run) |
| macOS | Development / testing | Standalone binary | **Implemented** (agent runs in dry-run) |

## 4. Scope

### 4.1 MVP (from brief §8)

| MVP item | Status | Detail |
|----------|--------|--------|
| Controller + Agent on macOS/Linux | **Implemented** | [runtime-and-configuration.md](runtime-and-configuration.md) |
| Device CRUD and TCP online monitoring | **Implemented** (Phase 2): CRUD, probe, history, monitor job with hysteresis, status history, retention | [devices-and-commands.md](devices-and-commands.md) |
| Wake-on-LAN | **Implemented** (Phase 2): `wakeDevice` with idempotent, fail-fast command lifecycle, completed by the monitor or timed out | [devices-and-commands.md](devices-and-commands.md) |
| Secure shutdown Agent on Linux | **Implemented** (Phase 3): Agent plus Controller dispatcher, `shutdownDevice`, Controller key, agent status and health job; systemd/polkit host validation pending | [shutdown-agent.md](shutdown-agent.md) |
| SQLite | **Implemented** | [storage-and-secrets.md](storage-and-secrets.md) |
| JWT authentication | **Implemented** (Phase 1b): argon2id, HS256 access tokens, rotating refresh tokens with reuse detection and grace window, `homecore user` CLI; no roles (ADR-011) | [authentication.md](authentication.md) |
| Cloudflare DDNS | **Implemented** (Phase 4): public-IP quorum, Cloudflare client, sealed tokens, scheduled and manual sync with backoff, zone/record lookups, audit; not yet run against the real Cloudflare | [ddns.md](ddns.md) |
| Legacy single-device API (added after the brief, not in it) | **Implemented**: `GET /api/turn-on\|turn-off\|is-online?device=`, long-lived scoped tokens (`homecore token create`), URLs in the device sheet (ADR-017) | [http-api.md](http-api.md) REQ-API-026..028, [authentication.md](authentication.md) REQ-AUTH-019/020 |
| Service account keys (added after the brief, not in it) | **Implemented**: server-generated Ed25519 keys (private key sealed in the database), TTL default 1 year, scopes `legacy`/`mcp`, create/list/revoke by a user, self-rotation by the key with a 1-minute grace and idempotent retry, audit (ADR-018) | [authentication.md](authentication.md) REQ-AUTH-021..027, [http-api.md](http-api.md) REQ-API-029..031, [storage-and-secrets.md](storage-and-secrets.md) REQ-SEC-006 |
| Swagger UI at `/api/docs` | **Implemented** | [http-api.md](http-api.md) |
| Static file serving | **Implemented** (UI embedded via `make build-full`; placeholder otherwise) | [http-api.md](http-api.md), [web-ui.md](web-ui.md) |

### 4.2 Non-goals

Clustering, distributed scheduling, WebSocket/SSE realtime, plugin systems, multiple database
engines, message brokers, Redis, separate database servers, and executing arbitrary commands on
agents.

## 5. Requirement documents

Only areas that are implemented and tested get a detailed requirement document. Each requirement
has a stable ID, a status and the test that verifies it.

| Document | Prefix | Covers brief |
|----------|--------|--------------|
| [runtime-and-configuration.md](runtime-and-configuration.md) | `REQ-RT` | §1 roles, §2, §5 modes |
| [http-api.md](http-api.md) | `REQ-API` | §3.1, §4, §6 (API zone) |
| [devices-and-commands.md](devices-and-commands.md) | `REQ-DEV`, `REQ-MON`, `REQ-CMD`, `REQ-RET` | §3.2, §3.3, §3.4, retention (§3.7) |
| [authentication.md](authentication.md) | `REQ-AUTH` | §3.1 (JWT), §6.1, §6.6, §6.7 (audit) |
| [storage-and-secrets.md](storage-and-secrets.md) | `REQ-ST`, `REQ-SEC` | §3.7, §6 (secrets) |
| [shutdown-agent.md](shutdown-agent.md) | `REQ-AG`, `REQ-SHD` | §3.5, §6 (agent zone, Controller key, shutdown dispatch) |
| [ddns.md](ddns.md) | `REQ-DDNS`, `REQ-SYS` | §3.6 (DDNS), §3.8 (system metrics), §6.4 |
| [web-ui.md](web-ui.md) | `REQ-UI` | §3.1 (static frontend), UI for §3.3–3.6 |

Status vocabulary used everywhere:

| Status | Meaning |
|--------|---------|
| **Implemented** | Code exists and an automated test or recorded smoke test covers it |
| **Implemented (untested)** | Code exists but no automated test covers it yet |
| **Partial** | Some of the requirement is built; the rest is listed |
| **Changed** | Deliberately different from the brief; the ADR explains why |
| **Planned** | Not started; tracked in the roadmap |

## 6. Brief traceability matrix

Every point of the brief, in order. "Ref" points to the document that covers the point.

### §1 Project overview

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 1.1 | Controller role: central server (devices, monitoring, WOL, shutdown, DDNS, REST API) | Implemented (Phase 4 added DDNS and metrics) | REQ-RT-001, REQ-SHD-*, REQ-DDNS-* |
| 1.2 | Agent role: light service on Linux/Windows that verifies shutdown commands | Implemented (Linux/macOS) | REQ-AG-* |
| 1.3 | One build supports both roles, selected by configuration | Implemented | REQ-RT-001 |
| 1.4 | Goals (scale, 24/7, low resources, shared logic, cross-compile, OpenAPI, security) | See §2 above | — |
| 1.5 | Platform matrix | See §3 above | — |
| 1.6 | Go as language; Kotlin host via gomobile | Go: Implemented. Kotlin: Planned | REQ-RT-008 |

### §2 Overall architecture

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 2.1 | Modular monolith, not microservices | Implemented | [architecture §5](../design/architecture.md) |
| 2.2 | All modules in one binary / native library | Implemented | REQ-RT-001 |
| 2.3 | No message broker, Redis or separate DB server | Implemented | ADR-007 |

### §3.1 HTTP API server

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 3.1.1 | REST API with JSON | Implemented | REQ-API-001 |
| 3.1.2 | JWT authentication and authorization | Implemented (authentication; authorization = any authenticated user, ADR-011) | REQ-API-007, REQ-API-008, REQ-AUTH-004..012 |
| 3.1.3 | Swagger UI at `/api/docs` | Implemented | REQ-API-003 |
| 3.1.4 | OpenAPI spec at `/api/openapi.json` | Implemented | REQ-API-002 |
| 3.1.5 | Static frontend serving | Implemented (UI wired to the API; backend endpoints pending) | REQ-API-005, REQ-UI-001..004 |
| 3.1.6 | Health/readiness endpoints | Implemented | REQ-API-004 |
| 3.1.7 | Request logging, timeout, graceful shutdown | Implemented | REQ-API-010, REQ-RT-005 |
| 3.1.8 | CORS, rate limiting, request body limit | Implemented (untested) | REQ-API-011..013 |
| 3.1.9 | `net/http` + `chi`; spec-first or codegen | Implemented (spec-first + oapi-codegen) | ADR-006 |
| 3.1.10 | Namespace `/api/v1`, `/api/docs`, `/api/openapi.json`, `/healthz`, `/readyz` | Implemented | REQ-API-001 |
| 3.1.11 | Swagger UI works offline, no CDN | Implemented | REQ-API-003, ADR-008 |

### §3.2 Device registry

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 3.2.1 | Devices identified by UUID | Implemented (schema, UUIDv7) | [database](../design/database.md#devices) |
| 3.2.2 | Fields: name, description, ip, mac, monitor_port, enabled, last_seen_at, status, timestamps | Implemented (schema, API) | database, REQ-DEV-001/002 |
| 3.2.3 | `shutdown_port` | Changed. Moved to `agent_bindings.port` | ADR-001, ADR-002 |
| 3.2.4 | `shutdown_secret` | Changed. Removed; Ed25519 trust instead | ADR-001 |
| 3.2.5 | Advanced: `monitor_timeout`, `monitor_interval`, `wol_broadcast_address` | Implemented (schema, validated API, used by the monitor and WOL) | database, REQ-DEV-002 |
| 3.2.6 | Advanced: `shutdown_protocol` | Changed. Single protocol v1, versioned in payload | [agent-protocol](../design/agent-protocol.md) |
| 3.2.7 | Never return secrets from read APIs | Implemented (no secret columns on devices; the DDNS token is write-only and only `has_api_token` is returned, tested) | REQ-SEC-004, REQ-DDNS-004 |
| 3.2.8 | Device CRUD | Implemented (Phase 2) | REQ-DEV-001..011 |

### §3.3 Online device monitor

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 3.3.1 | TCP connect probe to a configured port (not Telnet) | Implemented (library) | `monitoring.Probe`, `TestProbe` |
| 3.3.2 | States `online` / `offline` / `unknown` | Implemented (enum, schema) | [architecture §8.1](../design/architecture.md) |
| 3.3.3 | Hysteresis: 2 failures → offline, 1 success → online | Implemented (library) | `monitoring.Tracker`, `TestTrackerHysteresis` |
| 3.3.4 | Scheduler every 30 s, 1–3 s timeout, concurrency limit, jitter | Implemented (Phase 2): per-device interval (default 30 s, tick 5 s), timeout default 2 s, bounded pool, ±10 % jitter, active-command devices probed every tick | REQ-MON-001..009 |

### §3.4 Wake-on-LAN

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 3.4.1 | UDP magic packet to subnet broadcast | Implemented (library, directed broadcast) | `wol.Send`, `TestMagicPacketLayout`, `TestDirectedBroadcast` |
| 3.4.2 | Validate MAC format | Implemented | `wol.ParseMAC`, `TestParseMAC` |
| 3.4.3 | Wake one device | Implemented (Phase 2) | REQ-CMD-001..006 |
| 3.4.4 | Wake many devices | **Changed (decision):** client-side fan-out, one `wakeDevice` per device; no batch endpoint | REQ-CMD-012, ADR-014 |
| 3.4.5 | Wake history | Implemented (Phase 2): `GET /commands` with `device_id`/`status` filters, `GET /commands/{id}`, retention | REQ-CMD-009, REQ-RET-001 |
| 3.4.6 | Optional wait-for-online; `waking` state | Changed. Derived `power_transition` + command lifecycle (the wake command stays `sent` until the monitor sees the device online); Implemented for wake | REQ-DEV-009, REQ-CMD-006, [architecture §8](../design/architecture.md) |

### §3.5 Shutdown agent

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 3.5.1 | Agent listens on its own HTTP endpoint | Implemented (`POST /agent/v1/commands`) | REQ-AG-001 |
| 3.5.2 | No static plaintext secret | Implemented | REQ-AG-002 |
| 3.5.3 | HMAC-SHA256 or Ed25519 | Changed to Ed25519 (brief's preferred option) | ADR-001 |
| 3.5.4 | Payload with action, timestamp, nonce, request id | Implemented (`command_id`, plus `v`, `agent_id`) | REQ-AG-003 |
| 3.5.5 | Signature check | Implemented | REQ-AG-004 |
| 3.5.6 | Timestamp within ±30 s | Implemented | REQ-AG-006 |
| 3.5.7 | Nonce never reused | Implemented | REQ-AG-007 |
| 3.5.8 | Action in allowlist | Implemented | REQ-AG-005 |
| 3.5.9 | Request not executed before | Implemented (`command_id` idempotency) | REQ-AG-008 |
| 3.5.10 | TLS / WireGuard / Tailscale for confidentiality | Implemented (optional TLS, untested); VPN documented | REQ-AG-012 |
| 3.5.11 | Never accept arbitrary shell commands; `reboot` optional | Implemented | REQ-AG-005, REQ-AG-010 |

### §3.6 DDNS Cloudflare

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 3.6.1 | Scheduled public-IP check and DNS update flow | Implemented (Phase 4) | REQ-DDNS-006, REQ-DDNS-007 |
| 3.6.2 | Multiple records; A and AAAA | Implemented (one configuration per record) | REQ-DDNS-003, REQ-DDNS-006 |
| 3.6.3 | Configurable interval; manual refresh | Implemented (`interval_seconds` 60..86400; `POST /ddns/{id}/sync`) | REQ-DDNS-003, REQ-DDNS-006 |
| 3.6.4 | Retry with exponential backoff | Implemented (1 s to 5 min, jittered, `Retry-After` honoured) | REQ-DDNS-007 |
| 3.6.5 | Scoped Cloudflare token | Implemented (a token per configuration, sealed at rest, sent only as a bearer header; the Controller needs only `Zone: Read` and `DNS: Edit`) | REQ-DDNS-004, REQ-SEC-003 |
| 3.6.6 | Store last success and last error | Implemented (`last_ip`, `last_synced_at`, `last_error`, `last_error_at`, plus `detected_ip`, `dns_value`, `last_checked_at` and `ddns_sync_history`) | REQ-DDNS-006 |
| 3.6.7 | Several public-IP providers, handle disagreement | Implemented (at least 2 must agree, a tie fails, public ranges only) | REQ-DDNS-001, [ADR-016](../design/decisions.md#adr-016--phase-4-decisions-public-ip-quorum-record-ids-ddns-state-and-metrics) |

### §3.7 SQLite storage

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 3.7.1 | Tables: devices, status history, users, refresh tokens, agent credentials, command history, DDNS configs, DDNS history, settings | Implemented (some renamed) | [database §3](../design/database.md) |
| 3.7.2 | WAL mode | Implemented | REQ-ST-002 |
| 3.7.3 | `busy_timeout` | Implemented | REQ-ST-002 |
| 3.7.4 | Versioned schema migrations | Implemented | REQ-ST-003 |
| 3.7.5 | Transactions for important changes | Implemented (`WithTx`) | REQ-ST-004 |
| 3.7.6 | No plaintext secrets | Implemented (sealing library + sealed BLOB columns; passwords argon2id, refresh tokens SHA-256) | REQ-SEC-001..003, REQ-AUTH-001, REQ-AUTH-008 |
| 3.7.7 | Consistent backup | Implemented (untested), no API yet | REQ-ST-006 |
| 3.7.8 | Test SQLite on Android arm64 via gomobile early | Partial (cross-compile verified; on-device pending) | ADR-004, Phase 0 |

### §3.8 System monitoring

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 3.8.1 | Memory, CPU, uptime, disk, local IPs, process memory, Go runtime metrics | Implemented (Phase 4; unreadable metrics are listed in `restricted`) | REQ-SYS-001..004 |
| 3.8.2 | `gopsutil` on Linux | Implemented (gopsutil v4; exercised on macOS only: Linux is covered by the cross build, not by a run) | REQ-SYS-001 |
| 3.8.3 | Android-specific provider via Kotlin | Planned | Phase 5 |

### §4 API design

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 4.1 | All draft endpoints (auth, devices, wake/shutdown/probe/history, metrics, DDNS, commands, docs) | Specified in `api/openapi.yaml`; health, docs, `/auth/*`, `/system/info`, devices (CRUD, probe, history, wake, agent removal) and commands implemented; shutdown, controller key and activity (Phase 3) implemented; DDNS and system metrics (Phase 4) implemented, so no documented operation returns 501 any more | REQ-API-006, REQ-API-020, REQ-API-025 |
| 4.2 | Wake/shutdown create a `command_id`; HTTP does not wait for the machine | Wake Implemented (`202` + `Command`, dedup enforced in DB); shutdown Implemented (Phase 3: `202` + `Command`, idempotent, fail-fast) | REQ-ST-005, REQ-CMD-001/003, REQ-SHD-008 |

### §5 Source code architecture

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 5.1 | Modular monolith with ports & adapters only where needed | Implemented | ADR-007 |
| 5.2 | Directory layout | Implemented with 2 deviations (`internal/platform`, `internal/storage/migrations`) | [architecture §5](../design/architecture.md) |
| 5.3 | `--mode controller|agent|both` | Implemented | REQ-RT-001 |
| 5.4 | Combined mode: two listeners, independent auth | Implemented | REQ-RT-002 |
| 5.5 | Android defaults to Controller only | Implemented (enforced by `mobile.Start`) | REQ-RT-008 |

### §6 Security decisions

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 6.1 | Public API: JWT + roles | Implemented; **roles dropped** (ADR-011) | REQ-API-007, REQ-API-008, REQ-AUTH-* |
| 6.2 | Agent listener: signature + allowlist only | Implemented | REQ-AG-* |
| 6.3 | Swagger UI grants no permission | Implemented | REQ-API-003 |
| 6.4 | Cloudflare client with scoped token | Implemented (Phase 4; tested against a fake, not the live API) | REQ-DDNS-002 |
| 6.5 | Never expose the Agent to the Internet; prefer VPN | Documented (`deploy/README.md`, config comments) | REQ-AG-012 |
| 6.6 | JWT secret ≠ Agent secret | Implemented (JWT key, master key and the Controller Ed25519 key are three distinct secrets; the Controller key is generated and sealed in Phase 3) | REQ-AUTH-004, REQ-SEC-005, REQ-SHD-001 |
| 6.7 | Shutdown requires admin and writes audit log | **Changed:** no roles (ADR-011), any authenticated user. `audit_log` writer exists (`internal/audit`, used by login/logout/account changes and, since Phase 2, device create/update/delete/agent removal/wake); since Phase 3 `shutdownDevice` writes `device.shutdown` (result ok/failed) and key creation writes `controller_key.create` | REQ-API-008, REQ-AUTH-014, REQ-DEV-010, REQ-SHD-009 |
| 6.8 | Private keys and Cloudflare tokens protected at rest | Implemented (sealer used for the JWT key, refresh-grace copies, the Controller key since Phase 3, the DDNS tokens since Phase 4 and the service account private keys) | REQ-SEC-002, REQ-SEC-003, REQ-SEC-006, REQ-AUTH-004, REQ-SHD-001, REQ-DDNS-004 |
| 6.9 | Acknowledge before executing shutdown | Implemented | REQ-AG-009 |
| 6.10 | Agent only runs predefined commands | Implemented | REQ-AG-005 |
| 6.11 | Agent runs with least privilege | Implemented (polkit + hardened systemd unit; host validation pending) | REQ-AG-011 |
| 6.12 | Ed25519: Controller key pair; Agent stores trusted Controller public keys | Implemented (Phase 3): Agent trusts configured public keys; Controller key generated, sealed, exposed at `GET /system/controller-key` and `homecore key show` | REQ-AG-002, REQ-SHD-001..003 |

### §7 Roadmap and §8 open questions

| # | Brief point | Status | Ref |
|---|-------------|--------|-----|
| 7.x | Phases 1–6 and the parallel Android spike | Tracked | [roadmap](../plans/roadmap.md) |
| 8.1 | Agent protocol: HMAC vs Ed25519 | Decided: Ed25519 | ADR-001 |
| 8.2 | Device identity independent of IP | Decided | ADR-002 |
| 8.3 | Command state machine | Decided and implemented (`commands.Transition`) | ADR-003 |
| 8.4 | SQLite driver for Android/Linux | Decided: `modernc.org/sqlite` | ADR-004 |
| 8.5 | Multi-controller trust and duplicate commands | Decided; Agent side implemented | ADR-005 |
