# 2026-11-18: sidebar footer cleanup, "Port Forwarding" renamed to "Tunnel" in the UI

## Changes

- Sidebar footer: the Controller host now truncates (ellipsis, full value in the tooltip) instead of
  overflowing on long domains; the "API Connected/Issue" row was removed (the collapsed-sidebar status
  dot and `StatusBanner` remain).
- User-visible name "Port Forwarding" / "Forward" is now "Tunnel": sidebar, mobile bottom bar, command
  palette, screen heading, empty state, modal title, delete dialog, refresh button label.
- Not renamed: code identifiers, `portforward` tab id, `/api/v1/port-forwards`, OpenAPI schema names.

## Docs

- `docs/requirements/web-ui.md` (REQ-UI-031 wording, last updated).

## Verification

- `npx tsc --noEmit`: pass. `npx vitest run`: 15 files, 239 tests pass.
- Not run: `pnpm build`/lint, browser check.
