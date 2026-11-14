# 2026-11-14 — Legacy device endpoints and scoped client tokens

| | |
|---|---|
| Author | agent |
| Scope | `api/openapi.yaml`, `internal/api` (handlers, middleware, server), `internal/auth`, `internal/devices`, `cmd/homecore`, `frontend/` (submodule), docs |
| Requirements | REQ-API-026..028, REQ-AUTH-019/020, REQ-DEV-012, REQ-UI-021; ADR-017 |

## Added
- `GET /api/turn-on`, `/api/turn-off`, `/api/is-online` with the required query parameter `device` (UUID = id,
  anything else = name, case-insensitive). Thin adapters over the wake/shutdown requests: same idempotency,
  `409`s, audit (`device.wake`/`device.shutdown`, `via: legacy`, acting user) and the shared 1 req/s
  per-device throttle. `turn-*` answer `200` + text `true`; `is-online` answers `true`/`false` from the stored
  status. Errors keep the JSON envelope.
- `homecore token create --user NAME [--label] [--ttl 10y]`: long-lived HS256 token with `scp: "legacy"`
  (default 10 years, max 20), printed once, audited as `auth.legacy_token_issue` without the token.
- `middleware.Authorize` refuses a scoped token with `403` on every operation not tagged `legacy` in the spec.
- `devices.Service.Resolve` + `Repository.GetByName`.
- Frontend (submodule): "Legacy API Endpoints" section in the device detail sheet with copy buttons
  (`src/api/legacy.ts`, `legacy.test.ts`); generated types regenerated.
- ADR-017.

## Changed
- `WakeDevice`/`ShutdownDevice` now share `requestWake`/`requestShutdown` with the legacy handlers (behaviour
  and audit output unchanged).
- `DeviceThrottle` exposes `Admit`/`WriteRejection`; the router passes the same instance to the handlers.

## Notes
- **Device reference**: Controller ids are UUIDv7, never integers, so "integer id → id, otherwise name" means
  the name is what the UI shows today (the rule is implemented and tested for integer ids anyway). A device
  named like a number (`42`) is addressed by that name.
- **Token revocation** is the account's (disable/delete) or replacing the signing key; there is no per-token
  list. Disabling an account still needs SQL (no `user disable` yet). `user passwd` does not revoke access tokens.
- `turn-on` followed by `turn-off` for one device within a second gets `429` on the second (ADR-012).
- The `403` for scoped tokens is not declared on the `/api/v1` operations in the spec (like the fail-closed `403`).
- GitNexus impact (run after the edits, which should have preceded them): `middleware.Authorize` **HIGH**
  (1 direct caller, 3 flows; the change only adds a refusal for tokens with a scope, covered by tests);
  `WakeDevice`, `ShutdownDevice`, `JWT.Authenticate` LOW.
- The frontend is a git submodule: its changes are in the working tree of `frontend/` and need their own
  commit in HomeCoreUI, then a submodule pointer bump here.

## Verification
| Command | Result |
|---------|--------|
| `make test` | pass (all packages) |
| `go test -race ./internal/api/... ./internal/auth/... ./internal/devices/... ./cmd/...` | pass |
| `make lint` | gofmt + `go vet` clean (golangci-lint not installed, skipped) |
| `make cross` | all 7 targets build with `CGO_ENABLED=0` |
| `make generate` twice | second run byte-identical; `make check-generated` fails only because `api.gen.go` is not committed yet |
| frontend `npm run lint`, `npm test` (161 tests), `npm run check:api`, `npm run build` | pass |
| smoke test, real binary | user + token by the CLI, Controller started, device created; with the legacy token `is-online` → `false`, `turn-on` → `200 true`, an immediate `turn-off` → `429`, unknown device → `404`, missing `device` → `400`, no token → `401`, the token on `/api/v1/devices` → `403` |
| GitNexus `detect-changes --scope all` | reports **critical** (28 files; `run` in `cmd/homecore/main.go` gained the `token` dispatch, which sits on every startup flow, and the doc sections); the touched flows are covered by the tests above |

**Not run**: a real old app, the web UI in a browser (rendering, clipboard), a real Wake-on-LAN packet or a
real shutdown through the legacy endpoints.

## Known gaps / follow-ups
- Per-token revocation and `homecore user disable|enable|delete` (roadmap backlog).
- Try the section in a browser and with a real old app (roadmap).
