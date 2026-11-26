# 2026-11-26 — Inspect an agent from the device form; hostname row in the device sheet

| | |
|---|---|
| Author | Claude |
| Scope | Agent healthz, `agentctl`, OpenAPI + generated code, handlers, frontend (device form, device sheet), docs |
| Requirements | REQ-AG-020, REQ-API-035, REQ-UI-033 (new); REQ-AG-017 context |

## Why

A device is bound to an agent by `agent_id`, and the agent verifies the id in each command. With two
Controllers (the Pi and the Android box) each one generated its own id for the same PC, so the
second Controller's commands were rejected, and nothing in the form told you. The browser cannot
ask the agent (CORS, mixed content), so the Controller does it.

## Added

- Agent: `GET /agent/v1/healthz` also returns `trusted_key_ids` (sorted `key_id`s it trusts;
  additive, older agents omit it). `protocol.Verifier.KeyIDs()`.
- Controller: `agentctl.Inspector` and `POST /api/v1/agents/inspect` (`inspectAgent`). Returns
  `reachable`, `agent_id`, `version`, `trusted_key_ids`, `controller_trusted`, `error`.
  **SSRF limits** (the user chooses the address the Controller connects to): IP literals only,
  private / CGNAT / loopback; link-local (cloud metadata), public, unspecified, multicast and names
  are `400`; one GET to a fixed path, no proxy, no redirects, 2 s, 8 KiB, only parsed fields
  returned, session tokens only (no `x-token-scopes`).
- UI: *Agent at IP:port* panel on the shutdown tab (`AgentDetectPanel`, wording in
  `api/agentInspection.ts`). It looks when it opens and on *Detect*; for a new binding it fills the
  Agent ID with the installed agent's id, for an existing one it only warns about a different id.
  It warns when the agent does not trust this Controller and tells what to add.
- UI: **Hostname** row under *IP Address* in the device sheet's *Network Identity*; `N/A` when none
  is stored. Read-only and informational (the field is deprecated, REQ-MON-010); never sent on save.

## Changed

- `endpoints.contract.test.ts` gained dummy calls for `getOpenAPISettings` and `updateOpenAPISettings`.
  They were missing before this change, which made that test fail on `HEAD`; it passes now.

## Verification

- Go tests added: `agentctl` (`TestCheckTarget`, sanitising, old vs empty key list, non-agent
  answers, redirects, refusal before any I/O), `protocol.TestKeyIDsListsTheTrustedKeysSorted`, and
  through the real router and real agents `api.TestInspectAgent*` (trusted, trusted among others,
  trusts another controller only, trusts nobody, closed port, foreign HTTP service, 9 refused
  targets, 401).
- Frontend: `agentInspection.test.ts`, `mappers.test.ts` (hostname), contract test.
- `make test` — all Go packages passed (30 `ok`, no failures). `make lint` (`go vet`) and `make cross` — passed;
  `golangci-lint` is not installed, so the `//nolint` lines and SA1019 were not exercised.
- `make generate`: regenerated. `make check-generated` reports a diff only because the regenerated
  `internal/api/gen/api.gen.go` is not committed yet.
- Frontend: `pnpm run lint` (`tsc --noEmit`), `pnpm run test` (16 files, 251 tests, 0 failed),
  `pnpm run check:api` (up to date) and `pnpm run build` — passed.
- Not run: the panel and the sheet in a browser, a real second Controller, a deployed agent that
  reports `trusted_key_ids`, `make ui-build` (embedding), GitNexus `impact` (the repo is not indexed).

## Known gaps / follow-ups

- The panel and the hostname row were **not** run in a browser; only types, unit tests and the
  production build cover them.
- No rate limit specific to `/agents/inspect`; the general API limit (600/min) applies.
- The generated agent command still carries this Controller's key only (backlog).
- Deployed agents report `trusted_key_ids` only after they are rebuilt with this change; until then
  the panel shows "does not report which Controllers it trusts".
