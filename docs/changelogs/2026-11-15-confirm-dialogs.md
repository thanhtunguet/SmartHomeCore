# 2026-11-15 — Modal confirm dialogs, disabled Power off on cards

| | |
|---|---|
| Author | Claude |
| Scope | `frontend/src/components/common/ConfirmDialog.tsx` (new), `devices/DeviceDetailSheet.tsx`, `devices/DevicesScreen.tsx`, `settings/ServiceKeysSection.tsx`, `settings/SettingsScreen.tsx`, `ddns/DDNSScreen.tsx`, `docs/requirements/web-ui.md` |
| Requirements | `REQ-UI-028`, `REQ-UI-029` added |

## Changed
- All four `window.confirm` calls (remove device, revoke service key, stop DDNS updates, clear browser data) now open the in-app `ConfirmDialog` modal. A grep of `frontend/src` finds no remaining `alert(`, `confirm(` or `prompt(`.
- Device card: the "Pwr n/a" text is replaced by a Power off button that is disabled (with a tooltip: agent not enabled / device offline) instead of hidden.
- Table view is unchanged (it still hides the power button).
- No API, schema or config change.

## Verification
- `cd frontend && npx tsc --noEmit` — passed.
- `cd frontend && npx vitest run` — passed (13 files, 198 tests); no test covers these components.
- `cd frontend && pnpm build` — passed.
- Not run: browser check, `make test`, `make lint`, `make cross`, embedded UI rebuild. GitNexus impact analysis not run (it failed to load earlier in this repo; see the 2026-11-15 device-status entry); callers were found with grep.
