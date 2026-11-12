# HomeCore — Architecture Design

| | |
|---|---|
| Status | **v0.2**, derived from [`BRIEF.md`](../../BRIEF.md) v0.1 |
| Last reviewed | 2026-11-12 |
| Requirements | [requirements/overview.md](../requirements/overview.md) |
| Related design | [database.md](database.md) · [agent-protocol.md](agent-protocol.md) · [decisions.md](decisions.md) (ADRs) |
| API contract | [api/openapi.yaml](../../api/openapi.yaml) (source of truth) |
| Roadmap | [plans/roadmap.md](../plans/roadmap.md) |

---

## 1. Purpose and scope

HomeCore is a lightweight, always-on home infrastructure service for a household or small group
(< 10 users). One Go code base produces:

- a **standalone binary** (`homecore`) for Linux, macOS (dev) and later Windows, and
- a **native library** (gomobile AAR) embedded in Android phone / Android TV Box apps.

The binary runs in one of three **modes**: `controller`, `agent` or `both`.

| Role       | Responsibilities                                                                                   |
|------------|----------------------------------------------------------------------------------------------------|
| Controller | Device registry, TCP online monitoring, Wake-on-LAN, signed shutdown dispatch, Cloudflare DDNS, JWT-protected REST API, Swagger UI, static web UI, system metrics. |
| Agent      | Small HTTP listener on a managed machine that verifies signed commands from trusted Controllers and executes a fixed allowlist of power actions. |

### 1.1 MVP (in scope)

Controller + Agent on macOS/Linux · Device CRUD · TCP monitoring · Wake-on-LAN · secure shutdown
Agent on Linux · SQLite · JWT auth · Cloudflare DDNS · Swagger UI at `/api/docs` · static file serving.

### 1.2 Explicitly out of scope (do not build)

Clustering, distributed scheduling, WebSocket/SSE realtime, plugin systems, multiple DB engines,
message brokers, Redis, arbitrary command execution on agents, multi-tenant features.

---

## 2. Analysis of the brief

The brief is sound. The main shape (Go modular monolith, SQLite, HTTP/JSON, signed agent commands,
platform adapters) is kept. The review below lists **gaps and risks** found while turning it into a
buildable design, and how this document resolves them.

