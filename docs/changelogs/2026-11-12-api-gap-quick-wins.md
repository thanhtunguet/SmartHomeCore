# 2026-11-12 — API gap quick wins (spec, DB, UI)

| | |
|---|---|
| Author | Claude Code agent session, with the maintainer |
| Scope | `api/openapi.yaml`, `internal/api` (+ `gen`, `handlers`, `middleware`), `internal/storage/migrations/0002`, `internal/app`, `frontend/` (submodule), `docs/` |
| Requirements | `REQ-API-001` (verified), `REQ-API-015..017` added, `REQ-UI-008/009` changed, `REQ-UI-017` added |

Scope rule: only additive changes that need no product decision and do not depend on Phases 1b–4.
See [report §9](../reports/frontend-api-integration.md#9-resolution-log-second-pass-2026-11-12).

## Added
- `GET /api/v1/system/info` (implemented, any authenticated user, no secrets).
- `DELETE /api/v1/devices/{id}/agent` (spec + policy; handler answers `501` until Phase 3).
- `Device.device_type`, `os`, `last_latency_ms`; `DeviceCreate`/`DeviceUpdate` `device_type`, `os`. Migration `0002_device_meta.sql`.
- Optional `SystemMetrics` fields and `SystemInfo`; optional `DdnsConfig.detected_ip`, `dns_value`, `last_checked_at`.
- `middleware.NotImplementedEnvelope`; `setStaticCacheHeaders`.
- UI: server-side type/OS with a one-time carry-over of the old `localStorage` values, `removeDeviceAgent`, Settings "effective configuration", extra System tiles, WAN-changed hint, `button:disabled` style.

## Changed
- Spec descriptions: command state table, `error_code` values, wake/shutdown `409` preconditions, list ordering, "empty string clears" rule.
- `api.Deps` gained `Mode` (reported by `/system/info`); `app` passes it. `handlers.New` is unchanged (`Handlers.Info` is set by the router).
- Wake/shutdown actions are hidden for viewers and disabled during `power_transition` (cards, table, detail sheet, dashboard, command palette).
- Form text for disabling remote shutdown now says the binding is removed.

## Fixed
- `501` responses carry the error envelope (GAP-BE-02); static assets are cacheable (GAP-INF-04).

## Security
- `/system/info` is test-checked not to contain key, secret, path or dir strings.

## Verification
| Command / check | Result |
|-----------------|--------|
| `make generate`, then regenerate again and diff | Deterministic. `make check-generated` fails only because the change is uncommitted |
| `make test` | Pass (new: `TestUnimplementedUsesErrorEnvelope`, `TestRemoveDeviceAgentPolicy`, `TestSystemInfo`, `TestStaticCacheHeaders`, `TestDeviceMetaColumnsDefaultAndConstrain`) |
| `make lint` | Pass (`golangci-lint` not installed: `gofmt` + `go vet` only) |
| `make cross` | Pass (7 targets) |
| `cd frontend && npx tsc --noEmit`, `make ui-build` | Pass |
| Real Controller with the built UI: login → `501` JSON envelope, `/system/info` without token → `401`, `Cache-Control` on `/` and on a hashed asset | As expected |

**Not run:** the UI in a browser or against the spec mock (the mock was not kept, GAP-INF-07), so the
new UI behaviour (viewer gating, disabled buttons, legacy meta migration, agent removal, new tiles)
is type-checked and built only. Viewer-only `/system/info` with a real login is impossible until Phase 1b.

## Known gaps / follow-ups
- Handlers for devices, `agent` removal, metrics and DDNS fields come with Phases 2–4 (REQ-API-017 is `Partial`).
- Undecided and left in the spec as such: `409` for already-online/offline, `allow_viewer_wake`, viewer DDNS summary.
- `frontend/` changes are **uncommitted inside the submodule** (GAP-INF-08): commit and push there first, then update the gitlink.
