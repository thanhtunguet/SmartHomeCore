# 2026-11-11 — Project bootstrap: analysis, design, foundation and documentation

| | |
|---|---|
| Author | Claude Code agent session, with the maintainer |
| Scope | Whole repository (initial skeleton) + `docs/` |
| Requirements | Baseline created: REQ-RT-001..010, REQ-API-001..014, REQ-ST-001..007, REQ-SEC-001..005, REQ-AG-001..014 |

## Added

### Analysis and design
- Analysed `BRIEF.md` and recorded 16 gaps and risks with their resolutions
  ([architecture §2](../design/architecture.md#2-analysis-of-the-brief)). The main ones: clock skew
  on TV boxes without a clock battery, replay after an agent restart, observed vs. intended device
  state, and least-privilege shutdown.
- Decided the brief's five open questions plus three foundational choices as ADR-001..008
  ([decisions.md](../design/decisions.md)): Ed25519 agent auth, UUID device identity, command
  state machine, `modernc.org/sqlite` everywhere, multi-controller trust, spec-first API, minimal
  layering, offline Swagger UI.
- Normative Agent Protocol v1 ([agent-protocol.md](../design/agent-protocol.md)).
- REST contract `api/openapi.yaml` (OpenAPI 3.0.3): every brief endpoint plus `auth/logout`,
  `auth/me`, `commands/{id}`, `system/controller-key`, `ddns/{id}` PATCH/DELETE.

### Foundation (Phase 1)
- Go module `github.com/thanhtunguet/homecore` (Go ≥ 1.26), `Makefile`, `.gitignore`.
- `internal/config`: YAML + env + flags with strict validation; `configs/homecore.example.yaml`, `configs/dev.yaml`.
- `internal/logging` (slog), `internal/version` (ldflags), `internal/httpx` (server timeouts, graceful serve, JSON errors).
- `internal/app`: composition root for `controller`, `agent` and `both` modes (errgroup lifecycle).
- `cmd/homecore` entrypoint with `--config`, `--mode`, `--data-dir`, `--version`.
- `internal/api`: chi router, oapi-codegen generated server (`internal/api/gen`, committed), and
  authorization policy derived from the spec's `security`. Middleware: request id, trusted-proxy
  client IP, recoverer, access log, security headers, CORS, per-IP rate limits (stricter for
  `/auth/*`), 1 MiB body limit, 30 s timeout.
- `/healthz`, `/readyz`, `/api/openapi.json`, offline Swagger UI at `/api/docs` (pinned
  `swagger-ui-dist` 5.17.14 with checksums, `scripts/fetch-swagger-ui.sh`), embedded static UI with SPA fallback.
- `internal/storage`: SQLite (WAL, busy_timeout, FK, `BEGIN IMMEDIATE`), embedded versioned
  migration runner, `WithTx`, `Backup` (`VACUUM INTO`); migration `0001_init.sql` with 12 STRICT
  tables, including a DB-enforced "one active command per device and type" and "one active
  controller key".
- `internal/secrets`: master key resolution (env → host → `master.key` 0600) and AES-256-GCM sealing with associated data.
- `internal/scheduler`: interval runner with jitter and panic recovery.

### Secure Agent (Phase 3, Agent side)
- `internal/agent/protocol` (stdlib only): command format, Ed25519 signing with domain
  separation, verifier (authenticate before parsing, agent_id, allowlist, ±skew, start-time floor,
  single-use nonce, bounded fail-closed cache).
- `internal/agent`: listener (`POST /agent/v1/commands`, `GET /agent/v1/healthz`), persistent
  `agent_id`, `command_id` idempotency, cooldown, `202`-then-execute after `action_delay`.
- `internal/platform`: `PowerController` (Linux `systemctl poweroff|reboot`, dry-run elsewhere), `MetricsProvider` interface.
- `deploy/`: hardened systemd units for Controller and Agent, polkit rule limited to logind
  power-off/reboot for `homecore-agent`, install notes.
- `cmd/devtools`: `keygen` and `send` (signed command) for manual agent testing.

### Building blocks (Phase 2, not wired yet)
- `internal/wol`: MAC parsing/normalisation, magic packet, directed-broadcast resolution, UDP send.
- `internal/monitoring`: TCP connect probe and hysteresis `Tracker`.
- `internal/commands`: types, statuses and validated transitions.
- `internal/devices`, `internal/auth`, `internal/ddns`, `internal/system`: models, interfaces and TODO markers.
- `mobile`: gomobile binding surface (`Start/Stop/IsRunning/LastError/Version/SetLogSink`), controller-only.

### Documentation structure
- `docs/README.md` index, plus:
  - `docs/requirements/`: `overview.md` (goals, platforms, scope, point-by-point brief
    traceability matrix) and requirement documents for the well-implemented areas:
    `runtime-and-configuration.md`, `http-api.md`, `storage-and-secrets.md`, `shutdown-agent.md`.
  - `docs/design/`: `architecture.md`, `database.md` (new: conventions, ER diagram, every table,
    indexes, lifecycle, migration process), `agent-protocol.md`, `decisions.md`.
  - `docs/plans/roadmap.md`: phase checklists, next steps, backlog of known gaps.
  - `docs/changelogs/`: convention, template, and this entry.
- `AGENTS.md` (`CLAUDE.md` links to it): documentation rules that agents must follow.

## Changed
- Moved `docs/architecture.md`, `docs/decisions.md`, `docs/agent-protocol.md` to `docs/design/`
  (git history kept). The data-model section now points to `database.md`; roadmap and status moved to `plans/roadmap.md`.
- Updated doc paths in `README.md`, code comments (`internal/agent`, `internal/agent/protocol`,
  `internal/commands`) and one comment line in `0001_init.sql` (comment only, no schema change).

## Fixed (during the session)
- The DB file was created `0644`; it is now pre-created as `0600` (it holds password hashes and sealed secrets).
- Replaced chi's deprecated, spoofable `RealIP` with `ClientIPFromXFF` restricted to the
  `controller.trusted_proxies` CIDRs (the config key replaces the earlier boolean).
- Lowered the `go` directive from 1.27.2 to 1.26.0, the real minimum required by `modernc.org/sqlite`.

## Security
- The Agent never runs a shell or takes arguments from requests; unauthenticated callers get one generic `401`.
- Secrets have a sealed-at-rest design; refresh tokens are stored as hashes only; `api_token` is write-only in the API.
- Every protected API route answers `401` until JWT lands (`auth.RejectAll`), so nothing is exposed by accident.

## Verification
- `go test ./...`: 23 tests pass; `go test -race ./...` is clean; `go vet` and gofmt are clean.
- `make generate` is deterministic (byte-identical output).
- `make cross`: linux/amd64, linux/arm64, linux/arm, windows/amd64, darwin/arm64, darwin/amd64 and android/arm64 build with `CGO_ENABLED=0`; the stripped binary is about 15 MB.
- Smoke test in `both` mode on macOS: health/readiness/docs/spec OK; protected route 401; signed
  shutdown 202 then dry-run executed; second command `already_scheduled`; unsigned 401; graceful exit 0.
- DB file permission `0600` checked manually after the fix.
- **Not run**: gomobile AAR build, on-device Android tests, systemd/polkit on a real Linux host,
  resource benchmarks, tests for rate limiting/CORS/TLS.

## Known gaps / follow-ups
See [roadmap backlog](../plans/roadmap.md#backlog-known-gaps-from-requirement-docs). Most important:
JWT auth (Phase 1b), device CRUD and the monitor job (Phase 2), the Controller-side shutdown
dispatcher and key management (Phase 3), and the Android on-device spike (Phase 0).
