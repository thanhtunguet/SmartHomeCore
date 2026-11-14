# 2026-11-14 — No roles, idempotent commands + throttle, DDNS lookups, generated UI types

| | |
|---|---|
| Author | Claude Code agent session (frontend part by a delegated subagent), on the maintainers' decisions |
| Scope | `api/openapi.yaml`, `internal/api` (+ `gen`, `middleware`), `internal/auth`, `internal/config`, `internal/storage/migrations/0003`, `frontend/` (submodule, uncommitted), `Makefile`, `AGENTS.md`, `docs/` |
| Requirements | `REQ-API-008` changed; `REQ-API-018` implemented; `REQ-API-019..022` planned; `REQ-UI-008` removed; `REQ-UI-013` changed; `REQ-UI-016` implemented; `REQ-UI-018/019` added |
| Decisions | [ADR-011](../design/decisions.md#adr-011--no-roles-every-authenticated-user-is-equal), [ADR-012](../design/decisions.md#adr-012--idempotent-fail-fast-power-commands-with-a-per-device-throttle) |

Follows [report §10](../reports/frontend-api-integration.md#10-resolution-log-third-pass-2026-11-14-owner-decisions).

## Added
- `middleware.DeviceThrottle`: one wake/shutdown request per device per second for all users and both types, `429 rate_limited` + `Retry-After`; runs after `Authorize`.
- Spec: `AgentBinding.status/version/last_ok_at` (read-only), `POST /ddns/cloudflare/zones`, `POST /ddns/cloudflare/zones/{zone_id}/records`, `DdnsConfig.record_id` (required, no lookup by name), `429`/`502` responses, idempotency text on wake/shutdown.
- Migration `0003_users_no_roles.sql`.
- UI: per-device 1 s cooldown (`api/cooldown.ts`, `hooks/usePowerCooldown.ts`), DDNS zone/record dropdowns (the only way to choose them; no manual entry), agent status/version/last contact, generated wire types (`src/api/generated/schema.d.ts`, workspace `tools/apigen`), Vitest suites (110 tests), `check:api`.
- `make ui-test`, `make ui-check-api`, `make ui-check`.

## Changed
- Authorization: `bearerAuth` takes no scopes; `PolicyFromSpec` rejects them. `auth.Principal` lost `Role`.
- Agent dispatch policy (design only): 2 s timeout, at most one immediate retry (was 5 s, 3 retries); command deadlines unchanged.
- Tests in `internal/api/server_test.go` use two equal users.
- `AGENTS.md` (`CLAUDE.md` links to it): the "admin role" rules replaced by the no-roles and throttle rules.

## Removed
- `Role` schema, `User.role`, every `bearerAuth: [admin]`, the `Forbidden` response, `users.role`, config key `allow_viewer_wake`, all viewer/admin gating in the UI.

## Security
- The "shutdown requires admin" invariant is gone by decision (ADR-011). Authentication is still required everywhere except the public endpoints; shutdown will still write `audit_log` (Phase 3).

## Verification
Run on 2026-11-14:

| Command | Result |
|---|---|
| `go test -count=1 ./...` | pass (all packages) |
| `go vet ./...` (via `make lint`) | pass; `golangci-lint` not installed, skipped |
| `make cross` | 7 targets built |
| `go test -count=1 ./web ./internal/api` after `make ui-build` | pass |
| `make ui-build` (npm ci + Vite build into `web/static`) | pass |
| `cd frontend && npm run lint` / `npm run test:ci` / `npm run check:api` / `npm ci --dry-run` | pass / 6 files, 110 tests pass / "up to date" / clean |
| Drift check by the frontend subagent (spec copy with `/wake` renamed) | contract test fails and `check:api` exits 1 (reported by the subagent; not repeated) |

Not run: any browser session (the UI changes are type-checked, built and unit-tested only); the UI against the real Controller (business endpoints still answer `501`); the throttle with real handlers; the Cloudflare lookups (no implementation); `golangci-lint`; React component or flow tests (none exist).

## Known gaps / follow-ups
- Handlers for idempotent commands, agent status and Cloudflare lookups (Phases 2–4); `homecore user add|passwd` (Phase 1b), then remove `bootstrap_admin_user`.
- The frontend changes are uncommitted inside the submodule; commit and push there first, then the gitlink here (GAP-INF-08).
- Consequence of "dropdowns only": until the Phase 4 Cloudflare lookups exist the UI cannot create DDNS records (existing ones can be edited without a lookup).
- The UI keeps disabling wake/shutdown while `power_transition` is not `none`; with idempotent commands that could be relaxed to reattach to the running command.
