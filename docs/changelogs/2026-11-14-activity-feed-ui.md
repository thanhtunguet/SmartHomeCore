# 2026-11-14 — Activity screen wired to `GET /activity`

| | |
|---|---|
| Author | Claude Code agent session (frontend part by a delegated subagent) |
| Scope | `frontend/` (submodule), `docs/` |
| Requirements | `REQ-UI-014` changed |

## Changed
- Activity reads the persistent audit feed (`listActivity`, `category`/`device_id` filters, cursor paging capped at 200 rows with a notice when older entries exist), polled every 15 s and refetched after the user's own mutating actions.
- Wake/shutdown outcomes come from command history, merged into the feed row with the same `command_id` (`src/api/activity.ts`, pure and unit-tested).
- Local events for device/DDNS changes are gone (the server records them); probe results stay browser-local. "Clear Session Events" became "Clear local events".
- New category "Accounts"; lines show "by <actor>".

## Verification
Run on 2026-11-14: `npm run lint` (pass), `npm test` (7 files, 153 tests pass), `npm run check:api` (up to date), `npm run build` (pass), `make ui-build` and `go test ./web` (pass). Curl-level check against a throwaway real Controller: the `/activity` JSON matched the mapper and the wake command merged into its feed row.

Not run: any browser session; truncation, failed/timed-out commands, auth failures and DDNS rows against live data (unit tests only).