| # | Finding | Resolution |
|---|---------|------------|
| 1 | `shutdown_secret` as a device field implies a shared secret stored on both sides. | Ed25519: Controller signs, Agent stores only Controller **public** keys. The device stores `agent_id` + agent endpoint, no secret. (ADR-001) |
| 2 | Device `status` mixes *observed* state (online/offline) with *intended* transitions (`waking`). | Two fields: `status` = observed (`unknown/online/offline`); `power_transition` = derived from the active command (`none/waking/shutting_down`). |
| 3 | IP is not a stable identity under DHCP. | Identity = UUID. `ip_address` / `hostname` are mutable *addresses*; `agent_id` identifies a running agent. (ADR-002) |
| 4 | `command accepted` ≠ `machine off/on`. | Explicit command state machine with monitor-based verification and deadlines. (ADR-003, §8.2) |
| 5 | SQLite on Android via gomobile is the largest technical risk. | Pure-Go `modernc.org/sqlite`, one driver everywhere. Cross-compile verified for `android/arm64`, `linux/{arm,arm64,amd64}`, `windows/amd64`, `darwin/arm64` with `CGO_ENABLED=0`. **On-device spike still required**, especially 32-bit `armeabi-v7a` TV boxes. (ADR-004) |
| 6 | Two Controllers on one LAN may both act on an Agent. | Agent trusts a list of Controller keys; idempotency by `command_id`; per-action cooldown. Monitoring by several controllers is harmless (read-only). (ADR-005) |
| 7 | **Clock skew**: signed commands carry a timestamp (±30 s). Cheap Android TV boxes often have no RTC and boot with a wrong clock until NTP syncs. | Agent returns a distinguishable `clock_skew` error code; Controller surfaces it in the command result. Controller exposes its time in `/healthz`. |
| 8 | **Replay after agent restart**: an in-memory nonce cache is lost on restart, so a captured request could be replayed within the skew window. | Agent additionally rejects any command whose `issued_at` is older than the agent process start time. No persistent nonce store required. |
| 9 | JWT over plain HTTP on a LAN can be sniffed. | Optional TLS on the Controller listener; recommend reverse proxy / VPN for remote access. Short access-token TTL (15 min) + rotating refresh tokens. |
| 10 | WOL from Android: `255.255.255.255` may leave via the wrong interface. | Default to the **directed subnet broadcast** computed from the device IP + local interface mask; per-device `wol_broadcast_address` override; optional port 9 / 7. |
| 11 | Android may kill background work (Doze, app standby). Phones are poor 24/7 hosts. | Kotlin host runs a foreground service; Go side must tolerate being stopped/restarted at any time (all state persisted, idempotent startup). TV Box is the primary Android target. |
| 12 | Agent needs root-level power to shut down, but the HTTP server must not run as root. | Linux: dedicated `homecore` user + polkit rule allowing only `org.freedesktop.login1.power-off`/`reboot`. Agent calls `systemctl poweroff`. |
| 13 | Shutdown request may be cut off when the machine powers down. | Agent replies `202 Accepted` first, executes after a configurable delay (default 3 s). |
| 14 | Public IP lookup from a single provider can lie or fail. | Query ≥ 2 providers; require agreement (quorum); keep last known IP on disagreement and record an error. |
| 15 | Spec drift between OpenAPI and handlers. | **Spec-first**: `api/openapi.yaml` → `oapi-codegen` (chi server + models + embedded spec). Generated code is committed. |
| 16 | Storing every probe result would grow the DB unboundedly. | `device_status_history` stores **transitions only**; retention job prunes history/commands older than N days. |

---

## 3. System context

```
                    LAN / VPN                                     Internet
 ┌────────────┐   HTTPS/HTTP + JWT   ┌──────────────────────────┐   HTTPS (scoped token)  ┌────────────────┐
 │ Browser /  │ ───────────────────▶ │       Controller          │ ──────────────────────▶ │ Cloudflare API │
 │ Mobile UI  │                      │  REST API · Swagger · UI  │ ──────────────────────▶ │ Public-IP svcs │
 └────────────┘                      │  Monitor · WOL · DDNS     │                         └────────────────┘
                                     │  Commands · SQLite        │
                                     └───┬──────────┬────────┬───┘
                     TCP connect probe   │          │ UDP    │ HTTP POST, Ed25519-signed
                  (ssh/rdp/agent port)   │          │ magic  │ /agent/v1/commands
                                         ▼          ▼ packet ▼
                                ┌──────────────────────────────────┐
                                │ Managed machine (Linux/Windows)  │
                                │  └─ HomeCore Agent (non-root)    │
                                │       └─ polkit → systemctl poweroff
                                └──────────────────────────────────┘
```

---

## 4. Runtime model

### 4.1 One binary, several modes

```
homecore --mode controller   # Controller only (default on Android)
homecore --mode agent        # Agent only
homecore --mode both         # Both, in one process, on two separate listeners
```

In `both` mode the two roles share only the logger and process lifecycle. They have **separate
listeners, separate auth and separate config sections**. The Agent never reads the Controller DB
and the Controller REST API cannot reach Agent internals except over the signed protocol.

### 4.2 Process lifecycle

```
main / mobile.Start
  └─ config.Load (file → env overrides → validate)
  └─ app.New(cfg)               build components for the selected mode
  └─ app.Run(ctx)               errgroup: every component's Run(ctx) in parallel
        Controller: storage.Open+Migrate → schedulers (monitor, ddns, retention) → HTTP server
        Agent:      identity load/create → HTTP listener
  └─ SIGINT/SIGTERM or mobile.Stop → ctx cancel → graceful shutdown (bounded by shutdown_timeout)
```

