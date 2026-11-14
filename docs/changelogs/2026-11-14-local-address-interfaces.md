# 2026-11-14 — Local address interface names

| | |
|---|---|
| Author | Codex |
| Scope | System metrics collection and API, frontend System screen |
| Requirements | `REQ-SYS-001`, `REQ-API-025`, `REQ-UI-021` |

## Changed
- Return interface/IP pairs in optional `local_addresses` while preserving the existing `local_ips` response; render each interface and IP in separate columns. Older Controllers' IPs remain visible with an unknown interface marker.
- Regenerated the Go and frontend API types from the OpenAPI spec.

## Verification
- `make generate`, `cd frontend && npm run gen:api` — passed.
- `make ui-check` — passed: type-check, 157 frontend tests and generated API check.
- `go test ./internal/system ./internal/api/handlers ./internal/api` — passed; focused collection, mapping, rendering and API tests added.
- Browser layout and a live Controller's interface display were not tested.
- `go test ./internal/api -run '^TestSystemLocalAddressesOverHTTP$'`, `make test`, `make lint`, `make cross` — passed; `golangci-lint` was unavailable and skipped by `make lint`.
- `cd frontend && npm run build` — passed; the embedded UI was not rebuilt.
