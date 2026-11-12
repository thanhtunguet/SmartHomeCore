# 2026-11-12 — Frontend submodule wired to the REST API

| | |
|---|---|
| Author | Claude Code agent session, with the maintainer |
| Scope | `frontend/` (submodule), `web/`, `Makefile`, `.gitignore`, `docs/` |
| Requirements | `REQ-UI-001..016` added ([web-ui.md](../requirements/web-ui.md)); `REQ-API-005` extended (placeholder fallback) |

## Added
- `frontend/src/api/` (client with refresh handling, endpoint wrappers, mappers, wire types, device-meta store), `AuthContext`, login screen, status banner, toasts, `usePolling`.
- Backend-driven flows for wake and shutdown (follow `GET /commands/{id}`), DDNS CRUD + sync, activity from the command history, system metrics, controller key display.
- `make ui-install | ui-build | ui-lint | ui-dev | build-full`; Vite proxy to `HOMECORE_API_URL`.
- `web/placeholder/index.html` fallback and `web/embed_test.go`.
- `frontend/package-lock.json` (new, **uncommitted inside the submodule**).
- [Gap report](../reports/frontend-api-integration.md) (GAP-BE/API/INF, INC-UI), [web-ui.md](../requirements/web-ui.md), ADR-009 and ADR-010, `docs/reports/` in the docs map.

## Changed
- UI rewritten from mock/`localStorage` state to API-backed state (`AppContext`, Dashboard, Devices, Device form/sheet, DDNS, Settings, System, Activity, TopBar, Sidebar).
- Shutdown UI: per-device HMAC secret replaced by Agent ID + port + scheme and the Controller public key (matches ADR-001).
- Form limits and defaults aligned to the OpenAPI spec (MAC optional, timeout, interval, WOL port, DDNS interval).
- `web/static` is now a build output (git-ignored except `.gitkeep`); `web.Static()` falls back to the placeholder.
- Fonts bundled and `assetsInlineLimit: 0` to satisfy the Controller CSP.

## Fixed
- `frontend/package.json` could not be installed (peer conflict); AI Studio leftovers removed (see INC-UI-10).
- Viewport zoom lock removed.

## Security
- UI session tokens live in `localStorage` (ADR-010); no secrets are stored or displayed beyond the Controller public key. The DDNS API token is write-only.

## Removed
- `frontend/src/services/mockData.ts`, `frontend/metadata.json`, unused dependencies, fake Settings fields (HTTP port, bind address, agent secret, …).

## Verification
| Command / check | Result |
|-----------------|--------|
| `cd frontend && npx tsc --noEmit` | Pass |
| `make ui-build` | Pass |
| `make test` | Pass |
| `make lint` | Pass (`golangci-lint` not installed, so only `gofmt` + `go vet` ran) |
| `make cross` | Pass (7 targets, `CGO_ENABLED=0`) |
| `go test ./web ./internal/api` with the UI built and with only the placeholder | Pass both ways |
| Real Controller serving the embedded UI | `GET /`, assets and `/healthz` OK under the CSP with no violations; login returns `501` and the UI explains it |
| Headless Chromium against a throwaway Node mock of the spec (in `/tmp`, not in the repo) | 19/19 checks: login errors, viewer gating, cursor paging, wake completed / timed out, shutdown verified, `409` in the form, device create, DDNS create + sync, activity, metrics, silent refresh, reload keeps session, logout |

**Not run:** UI against the real Controller beyond health/login/`401` (business endpoints are `501`); browsers other than Chromium; Android WebView/TV browsers; plain-HTTP secure-context behaviour on a LAN host; multi-tab refresh race; accessibility audit; `make check-generated` (spec not changed).

## Known gaps / follow-ups
- Controller side is the blocker: Phases 1b, 2, 3, 4 ([roadmap](../plans/roadmap.md)); re-run the UI against it as each lands.
- Full lists with IDs and suggested fixes: [frontend-api-integration.md](../reports/frontend-api-integration.md); backlog entries added to the roadmap.
- **Nothing is committed.** Commit and push the changes in `frontend/` (including `package-lock.json`) first, then `git add` the updated submodule pointer and `web/static/.gitkeep` here.