Rules: every long-running component implements `Run(ctx context.Context) error` and returns when
`ctx` is cancelled. No goroutine is started outside a component's `Run`. Startup is idempotent so
Android can stop and restart the library freely.

---

## 5. Code layout and dependency rules

```
cmd/homecore/            CLI entrypoint (flags → config → app.Run)
mobile/                  gomobile bindings (only gomobile-compatible types)
api/openapi.yaml         REST contract (source of truth)
internal/
  app/                   composition root: wiring + lifecycle per mode
  config/                config structs, defaults, file/env loading, validation
  logging/               slog setup
  api/                   HTTP server, router, middleware, handlers, docs serving
    gen/                 oapi-codegen output (do not edit)
    middleware/
    handlers/
  auth/                  users, password hashing, JWT, refresh tokens, roles
  devices/               device model, validation, repository
  monitoring/            TCP probe, hysteresis, scheduler job, status history
  wol/                   magic packet build/send, MAC parsing, broadcast resolution
  commands/              command model + state machine, dispatcher, history
  agent/                 Agent listener, verifier, executor
    protocol/            signed-command wire format (shared by Controller client and Agent)
  ddns/                  public IP discovery, Cloudflare client, sync job
  system/                metrics collection (via platform provider)
  secrets/               master key, AES-GCM sealing of secrets at rest
  storage/               SQLite open/pragmas, migrations (embedded), tx helpers
    migrations/          versioned *.sql files
  scheduler/             tiny interval runner with jitter
  platform/              OS adapters: power control, metrics (build-tagged)
web/                     embedded UI (static/ = build of the frontend submodule, placeholder/ = fallback) + offline Swagger UI assets
frontend/                git submodule (HomeCoreUI source; built by `make ui-build`, see ADR-009)
configs/                 example configuration
deploy/                  systemd units, polkit rules
docs/                    requirements, design, plans, changelogs (see docs/README.md)
scripts/                 dev scripts (fetch Swagger UI, etc.)
```

Deviation from the brief's tree: `platform/` and `migrations/` moved under `internal/` because Go
`//go:embed` cannot reach parent directories and OS adapters should not be importable externally.

**Dependency rules** (enforced by review; keep the graph acyclic):

- `api/handlers` → domain packages (`devices`, `commands`, `auth`, `ddns`, `system`). Never the reverse.
- Domain packages depend on `storage` only through small repository interfaces defined **in the
  domain package** (consumer-side interfaces). SQL lives next to the domain (`devices/repo_sqlite.go`).
- `agent/protocol` depends on nothing but the stdlib — it is shared by both roles.
- `platform` is a leaf. Domain code receives platform capabilities as interfaces.
- Only `app` knows about all packages (composition root). No global singletons, no `init()` side effects.

Not every module needs model/service/repository layers — add a layer only when it has a second
implementation or a test that needs it.

---

## 6. Module design

### 6.1 HTTP API server (`internal/api`)
- `chi` router; generated `ServerInterface` from the spec; handlers embed `gen.Unimplemented`, so
  unimplemented operations return `501` while staying documented.
