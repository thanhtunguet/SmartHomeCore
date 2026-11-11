# HomeCore Agent Protocol v1

Normative spec for Controller → Agent commands. Implementation:
`internal/agent/protocol` (shared by both roles). Rationale: ADR-001, ADR-005.

## 1. Keys and identities

- **Controller key**: Ed25519 key pair. `key_id` = lowercase hex of the first 8 bytes of
  `SHA-256(public_key)` (16 hex chars). Public keys are exchanged as standard base64 (32 bytes).
- **Agent identity**: `agent_id`, a UUID generated on the Agent's first start and persisted in
  `<agent.data_dir>/agent_id`. Printed at startup; entered on the Controller when binding a device.
- **Trust**: the Agent config lists trusted Controller public keys. The Agent never holds a private
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

1. Build a body with a fresh nonce and `issued_at = now`, sign, POST with a 5 s timeout.
2. `202`/`200` → command `acknowledged`; then wait for the monitor to report `offline`.
3. Network error / 5xx → retry up to 3 times with backoff, **same `command_id`, new nonce and
   `issued_at`** (idempotent by step 10).
4. 4xx → command `failed` with the agent's `error.code`.

## 6. Other Agent endpoints

| Endpoint | Auth | Purpose |
|----------|------|---------|
| `GET /agent/v1/healthz` | none | liveness: `{"status":"ok","agent_id":"…","version":"…"}`; may be used as the device `monitor_port` |

## 7. Future (not in v1)

- Signed acknowledgements: Agent key pair; `X-HomeCore-Agent-Signature` over
  `"homecore-agent-ack-v1\n" || request nonce || "\n" || response body`.
- Signed agent announce (IP change detection, ADR-002).
- Optional mTLS / TLS with pinned certificate.
