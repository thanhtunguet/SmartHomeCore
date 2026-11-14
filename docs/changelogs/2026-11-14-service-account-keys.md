# 2026-11-14 — Service account keys

| | |
|---|---|
| Author | agent |
| Scope | `api/openapi.yaml`, `internal/servicekey` (new), `internal/auth`, `internal/api` (server, handlers, middleware), `internal/app`, migration `0007`, `frontend/` (submodule), docs |
| Requirements | REQ-AUTH-021..027, REQ-API-029..031, REQ-SEC-006, REQ-UI-023/024; ADR-018 |

## Added
- **`internal/servicekey`**: the Controller generates an Ed25519 key pair per key and stores the private
  key sealed with the master key (AAD `service_account_keys.private_key:<id>`). The credential is an
  EdDSA JWT (`kid` = key id), shown once and never stored, but derivable from the row. TTL defaults to
  1 year (60 s to 20 years). Verification checks the signature, then the row (revoked, expired, grace)
  and the owner (exists, enabled) on every request.
- **Migration `0007_service_account_keys.sql`** and a `retention` target that prunes ended keys.
- **API**: `POST/GET /service-account-keys`, `DELETE /service-account-keys/{id}` (session token only)
  and `POST /auth/service-key/rotate`: authenticated by the old key, re-validated inside the
  transaction, creates a new key with a new key pair (same owner/name/scope, old TTL or the request's).
  The old key is superseded, not revoked: it works for 1 more minute, and a retry inside that window
  returns the same successor (`200`), so a lost response does not lock an agent out. `409` when that
  successor is no longer usable. The path is under `/auth/`, so it has the login per-IP limit.
- **Scopes** `legacy` and `mcp`, declared per operation with the new `x-token-scopes` extension.
  `mcp` = device control + read-only monitoring (list in ADR-018); never configuration, DDNS, the
  Controller key or key management.
- `auth.Router` (dispatch by JWT `alg`), `auth.TokenAlgorithm`, `Principal.KeyID`.
- Audit: `service_key.create|revoke|rotate` in the same transaction as the change; device audit rows made
  with a key carry `service_key_id`.
- **Web UI** (`frontend/`, submodule): a **Service Account Keys** card in Settings (REQ-UI-023/024): key list with
  scope, expiry, last use and state; create form (name, scope with descriptions, lifetime in days with presets,
  default 1 year); one-time key panel with Copy and `curl` examples (use, rotate); revoke with confirmation. No
  Rotate button, because a key rotates itself. New files `src/api/serviceKeys.ts` (+ test),
  `src/components/settings/ServiceKeysSection.tsx` (+ static-render test); `endpoints.ts`, `schema.ts` and the
  contract test cover the three operations; `src/api/generated/schema.d.ts` regenerated (`npm run gen:api`).
- ADR-018; requirements, database, architecture (§10.2), roadmap updated.

## Changed
- `middleware.Requirement.Legacy bool` became `Requirement.Scopes []string` (+ `AllowsScope`);
  `PolicyFromSpec` reads `x-token-scopes` in addition to the `legacy` tag, `Authorize` compares the
  principal's scope with it. Behaviour for session tokens and for the HS256 legacy tokens is unchanged.
- `403` is now declared on the `mcp` operations in the spec (a `legacy` key gets it there).
- `TestPolicyMarksTheLegacyOperationsOnly` now expects the rotate operation next to the three legacy ones.

## Decisions to review
- **The server holds the private keys**, as asked. They are not needed to verify a credential; they are
  what makes the credential rebuildable (idempotent rotation retry). Cost: DB + master key can mint
  credentials for any key row (equivalent to the existing JWT signing key). ADR-018 lists the alternatives.
- **`mcp` allowlist** includes `wakeDevice` and `shutdownDevice` (an agent that manages devices needs them);
  remove `mcp` from those operations in the spec if an agent must not power machines off.
- **Grace window** is the constant `servicekey.RotationGrace` = 1 minute, not a config key.
- **Ownership**: a key belongs to a user; users see and revoke only their own keys (no roles, ADR-011).

## Notes
- The MCP server is not in this repository; `mcp` is the scope it authenticates against.
- The frontend is a git submodule: its changes sit in the working tree of `frontend/` and need their own commit in
  HomeCoreUI, then a pointer bump here. Until then `git status` here shows `m frontend`.
- GitNexus impact before editing: `Authorize` and `PolicyFromSpec` **HIGH** (1 direct caller `newRouter`,
  3 flows, authentication of every protected operation); `Principal` LOW; `Requirement` UNKNOWN (no
  resolvable callers; confirmed by text search: only `authz.go` and its test). The change to them is a
  superset (scopes list instead of a bool) and every flow is covered by `internal/api`, `middleware` tests.
- GitNexus `detect-changes --scope all`: **critical** (18 files, 61 flows; `runController` gained the
  service wiring, `Authorize` and `auditTarget` sit on every protected request), covered by the runs below.

## Verification
| Command | Result |
|---------|--------|
| `make test` | pass (all packages) |
| `go test -race ./internal/servicekey/` | pass (includes 8 concurrent rotations of one key → one successor) |
| `make lint` | `go vet` clean (gofmt clean; golangci-lint not installed, skipped) |
| `make cross` | all 7 targets build with `CGO_ENABLED=0` |
| `make generate` | succeeded; `internal/api/gen/api.gen.go` regenerated |
| smoke test, real binary (temp data dir) | `homecore user add`, Controller started: create an `mcp` key → `201`; `GET /devices` with it → `200`; `GET /ddns` → `403`; rotate → `200` with `previous_key_valid_until` 60 s ahead; the new key `GET /devices` → `200`; a retry with the old key returned the identical new key; revoke the new key → `204`, then `401`; the key and its signature are absent from the Controller log |

| frontend `npm run lint`, `npm test` (192 tests), `npm run check:api`, `npm run build` | pass |
| browser run, headless Google Chrome (playwright-core, outside the repo) against a real Controller built from this tree and the Vite dev server | create (90 days) → one-time JWT panel, Copy puts the exact key on the clipboard → the key `GET /devices` `200`, `GET /ddns` `403` → dismiss, key gone from the DOM → reload, listed as Active → revoke → Revoked, button gone → Controller `401`. No page errors besides the missing `favicon.ico`. Screenshots viewed (dark theme) |

**Not run**: a real MCP client or legacy app using a key; a clock-based wait through the real 1-minute
grace (the unit tests use a fake clock); the UI in light theme, on a narrow/mobile viewport, and in Firefox/Safari;
golangci-lint. The first browser attempts failed because of the test script (it clicked the Controller-key Copy
button above the keys card, and a selector matched the description text), not the app; the script was fixed, not
the UI.

## Known gaps / follow-ups
- A `homecore` subcommand for creating keys without the API, the MCP server itself (roadmap, "Service account keys").
- Bearer keys: no proof of possession, no per-device or per-IP restriction; a stolen old key can fetch the
  new key during the grace window.
- No cap on keys per user; no user-facing rotate-by-id.
