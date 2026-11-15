# 2026-11-15 — MCP server in the Controller

| | |
|---|---|
| Author | agent |
| Scope | `api/openapi.yaml`, `internal/mcp` (new), `internal/api/handlers/handlers_mcp.go` (new), `internal/api/gen`, docs |
| Requirements | REQ-MCP-001..013 (new, [mcp.md](../requirements/mcp.md)); REQ-API-029/030 context; ADR-020 |

## Added
- **`POST /api/v1/mcp`**: the Controller is an MCP server for AI agents. Stateless Streamable HTTP, JSON only
  (no SSE, no sessions); one JSON-RPC 2.0 message per request; `initialize` (versions `2025-11-25`,
  `2025-06-18`, `2025-03-26`), `ping`, `tools/list`, `tools/call`; notifications get `202`.
- **`internal/mcp`**: the protocol layer, standard library only, no knowledge of devices or auth.
- **Tools** (`handlers_mcp.go`): `list_devices`; `get_devices` (one device, by id or name); `control_device`
  with `action` = `turn_on` (Wake-on-LAN), `turn_off` (signed shutdown through the agent), `check_status`
  (probe now). They reuse `requestWake`, `requestShutdown`, `Devices.Resolve`, `Monitor.ProbeNow` and the shared
  per-device throttle, so idempotency, the 1 s limit and the audit trail are the REST ones (`via: mcp`,
  `service_key_id`, actor = the key's owner).
- Authentication is a **service account key of any scope** and nothing else: no per-tool permission. A session or
  legacy HS256 token gets `403`.
- ADR-020 (partly supersedes ADR-018), `docs/requirements/mcp.md`, architecture §10.3, roadmap section.

## Changed
- The statements that the MCP server is "a separate project" were removed from the requirements and the
  roadmap (ADR-018 itself is not rewritten; it carries a pointer to ADR-020).
- `TestPolicyMarksTheLegacyOperationsOnly` now expects `POST /api/v1/mcp` next to the three legacy operations
  and rotation, because the endpoint lists both key scopes.

## Decisions to review
- **Any key, any scope.** A `legacy` key can now wake and shut down devices through MCP; before, it was confined to
  the three legacy URLs. This is what "no permissions, the key is the permission" means; per-key permissions are
  not designed.
- **The `mcp` scope and its REST allowlist were left in place.** They are redundant now, but the scope has a
  `CHECK` in migration `0007`, so removing it needs a migration and could break existing keys. Say if you want it.
- **`get_devices`** keeps the plural name from the request although it returns one device (`device` = id or name).
- **`check_status` probes** (a TCP connect, recorded like `POST /probe`) instead of reading the stored status, so
  an agent can follow up on `turn_on`/`turn_off` without waiting for the monitor's tick.
- No MCP SDK dependency (ADR-020).

## Verification
- `go test ./... -count=1`: all packages pass. New: `internal/mcp` (5 tests: negotiation, id echo, tools list/call,
  protocol errors, notifications) and `internal/api/mcp_test.go` (9 tests: key required and any scope, session and
  legacy tokens refused, revoked key, `GET` is 405, handshake, list/get, turn_on once + audit, throttle shared
  with REST, errors, check_status probes, turn_off reaches a real agent, online device does nothing). Every
  response through the router is also validated against `api/openapi.yaml`.
- `make lint`: `go vet` and the gofmt check clean; `golangci-lint` is not installed (skipped).
- `make cross`: all 7 targets build with `CGO_ENABLED=0`.
- `make generate` was run; `internal/api/gen/api.gen.go` is regenerated.
- **Smoke test with the real binary** (temporary data dir, key created through the API): anonymous `401`, session
  token `403`, `initialize`, `notifications/initialized` `202`, `list_devices`, `check_status` (online, probed),
  `turn_on` (command `completed`: the device was already online), a second `turn_on` in the same second refused
  as a tool error, `GET` `405`; the key does not appear in the log.

## Not run
- No real MCP client (Claude Code, Claude Desktop, MCP Inspector) was connected; only `curl` and Go tests.
- `make check-generated` reports a diff only because the regenerated file is not committed yet (it compares with
  `HEAD`); `make generate` itself succeeds.
- Frontend: `make ui-check-api` / `ui-check` not run; the submodule's generated types are not regenerated and
  will be stale until its owner does (REQ-API: spec change MCP server).
- GitNexus impact and `detect-changes` could not run: its native library fails to load on this machine
  (`libssl.3.dylib` missing), so the pre-edit impact check is not recorded. No existing function was edited; the
  only edits to existing code are the generated file, a test expectation and docs.

## Known gaps / follow-ups
See [mcp.md](../requirements/mcp.md#known-gaps): no permission model, redundant `mcp` scope, no SSE/sessions,
no OAuth discovery, no `Origin` check, not tried with a real client.
