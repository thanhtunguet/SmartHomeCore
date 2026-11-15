# 2026-11-15 — Compact header, overlapping sidebar, 2×2 Dashboard cards

| | |
|---|---|
| Author | Claude |
| Scope | `frontend/` (`App.tsx`, `TopBar.tsx`, `Sidebar.tsx`, `AppContext.tsx`, `DashboardScreen.tsx`), `docs/requirements/web-ui.md` |
| Requirements | `REQ-UI-026` added |

## Changed
- Below `lg`, the header's Ambient Display, Add Device and Sync DNS buttons show only their icon (`aria-label` and tooltip added).
- Below `lg`, the sidebar is `fixed` and overlaps the content instead of sharing the flex row; the main pane keeps `ml-16`, the collapsed width. Expanding the sidebar floats it over the content with a backdrop that collapses it on tap. It starts collapsed on such viewports. From `lg` the layout is unchanged.
- Below `lg`, the four Dashboard overview cards are two rows of two (were one row of four from `md`).
- The mobile drawer always renders the expanded sidebar (it would otherwise show the icon rail now that the collapsed state can be the default).

## Verification
- `cd frontend && pnpm lint` — passed.
- `cd frontend && pnpm test` — passed (13 files, 198 tests); no layout test.
- `cd frontend && pnpm build` — passed.
- Not run: browser check at `sm`/`md`/`lg` widths, `make test`, `make lint`, `make cross`, embedded UI rebuild.

## Known gaps / follow-ups
- The `lg` (1024 px) breakpoint for the text/overlay switch is a choice: the request named `md` and `sm`. Move it to `xl` if 1024 px screens feel cramped.
