# 2026-11-14 — System disk path tooltip

| | |
|---|---|
| Author | Codex |
| Scope | `frontend/`, `docs/requirements/web-ui.md` |
| Requirements | `REQ-UI-020` added |

## Fixed
- Keep the disk filesystem path in the label's native tooltip instead of displaying it inline; retain usage percentage and used/total values.

## Verification
- `cd frontend && npm test` — passed (8 files, 154 tests), including a static-render regression test.
- `cd frontend && npm run lint` — passed.
- `cd frontend && npm run build` — passed; Vite emitted an existing configuration-loader warning.
- Not run: browser or mobile tooltip/layout verification, `make test`, `make lint`, `make cross`, or embedded UI rebuild.
