# 2026-11-14 — Legacy endpoints: paths and separate Origin

| | |
|---|---|
| Author | agent |
| Scope | `frontend/src/api/legacy.ts`, `legacy.test.ts`, `components/devices/DeviceDetailSheet.tsx`, docs |
| Requirements | REQ-UI-021 (changed) |

## Changed
- `legacyEndpoints(device)` no longer takes an origin; `LegacyEndpoint.url` is now `path` (`/api/turn-on?device=…`).
- Device sheet, Legacy API Endpoints: a new **Origin** field (`window.location.origin`) with its own Copy
  button; the three endpoints show and copy the path only. Copy buttons share a small `CopyButton` and
  `CodeLine` component.

## Verification
- `npm run lint` (tsc --noEmit): clean. `npm test`: 161 passed.
- **Not run**: the rendering and clipboard in a browser.