- Middleware order: request-id → client-ip (X-Forwarded-For only from `trusted_proxies` CIDRs) → recoverer → structured access log → body limit (1 MiB)
  → timeout (30 s) → CORS (allowlist, off by default) → rate limit (per-IP, stricter on `/auth/*`)
  → JWT auth (per-operation, driven by the spec's `security` requirements).
- `GET /api/openapi.json` serves the embedded spec; `GET /api/docs` serves Swagger UI from embedded
  assets (no CDN). `/healthz` = process alive; `/readyz` = DB reachable and migrations applied.
- Errors use one JSON shape: `{"error": {"code": "...", "message": "..."}}`.
- Optional TLS (`controller.tls.cert_file/key_file`).

### 6.2 Device registry (`internal/devices`)
Fields (see `devices` table): `id`, `name`, `description`, `hostname`, `ip_address`, `mac_address`,
`wol_broadcast_address`, `wol_port`, `monitor_port`, `monitor_interval`, `monitor_timeout`,
`enabled`, `status`, `last_seen_at`, `last_checked_at`, `created_at`, `updated_at`, plus an optional
**agent binding** (`agent_id`, `agent_port`, `agent_scheme`).
- Probe target = `hostname` if set, else `ip_address`.
- MAC normalised to `aa:bb:cc:dd:ee:ff`. `shutdown_secret` from the brief is **removed** (ADR-001).
- Read APIs never return secrets (there are none on devices any more).

### 6.3 Online monitor (`internal/monitoring`)
- TCP connect probe (`net.DialTimeout`), not Telnet.
- One scheduler tick every `monitor.tick` (default 5 s) selects devices whose `next_probe_at` is due;
  per-device interval (default 30 s) + random jitter (±10 %); bounded worker pool (default 8);
  timeout default 2 s.
- **Hysteresis**: 1 success → `online`; `offline_threshold` (default 2) consecutive failures →
  `offline`. Counters live in memory; `unknown` after restart until the first conclusive result.
- Writes `device_status_history` only on transitions; updates `last_seen_at` on success.
- Exposes an `Observe(deviceID) <-chan Status` hook used by the command dispatcher to verify
  wake/shutdown outcomes, and `ProbeNow(id)` for `POST /devices/{id}/probe`.

### 6.4 Wake-on-LAN (`internal/wol`)
- Magic packet: 6 × `0xFF` + 16 × MAC (102 bytes), UDP to broadcast:9 (configurable).
- Broadcast resolution: device override → directed broadcast of the local interface subnet that
  contains the device IP → `255.255.255.255`.
- Sending is fire-and-forget; verification happens through the command state machine.

### 6.5 Commands (`internal/commands`)
- Types: `wake`, `shutdown` (later `reboot`). Every request creates a row with a UUID `command_id`
  and returns `202 Accepted` immediately; the HTTP request never waits for the machine.
- **Dedup**: if a non-terminal command of the same type exists for the device, return it instead of
  creating another.
- Dispatcher goroutine drives state transitions (§8.2) and records `audit_log` entries.

### 6.6 Agent (`internal/agent`)
- Separate listener (default `:9750`), separate config, no DB (state in a small data dir:
  `agent_id`, optional agent key pair).
- `POST /agent/v1/commands` — verify (§ agent-protocol) → `202` → execute after delay.
- `GET /agent/v1/healthz` — unauthenticated liveness; can double as the monitor port.
- Action allowlist from config (`shutdown`, optionally `reboot`). **No shell, no arguments.**
- Executes via `platform.PowerController` (Linux: `systemctl poweroff|reboot`; macOS: dry-run log).

### 6.7 DDNS (`internal/ddns`)
Flow: scheduled job → discover public IP (≥ 2 providers, quorum) → validate v4/v6 → compare with
last known → GET Cloudflare record → PATCH only if different → persist state + `ddns_sync_history`.
- Multiple records; `A` and `AAAA`; per-config interval; manual `POST /ddns/{id}/sync`.
- Retry with exponential backoff (1 s → 5 min cap, jitter) on transient errors.
- API token per config, sealed at rest; documented minimum scope: `Zone → DNS → Edit` on one zone.

### 6.8 Storage (`internal/storage`)
- `modernc.org/sqlite` (pure Go). DSN pragmas: `journal_mode=WAL`, `busy_timeout=5000`,
  `foreign_keys=ON`, `synchronous=NORMAL`.
- `SetMaxOpenConns(1)` for writes is unnecessary at this scale; use a small pool (4) and short
  transactions. All multi-row changes run in `storage.WithTx`.
- Versioned migrations embedded from `internal/storage/migrations/NNNN_name.sql`, applied in order
  in one transaction each, tracked in `schema_migrations`.
- Timestamps stored as **INTEGER unix milliseconds (UTC)**; IDs as TEXT UUIDv7 (time-sortable).
- Backup: `VACUUM INTO '<path>'` (consistent online snapshot), exposed later as an admin endpoint.

### 6.9 Secrets at rest (`internal/secrets`)
- A 32-byte **master key** from (in order): `HOMECORE_MASTER_KEY` env (base64) → host-provided key
  (Android Keystore via the Kotlin host) → `<data_dir>/master.key` (created `0600` on first run).
- Secrets (Cloudflare tokens, Controller Ed25519 private key, JWT signing key) are sealed with
  AES-256-GCM, `nonce || ciphertext`, with the column name as associated data.
- The JWT signing key and the Controller signing key are **different keys** (brief §6).

### 6.10 System metrics (`internal/system`)
`SystemMetricsProvider` interface: memory (total/available/used), CPU %, uptime, disk usage,
local IPs, process RSS, Go runtime stats. Linux/macOS implementation uses `gopsutil` (Phase 4).
Android implementation is supplied by the Kotlin host through a gomobile callback interface.

### 6.11 Scheduler (`internal/scheduler`)
`scheduler.Every(ctx, interval, jitter, fn)` — a ticker loop with jitter and panic recovery. No cron
expressions, no persistence; jobs re-derive their work from the DB on each tick.

---

## 7. Data model (SQLite)

Full specification (tables, columns, constraints, indexes, conventions, migration process):
**[database.md](database.md)**. Authoritative DDL: `internal/storage/migrations/`.

Summary: 12 STRICT tables in four groups. **Identity**: `users`, `refresh_tokens`. **Devices**:
`devices`, `agent_bindings`, `device_status_history`. **Operations**: `commands`, `audit_log`,
`controller_keys`. **DDNS and settings**: `ddns_configs`, `ddns_sync_history`, `system_settings`.
`schema_migrations` belongs to the migration runner. The brief's `agent_credentials` is split into
`agent_bindings` (per device) and `controller_keys` (per Controller), because with Ed25519 the
secret material belongs to the Controller, not the device.

---

## 8. State machines

### 8.1 Device status (observed)

```
            success                     success
 unknown ───────────▶ online ◀──────────────────────┐
    │                   │ failure (count < threshold)│ stays online
    │ failures ≥ thr    │ failures ≥ threshold       │
    ▼                   ▼                            │
 offline ◀──────────────┘ ───────── success ─────────┘
```
`power_transition` (`none | waking | shutting_down`) is **derived** from the device's active
(non-terminal) command and returned alongside `status`; it is never stored on the device.

### 8.2 Command lifecycle

```
                 dispatch ok                    agent 202
  pending ─────────────────▶ sent ──────────────────────────▶ acknowledged
     │                         │  (shutdown only)                 │
     │ dispatch error          │ agent rejects / unreachable       │ monitor: offline before deadline
     ▼                         ▼                                   ▼
   failed                    failed                            completed
                               │ wake: monitor online before deadline → completed
                               │ deadline passed                   │ deadline passed
                               ▼                                   ▼
                           timed_out                           timed_out
```
- Terminal states: `completed`, `failed`, `timed_out` (the brief's “unknown”; renamed to avoid
  clashing with device status `unknown`).
- Default deadlines: wake 180 s, shutdown 120 s (configurable).
- `wake` skips `acknowledged` (no agent involved).
- Transitions are validated by `commands.CanTransition`; illegal transitions are programming errors.

---

## 9. REST API

Namespace `/api/v1`; full contract in `api/openapi.yaml`. Summary (brief §4, plus additions marked ✚):

| Method | Path | Auth |
|--------|------|------|
| POST | `/api/v1/auth/login` · `/auth/refresh` · ✚`/auth/logout` | public / public / user |
| GET | ✚`/api/v1/auth/me` | user |
| GET/POST | `/api/v1/devices` | user / admin |
| GET/PATCH/DELETE | `/api/v1/devices/{id}` | user / admin / admin |
| POST | `/api/v1/devices/{id}/wake` | admin (✚ viewers can wake if `allow_viewer_wake`) |
| POST | `/api/v1/devices/{id}/shutdown` | **admin** |
| POST | `/api/v1/devices/{id}/probe` | user |
| GET | `/api/v1/devices/{id}/history` | user |
| GET | `/api/v1/commands` · ✚`/commands/{id}` | user |
| GET | `/api/v1/system/metrics` | user |
| GET/POST | `/api/v1/ddns` | admin |
| ✚PATCH/DELETE | `/api/v1/ddns/{id}` | admin |
| POST | `/api/v1/ddns/{id}/sync` | admin |
| GET | `/healthz`, `/readyz`, `/api/openapi.json`, `/api/docs` | public |

Pagination: `?limit=&cursor=` (cursor = opaque, UUIDv7-based). Times: RFC 3339 UTC in JSON.

---

## 10. Security model

| Zone | AuthN | AuthZ |
|------|-------|-------|
| REST API | JWT (HS256, 15 min) + rotating refresh token (30 d) | roles `admin`, `viewer` |
| Agent listener | Ed25519 signature by a trusted Controller key | action allowlist |
| Swagger UI | none (static); API calls still require JWT | grants nothing |
| Cloudflare | scoped API token | DNS edit on one zone |

Principles:
1. Agent never executes arbitrary commands; actions are an enum.
2. Agent listener is LAN-only; remote access via VPN (WireGuard/Tailscale), never port-forwarded.
3. Separate keys: JWT signing key ≠ Controller signing key ≠ master key.
4. No plaintext secrets in the DB; no secrets in API responses or logs.
5. Shutdown requires `admin` and writes `audit_log`.
6. First run: if there are no users, an `admin` is created from `HOMECORE_ADMIN_PASSWORD` or a random
   password printed **once** to the log (Android host displays it).
7. Login is rate-limited per IP; generic error messages; constant-time password verification.
8. Agent runs unprivileged; power actions via polkit (Linux) / `SeShutdownPrivilege` (Windows).

---

## 11. Configuration

YAML file (`--config`), then `HOMECORE_*` environment overrides, then validation. See
[configs/homecore.example.yaml](../../configs/homecore.example.yaml). Android passes the YAML string and
its app-private data dir to `mobile.Start`.

---

## 12. Platform adapters

| Capability | Linux | macOS (dev) | Windows (later) | Android |
|------------|-------|-------------|-----------------|---------|
| Power control (agent) | `systemctl poweroff/reboot` via polkit | dry-run (log only) | `InitiateSystemShutdownExW` | n/a (controller only) |
| System metrics | gopsutil | gopsutil | gopsutil | Kotlin callback |
| Service manager | systemd unit | `go run` / launchd | Windows Service | Foreground service |
| Master key | file / env | file / env | DPAPI (later) | Android Keystore via host |

Build tags: note that `GOOS=android` also satisfies the `linux` constraint, so Linux-only adapters
use `//go:build linux && !android`.

---

## 13. Observability

- `log/slog`, text in dev, JSON in production; request-id on every API log line.
- `/healthz`, `/readyz`; `/api/v1/system/metrics` includes process RSS and goroutine count.
- No Prometheus endpoint in MVP (cheap to add later behind config).

---

## 14. Testing strategy

- Unit tests per package (protocol signing/verification, replay cache, MAC parsing, magic packet,
  state machine, hysteresis, config validation).
- Storage tests against a temp-file SQLite DB (real driver, no mocks).
- API tests with `httptest` against the real router + temp DB.
- Integration test: Controller and Agent in `both` mode with a fake `PowerController`.
- Cross-compile check in CI for all targets (`make cross`).
- Resource benchmark (Phase 6): idle RSS target < 30 MB, idle CPU ≈ 0 %.

---

## 15. Roadmap and implementation status

Moved to [plans/roadmap.md](../plans/roadmap.md). Per-requirement status lives in
[requirements/](../requirements/overview.md).
