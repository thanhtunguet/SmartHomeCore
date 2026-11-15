# Requirements: MCP server for AI agents

| | |
|---|---|
| Brief | Not in `BRIEF.md`; added by request (AI agents turning machines on and off) |
| Code | `internal/mcp` (protocol), `internal/api/handlers/handlers_mcp.go` (tools), `api/openapi.yaml` (`POST /api/v1/mcp`) |
| Design | [architecture §10.3](../design/architecture.md), [ADR-020](../design/decisions.md#adr-020--the-controller-hosts-the-mcp-server-the-service-account-key-is-the-only-credential), keys: [ADR-018](../design/decisions.md#adr-018--service-account-keys-server-generated-ed25519-keys-scoped-individually-revocable-and-self-rotating) |
| Last updated | 2026-11-15 (new) |
| Status | **Implemented**; smoke-tested with `curl` against the real binary, **not yet with a real MCP client** |

The Controller is itself an MCP server (Model Context Protocol) at `POST /api/v1/mcp`. An agent
authenticates with a **service account key** and may call three tools. There is no permission layer:
a valid key may use every tool. If permissions are ever needed they belong to the key.

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-MCP-001 | **Transport.** Stateless Streamable HTTP without SSE: `POST /api/v1/mcp` carries one JSON-RPC 2.0 message. A request is answered `200 application/json` (a result, or a JSON-RPC error for an unknown method, unknown tool or bad params). A notification or a client response is `202` with no body. A body that is not valid JSON, is a batch, has the wrong `jsonrpc`, a `null`/non-scalar `id` or no `method` is `400` with a JSON-RPC error object. `GET`, `DELETE` and `PUT` are `405`. No session id is issued. Answers are `Cache-Control: no-store`. | Implemented | `mcp.TestProtocolErrors`, `mcp.TestNotificationsAndResponsesAreAcknowledgedSilently`, `mcp.TestIDIsEchoedExactly`, `TestMCPNeedsAServiceAccountKey`, `TestMCPHandshakeAndToolCatalogue` |
| REQ-MCP-002 | **Methods.** `initialize` negotiates the protocol version (`2025-11-25`, `2025-06-18`, `2025-03-26`; an unknown or missing version gets the newest), reports `capabilities.tools`, `serverInfo` (`homecore`) and `instructions`. `ping` answers `{}`. `tools/list` lists the tools with `inputSchema` and annotations (`readOnlyHint`; `destructiveHint`/`idempotentHint` for `control_device`). `tools/call` runs one tool; arguments may be absent (`{}`). | Implemented | `mcp.TestInitializeNegotiatesTheProtocolVersion`, `mcp.TestToolsListAndCall`, `TestMCPHandshakeAndToolCatalogue` |
| REQ-MCP-003 | **Authentication is the service account key, nothing more.** The bearer must be a valid key (REQ-AUTH-021..023): anonymous or garbage is `401`; a revoked, expired or owner-disabled key is `401` at once. Any scope is accepted (`x-token-scopes: [legacy, mcp]`), and there is no per-tool permission. A valid **session** token or HS256 legacy token is refused with `403 forbidden`: the endpoint is for keys. | Implemented | `TestMCPNeedsAServiceAccountKey`, `TestPolicyMarksTheLegacyOperationsOnly` |
| REQ-MCP-004 | **Tools.** Exactly `list_devices`, `get_devices` and `control_device`, in that order. | Implemented | `TestMCPHandshakeAndToolCatalogue` |
| REQ-MCP-005 | **`list_devices`** (no arguments) returns `{count, devices[]}`, devices ordered by name, each with `id`, `name`, `description`, `device_type`, `os`, `enabled`, `status` (`online`/`offline`/`unknown`), `power_transition` (`none`/`waking`/`shutting_down`), `last_seen_at`, `last_checked_at`, `can_turn_on` (a MAC address is set), `can_turn_off` (an agent is bound). MAC, IP address, ports and the agent binding are not included. At most 500 devices. The result is both the text content (JSON) and `structuredContent`. | Implemented | `TestMCPListAndGetDevices` |
| REQ-MCP-006 | **`get_devices`** takes `device` (id or name, case-insensitive) and returns `{device}` in the same shape. An unknown or empty reference is a tool error that points to `list_devices`. | Implemented | `TestMCPListAndGetDevices` |
| REQ-MCP-007 | **`control_device`** takes `device` and `action` ∈ {`turn_on`, `turn_off`, `check_status`}; an unknown action is a tool error. | Implemented | `TestMCPControlDeviceErrors` |
| REQ-MCP-008 | `action: turn_on` does what `POST /devices/{id}/wake` does (Wake-on-LAN, idempotent, never waits) and returns `{device, action, command{id,type,status}, note}`. A device already online gives a command born `completed`, no packet, and a note saying so. A missing MAC address is a tool error. | Implemented | `TestMCPTurnOnSendsWakeOnLANOnceAndAuditsTheKey`, `TestMCPTurnOnOfAnOnlineDeviceDoesNothing`, `TestMCPControlDeviceErrors` |
| REQ-MCP-009 | `action: turn_off` does what `POST /devices/{id}/shutdown` does (signed command to the device's agent, idempotent, never waits) and returns the same shape. No agent binding is a tool error. | Implemented | `TestMCPTurnOffReachesTheAgent`, `TestMCPControlDeviceErrors` |
| REQ-MCP-010 | `action: check_status` probes the device **now** (like `POST /devices/{id}/probe`, recorded as any observation, reason `manual probe`), then returns `{device, reachable}` with the updated status. Without a monitor it returns the stored status. Not throttled. | Implemented | `TestMCPCheckStatusProbesNow`, `TestMCPTurnOffReachesTheAgent` |
| REQ-MCP-011 | **Throttle.** `turn_on`/`turn_off` share the one-request-per-device-per-second slot with REST wake/shutdown and the legacy endpoints (REQ-API-018/027), however the device is spelled. A refused request is a tool error that says how long to wait; it sends nothing. | Implemented | `TestMCPPowerRequestsAreThrottledPerDevice` |
| REQ-MCP-012 | **Audit.** `device.wake`/`device.shutdown` entries made through MCP are attributed to the key's owner, carry `via: mcp` and `service_key_id`, and never the key. | Implemented | `TestMCPTurnOnSendsWakeOnLANOnceAndAuditsTheKey`, `TestMCPTurnOffReachesTheAgent`; smoke test 2026-11-15 (key not in the log) |
| REQ-MCP-013 | **Errors.** Problems an agent can fix (unknown device or action, no MAC, no agent, throttled, bad arguments) are tool results with `isError: true` and a plain sentence. Unexpected failures are logged and reported only as `internal error`. | Implemented | `TestMCPListAndGetDevices`, `TestMCPControlDeviceErrors`, `TestMCPPowerRequestsAreThrottledPerDevice` |

## Known gaps

- **No permission model.** Every valid key can turn every device on and off, including a key with scope
  `legacy`, which no longer confines it to the three legacy URLs. Per-key permissions (a scope or per-device
  rules) are not designed; ADR-020 says they belong to the key.
- The `mcp` scope of ADR-018 and its REST allowlist (`x-token-scopes: [mcp]` on 12 operations) are now redundant
  for MCP. Removing the scope needs a migration (`service_account_keys.scope` has a CHECK). Not done.
- No SSE, no sessions, no server-to-client messages, no resumability, no batches (the 2025-03-26 spec allowed
  them; later versions do not). No `resources` or `prompts`.
- Not tested with a real MCP client (Claude Desktop, Claude Code, Inspector); only with `curl` and the Go
  tests. The `Origin` header is not validated (the endpoint needs a bearer key).
- OAuth discovery (`/.well-known/oauth-protected-resource`) is not offered: clients must be given the key as a
  static `Authorization` header.
