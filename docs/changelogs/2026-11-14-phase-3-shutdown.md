# 2026-11-14 — Phase 3: signed shutdown from the Controller

| | |
|---|---|
| Author | agent (Claude), Controller side of Phase 3 |
| Scope | `internal/controllerkey`, `internal/agentctl`, `internal/activity`, `internal/devices`, `internal/app`, `internal/api` (handlers, spec), `cmd/homecore`, migration `0005`, docs |
| Requirements | REQ-SHD-001..012 (new), REQ-API-019/021/023/024, REQ-DEV-006/010, REQ-CMD-007/011, REQ-SEC-003/005, REQ-RT-012; ADR-015 |

## Added

- **Controller key** (`internal/controllerkey`): Ed25519 key generated on first start (or by the CLI), seed sealed with the master key (AAD `controller_keys.private_key`), loaded in a transaction that survives concurrent first starts. `GET /api/v1/system/controller-key` and `homecore key show` publish the public half and an `agent.trusted_controllers` snippet.
- **Shutdown Dispatcher** (`internal/agentctl`): signed `POST /agent/v1/commands`, 2 s timeout, one immediate retry with the same `command_id` and a new nonce/`issued_at`, agent error codes passed through, Controller codes `agent_unreachable`, `agent_timeout`, `agent_bad_response`, `agent_error`. Registered in `internal/app`; the command engine is unchanged.
- **`POST /devices/{id}/shutdown`** implemented: `202` + `Command`, idempotent, throttled, `409` without an agent binding, an offline device completes without contacting the agent, `audit_log` `device.shutdown`.
- **Agent status**: `agent_bindings.status|version|last_ok_at` (migration `0005`), written by every dispatcher contact and by a health job (`agentctl.Health`, once a minute, checks the bound `agent_id`). Exposed as `agent.status|version|last_ok_at`.
- **`GET /api/v1/activity`** (`listActivity`, new in the spec): cursor-paged feed over `audit_log` with `category` and `device_id` filters (`internal/activity`).
- **`homecore key show`**.
- Audit action `controller_key.create`.
- Docs: new REQ-SHD table, ADR-015, architecture §6.5a, database `0005`, report §13, roadmap ticks.

## Changed

- `api/openapi.yaml`: new `listActivity` operation and schemas, tag `activity`, description edits for shutdown, `AgentBinding` and the controller key. `make generate` was run; `internal/api/gen/api.gen.go` is regenerated, not hand edited. The generated frontend types are now stale for `listActivity` (the submodule was not touched).
- `devices.AgentBinding` gains `Status`, `Version`, `LastOKAt`; `upsertBinding` resets them only when `agent_id`, port or scheme changes.
- `configs/dev.yaml` comment and `deploy/README.md` mention `homecore key show`.

## Security

- The Controller private key exists in the database only sealed; `controllerkey.Key` never formats its private part; no key material in logs or responses. JWT key, master key and Controller key are three independent values.
- The Agent's verification order and wire format are unchanged (protocol v1).
- The dispatcher's HTTP client uses no proxy, follows no redirects, keeps no connections; a success answer counts only if it names the bound `agent_id` and the command's `command_id`.
- `device.shutdown` is written best effort, after the dispatch (see Known gaps).

## Verification

Run on 2026-11-14 in the working tree:

| Command | Result |
|---------|--------|
| `make test` | pass (all packages) |
| `make test-race` | pass |
| `make lint` | exit 0 (`gofmt` clean, `go vet` clean; `golangci-lint` is not installed, so it was skipped) |
| `make cross` | all 7 targets built (`CGO_ENABLED=0`) |
| `make generate`, then `shasum -c` on `api.gen.go` | unchanged (idempotent) |
| `node .gitnexus/run.cjs analyze --index-only`, then `detect-changes --scope all` | ran; risk reported `critical` because the change touches `cmd/homecore` `run`, `scanDevice`, `Deps` and 91 flows (all expected, additive); `partial`/`truncated` were not reported |

Tests added: `controllerkey` (once-and-sealed, distinct from JWT/master key, wrong master key, tampering, concurrency, single active key, no private formatting); `agentctl` unit tests for the dispatcher (success, `already_*`, every agent code, retry once with a new nonce, 5xx twice, timeout, non-agent bodies, redirects, wrong `agent_id`, status recording) and the health job (ready, unreachable, wrong agent, offline, injected ticks, concurrency); `activity`; `devices.TestAgentContactIsRecordedAndForgottenOnRebind`; `cmd/homecore` key tests; integration tests in `internal/api/shutdown_test.go` (real Controller router plus the real Agent handler with a fake `PowerController`: success, offline device, stale offline, untrusted key, agent down, clock skew, no binding `409`, throttle, timeout, wake/shutdown independence, the key endpoint, an Agent trusting the API's key) and `internal/api/activity_test.go`; `internal/app/app_test.go` runs `app.Run` in `both` mode end to end.

**Smoke test (two processes, real binary):** Controller on `127.0.0.1:18080`, a real Agent (`dry_run: true`) on `19750`, a second Agent trusting another key on `19751`, a Python TCP listener as the device. Observed with `curl` and the logs: key created by `homecore key show` before the first start, its public key placed in the Agent config; shutdown `202` then `acknowledged`, the Agent logged `DRY RUN: would power off this machine` once, the listener was closed and the command became `completed`; a repeat while active returned the same command id and a request in the same second `429`; shutdown of the offline device `completed` with no new Agent log line; Agent stopped: `failed` / `agent_unreachable` in 0.037 s; device without binding `409`; the Agent with another trusted key: `failed` / `unauthorized` naming the `key_id`; `audit_log` rows and `GET /activity` matched; `agent.status` moved `unknown`, `ready`, `unreachable` as expected, and a restarted Agent went from `unreachable` to `ready` through the health job about a minute later.

**Not run / not exercised:** a real power-off; the Linux power adapter (`systemctl` and polkit) and the systemd unit on a real Debian/Ubuntu host (that roadmap item stays unticked); `https` agent bindings; an Agent on another machine; the UI in a browser; `golangci-lint`; the frontend checks (`frontend/` untouched).

## Known gaps / follow-ups

- Open owner decision: the shutdown audit row is best effort, written after the dispatch. Making it blocking (refuse when it cannot be recorded) needs a write-ahead record; see ADR-015 and the roadmap backlog.
- `agent.status` lags up to 60 s after a boot or a bind; `ready` does not prove the Controller key is trusted by that Agent.
- The Controller key cannot be rotated; the health interval is not configurable; `https` agents need a certificate the system trust store accepts.
- The frontend generated types need regenerating for `listActivity` (`npm run gen:api` in the submodule).
- The very first health pass runs at Controller start, before devices created later exist; the next pass covers them.
