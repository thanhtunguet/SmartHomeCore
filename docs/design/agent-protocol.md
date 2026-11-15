# HomeCore Agent Protocol v1

Last reviewed: 2026-11-15 (agent_id may be supplied as an argument, ADR-019; wire format unchanged).

Normative spec for Controller → Agent commands. Implementation:
`internal/agent/protocol` (shared by both roles). Rationale: ADR-001, ADR-005.

## 1. Keys and identities

- **Controller key**: Ed25519 key pair. `key_id` = lowercase hex of the first 8 bytes of
  `SHA-256(public_key)` (16 hex chars). Public keys are exchanged as standard base64 (32 bytes).
- **Agent identity**: `agent_id`, a UUID. Normally chosen by the Controller's UI when the device is bound
  and given to the Agent as `--agent-id` / `HOMECORE_AGENT_ID` / `agent.id` (ADR-019), in which case
  nothing is stored on the Agent; the Agent compares the **canonical lower-case** form. Without one, the
  Agent generates it on first start and persists it in `<agent.data_dir>/agent_id` (ADR-002). Logged at
  startup. It is an address, not a secret: authentication is the signature.
- **Trust**: the Agent's trusted Controller public keys come from `agent.trusted_controllers` and from
  `--trusted-controller` (repeatable). The Agent never holds a private
  key that can issue commands.

## 2. Request

```
POST /agent/v1/commands HTTP/1.1
Content-Type: application/json
X-HomeCore-Key-Id: 3f9a0c1d2b4e5f60
X-HomeCore-Signature: <base64url, no padding, 64-byte Ed25519 signature>

{"v":1,"command_id":"0193…","agent_id":"0193…","action":"shutdown","issued_at":1794430000,"nonce":"Zk3…"}
```

| Field | Type | Rule |
|-------|------|------|
| `v` | int | must be `1` |
| `command_id` | UUID string | Controller command id, unique per command, reused on retries |
| `agent_id` | UUID string | must equal the receiving Agent's id |
| `action` | string | enum: `shutdown`, `reboot`; must be in the Agent's allowlist |
| `issued_at` | int | unix seconds (Controller clock) |
| `nonce` | string | base64url (no padding) of ≥ 16 random bytes; fresh per HTTP attempt |

Body ≤ 4 KiB. Unknown fields are rejected.

### 2.1 Signed message

The signature covers the **exact body bytes** (no JSON canonicalisation), bound to the protocol
version and the key id:

```
message = "homecore-agent-cmd-v1" || 0x0A || key_id || 0x0A || body
```

## 3. Verification (Agent) — in this order

| Step | Check | Failure → HTTP / `error.code` |
|------|-------|-------------------------------|
| 1 | method, content type, size ≤ 4 KiB | 405 / 415 / 413 |
| 2 | `X-HomeCore-Key-Id` is a trusted key | 401 `unauthorized` |
| 3 | Ed25519 signature valid over the message | 401 `unauthorized` |
| 4 | body parses strictly, `v == 1`, all fields valid | 400 `bad_request` |
| 5 | `agent_id` equals own id | 403 `wrong_agent` |
| 6 | `action` in allowlist | 403 `action_not_allowed` |
| 7 | `|agent_now − issued_at| ≤ clock_skew` (default 30 s) | 401 `clock_skew` |
| 8 | `issued_at ≥ agent_start_time` | 401 `stale` |
| 9 | `nonce` not seen within `2 × clock_skew` | 409 `replay` |
| 10 | `command_id` already accepted | 200 `already_accepted` (no re-execution) |
| 11 | a power action is already scheduled (cooldown, default 60 s) | 200 `already_scheduled` |
| 12 | accept: remember nonce + command_id, schedule action after `action_delay` (default 3 s) | **202** `accepted` |

Steps 2–3 return an identical generic body so unauthenticated callers learn nothing. After step 3
the caller is authenticated, so specific error codes are safe and help debugging (notably
`clock_skew` on RTC-less TV boxes).

Step 8 closes the replay gap created by the in-memory nonce cache being lost on restart. Side effect:
during the first seconds after an Agent restart, commands from a Controller whose clock lags may be
rejected as `stale`; the Controller simply retries with a fresh `issued_at`.

Nonce and command caches are bounded (10 000 entries). When full, the Agent **fails closed**
(503 `busy`).

## 4. Responses

Success (`202` or `200`):

```json
{"command_id":"0193…","agent_id":"0193…","status":"accepted","execute_at":"2026-11-11T10:00:03Z","agent_time":"2026-11-11T10:00:00Z"}
```

Error:

```json
{"error":{"code":"clock_skew","message":"issued_at outside allowed window"},"agent_time":"2026-11-11T10:00:00Z"}
```

`agent_time` lets the Controller report clock drift.

## 5. Controller behaviour

1. Build a body with a fresh nonce and `issued_at = now`, sign, POST with a 2 s timeout (LAN; fail fast, ADR-012).
2. `202`/`200` → command `acknowledged`; then wait for the monitor to report `offline`.
3. Network error / 5xx → at most **one** immediate retry, **same `command_id`, new nonce and
   `issued_at`** (idempotent by step 10), then the command is `failed`. No backoff loop: the user
   can simply press the button again.
4. 4xx → command `failed` with the agent's `error.code`.

Implemented by `internal/agentctl` (Phase 3) without any change to the wire format (protocol stays
v1). Controller-side details that the protocol does not constrain:

- The body's `command_id` is the Controller's command id; the retry reuses it. A `202`/`200` is
  trusted only if the body's `agent_id` equals the binding's; otherwise it is treated as a
  non-agent answer (`agent_bad_response`).
- The Controller maps the agent's `error.code` onto the command's `error_code` unchanged when it is
  one listed in §4, and adds its own codes for conditions the Agent cannot report
  (`agent_unreachable`, `agent_timeout`, `agent_bad_response`, `agent_error`); see REQ-SHD-007.
- The HTTP client uses no proxy, follows no redirects and keeps no connections alive.

## 6. Other Agent endpoints

| Endpoint | Auth | Purpose |
|----------|------|---------|
| `GET /agent/v1/healthz` | none | liveness: `{"status":"ok","agent_id":"…","version":"…"}`; may be used as the device `monitor_port` |

The Controller's health job (`agentctl.Health`, once a minute) calls this endpoint for every bound device that is online and accepts the answer only when `status` is `ok` and `agent_id` equals the binding's (ADR-015). It records the answer as `agent.status`, `agent.version` and `agent.last_ok_at` on the binding.

## 7. Future (not in v1)

- Signed acknowledgements: Agent key pair; `X-HomeCore-Agent-Signature` over
  `"homecore-agent-ack-v1\n" || request nonce || "\n" || response body`.
- Signed agent announce (IP change detection, ADR-002).
- Optional mTLS / TLS with pinned certificate.
