# 2026-11-14 — Responsive Activity header

| | |
|---|---|
| Author | Codex |
| Scope | `frontend/`, `docs/requirements/web-ui.md` |
| Requirements | `REQ-UI-022` added |

## Fixed
- Keep Activity action labels on one line and move the action group below the title when space is insufficient; allow the buttons themselves to wrap on very narrow screens.

## Verification
- `cd frontend && npm run lint` — passed.
- `cd frontend && npm test` — passed (9 files, 157 tests); no responsive layout test.
- `cd frontend && npm run build` — passed; Vite emitted an existing configuration-loader warning.
- Not run: responsive browser verification, `make test`, `make lint`, `make cross`, or embedded UI rebuild.
