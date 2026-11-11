# Requirements: Secure Shutdown Agent

| | |
|---|---|
| Brief | §3.5, §6 (agent zone, least privilege, acknowledge-before-execute), §8.1, §8.5 |
| Code | `internal/agent`, `internal/agent/protocol`, `internal/platform`, `deploy/systemd/homecore-agent.service`, `deploy/polkit/50-homecore-agent.rules`, `cmd/devtools` |
| Design | [agent-protocol.md](../design/agent-protocol.md) (normative), [ADR-001](../design/decisions.md#adr-001--agent-command-authentication-ed25519-signatures), [ADR-005](../design/decisions.md#adr-005--multiple-controllers) |
| Status | **Implemented** (Agent side). Controller-side signing client and key management are planned |

## Functional requirements

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-AG-001 | The Agent runs its own listener (default `:9750`) with exactly two endpoints: `POST /agent/v1/commands` and the unauthenticated `GET /agent/v1/healthz` (`status`, `agent_id`, `version`). | Implemented | `agent/server_test.go`; smoke test |
| REQ-AG-002 | Commands are authenticated with **Ed25519**. The Agent stores only public keys of trusted Controllers (`agent.trusted_controllers[].public_key`, base64 of 32 bytes). `key_id` = hex of the first 8 bytes of SHA-256(public key). An invalid key stops startup; no keys = warning, every command rejected. | Implemented | `TestVerifyAcceptsValidCommand`; startup validation by smoke test |
| REQ-AG-003 | The command body is JSON `{v, command_id, agent_id, action, issued_at, nonce}`: `v = 1`, UUIDs, `issued_at` in unix seconds, nonce base64url of ≥ 16 random bytes. The body is at most 4 KiB, unknown fields and trailing data are rejected. The signature covers `"homecore-agent-cmd-v1\n" + key_id + "\n" + body` exactly, so no canonicalisation is needed. | Implemented | `TestVerifyRejectsUnknownJSONFields`, `TestVerifyRejections/short_nonce` |
| REQ-AG-004 | The signature is checked **before** the body is parsed. An unknown key id and a bad signature give the identical response `401 unauthorized`, so callers learn nothing. | Implemented | `TestVerifyRejections` (unknown key, untrusted signer, tampered body), `TestUnsignedRequestRejected` |
| REQ-AG-005 | Only allowlisted actions run (`agent.allowed_actions`, default `[shutdown]`, optional `reboot`). Unknown actions → `400 bad_request`; known but not allowed → `403 action_not_allowed`. The Agent never runs a shell or takes arguments from requests. | Implemented | `TestVerifyRejections` (action not allowed, unknown action) |
| REQ-AG-006 | Freshness: `|agent_now − issued_at| ≤ clock_skew` (default 30 s, configurable 5 s–5 m), otherwise `401 clock_skew`. Every response includes `agent_time` so drift can be diagnosed. | Implemented | `TestVerifyRejections` (too old, from the future) |
| REQ-AG-007 | Each nonce is single-use for 2 × `clock_skew`; reuse → `409 replay`. The nonce cache holds at most 10 000 entries and **fails closed** (`503 busy`) when full. | Implemented | `TestVerifyRejectsReplay`, `TestExpiringSetFailsClosedWhenFull` |
| REQ-AG-008 | Idempotency: a `command_id` already accepted in the last 10 minutes returns `200 already_accepted` and does not run again (Controller retries reuse `command_id` with a fresh nonce). While a power action is scheduled or in its cooldown (default 60 s), other commands get `200 already_scheduled`. | Implemented | `TestShutdownIsAcknowledgedThenExecutedOnceAndIdempotent` |
| REQ-AG-009 | Acknowledge before executing: an accepted command returns `202 accepted` with `execute_at`, and the action runs after `action_delay` (default 3 s). If the Agent stops before then, the pending action is cancelled and logged. | Implemented | `TestShutdownIsAcknowledgedThenExecutedOnceAndIdempotent` (cancel path untested) |
| REQ-AG-010 | Addressing: the Agent has a persistent `agent_id` (UUIDv7 in `<agent.data_dir>/agent_id`, mode `0600`, created on first start and logged). A command for another `agent_id` → `403 wrong_agent`. | Implemented | `TestVerifyRejections/wrong_agent` |
| REQ-AG-011 | Least privilege: on Linux the action is `systemctl poweroff|reboot --no-wall` with fixed arguments, run as the unprivileged user `homecore-agent`. Authorisation comes from a polkit rule allowing only the four `org.freedesktop.login1.power-off*/reboot*` actions to that user, under a hardened systemd unit. Other platforms, or `dry_run: true`, only log the action. | Implemented (untested on a host) | Code review; macOS dry-run smoke test |
| REQ-AG-012 | Transport: optional TLS (`agent.tls`). The listener is meant for the LAN only; remote access goes through a VPN (documented in config comments and `deploy/README.md`). | Implemented (TLS untested) | — |
| REQ-AG-013 | Replay across restarts: commands with `issued_at` earlier than the Agent's start time → `401 stale`, so losing the in-memory nonce cache on restart opens no window. | Implemented | `TestVerifyRejections/issued_before_agent_start` |
| REQ-AG-014 | Every rejected command is logged with remote address, error code and reason; every processed command with `command_id`, action, outcome and `execute_at`. | Implemented | Smoke-test log |

## Developer tooling

`cmd/devtools` provides `keygen` (Controller key pair: seed file `0600`, prints `key_id` and public
key) and `send` (builds, signs and posts a command; `-command-id` reuse tests idempotency).

## Known gaps

- The accepted-`command_id` map is pruned after 10 minutes but has no size cap, unlike the "bounded
  caches" statement in the protocol spec. Only authenticated callers can add entries, so the risk is
  low; tracked in the roadmap backlog.
- No Controller-side dispatcher, signing-key storage (`controller_keys`), or
  `GET /api/v1/system/controller-key` yet.
- Signed acknowledgements (protocol v2) are not implemented.
- No Windows power adapter (falls back to dry-run).
- The systemd unit and polkit rule have not been validated on a real Linux host.
