# 2026-11-23 — OpenAPI enable switch

| | |
|---|---|
| Author | Codex |
| Scope | OpenAPI settings API, SQLite, API docs routes, frontend Settings |
| Requirements | REQ-API-034, REQ-ST-008, REQ-UI-032 |

## Added

- Settings can enable or disable the OpenAPI document and Swagger UI.
- Added `/api/swagger.json` as an alias for the existing `/api/openapi.json` endpoint.
- Applied a shared per-client-IP limit of 2 requests per second to the docs index and spec endpoints; static UI assets remain loadable and use the setting gate.
- Added migration `0010_openapi_setting.sql`; the setting starts disabled.

## Changed

- `/api/docs`, `/api/openapi.json`, and `/api/swagger.json` now depend on the persisted database setting. The legacy `controller.api_docs` YAML key remains accepted for existing configs but is ignored.
- Settings now includes an OpenAPI switch and links when enabled.

## Verification

- `make generate` — passed.
- `pnpm gen:api` — passed.
- `go build ./...`, `make cross`, and `make lint` — passed (`golangci-lint` was unavailable and skipped by the Makefile).
- `pnpm build`, `pnpm lint`, `pnpm check:api`, and `git diff --check` — passed; Vite printed its existing native-config-loader warning.
- `make check-generated` — reports the expected diff in `internal/api/gen/api.gen.go` after regenerating the changed spec; generated output was committed to the working tree.
- Go tests, frontend tests, and runtime/browser checks were not run.

## Known gaps / follow-ups

- The API docs enable switch and rate limit do not yet have recorded automated or runtime verification.
