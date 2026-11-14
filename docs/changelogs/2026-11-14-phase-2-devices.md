# 2026-11-14 — Phase 2: device management (registry, monitor, wake, commands, retention)

| | |
|---|---|
| Author | Claude Code agent session (delegated implementation of roadmap Phase 2) |
| Scope | `internal/devices`, `internal/monitoring`, `internal/commands`, `internal/retention`, `internal/page`, `internal/ddns` (prune only), `internal/api` (+ `handlers/handlers_devices.go`, `handlers_commands.go`), `internal/app`, `internal/config`, `api/openapi.yaml` + `internal/api/gen`, `README.md`, `docs/` |
| Requirements | New [devices-and-commands.md](../requirements/devices-and-commands.md): `REQ-DEV-001..011`, `REQ-MON-001..009`, `REQ-CMD-001..012`, `REQ-RET-001..002`; `REQ-API-017` Implemented, `REQ-API-019` Partial (wake done); `REQ-RT-004` extended; overview §4.1 and traceability 3.2.2/3.2.5/3.2.8, 3.3.4, 3.4.3..3.4.6, 4.1, 4.2, 6.7 updated |
| Decisions | [ADR-014](../design/decisions.md#adr-014--phase-2-decisions-manual-probes-feed-hysteresis-client-side-wake-many-urgent-probing) |

No migration (the Phase 1 schema plus `0002` already had everything); no new dependency.

## Added
- `internal/devices`: `Service` (validation with every problem listed, normalisation, defaults, keyset cursors), `Repository` + SQLite implementation (LEFT JOIN of `agent_bindings`, case-insensitive unique name, `RecordObservation` in one transaction, `MonitorTargets`, `PruneHistory`).
- `internal/monitoring`: `Monitor` (`Run(ctx)`, tick, due selection, bounded worker pool, jitter, per-device single-flight, hysteresis tracker seeded from the stored status, `ProbeNow`, `Observer` hook, aborted probes not recorded) and `ProbeResult.FailureReason()`.
- `internal/commands`: type-generic `Engine` (idempotent `Request`, `Dispatcher` interface per type, compare-and-set state changes, completion from monitor observations, deadline sweep, restart recovery, `PowerTransitions`, `List`/`Get`), `Repository` + SQLite implementation relying on `commands_one_active`, `WakeDispatcher` (magic packet through `internal/wol`).
- `internal/retention`: job pruning terminal commands, status history and DDNS sync history in batches (`ddns.PruneSyncHistory` added).
- `internal/page`: `limit` bounds and opaque keyset cursors.
- Handlers for `listDevices`, `createDevice`, `getDevice`, `updateDevice`, `deleteDevice`, `removeDeviceAgent`, `probeDevice`, `getDeviceHistory`, `wakeDevice`, `listCommands`, `getCommand`; `audit_log` entries `device.create|update|delete|agent_remove|wake`.
- `internal/app` wires the repositories, monitor, engine and retention as errgroup components (controller mode only); `api.Deps` and `handlers.Handlers` gained `Devices`, `Commands`, `Monitor`, `Audit` (a nil service still answers `501`, so the older server tests keep their meaning).
- Tests: `internal/api/conformance_test.go` (`specChecker`: every response of the implemented operations is validated against `api/openapi.yaml` with kin-openapi `openapi3filter`; `TestSpecCheckerCatchesDrift` shows seven violating responses fail), `internal/api/devices_test.go`, `internal/api/commands_test.go`, plus package tests for devices, monitoring, commands, retention, page and config (see the requirement doc for the names). No new module was needed for the checker (kin-openapi was already a direct dependency).

## Changed
- Spec (`api/openapi.yaml`): `400` added to `listDevices`, `getDeviceHistory`, `listCommands`; `409` added to `updateDevice`; two DDNS descriptions quoted because unquoted commas in flow mappings made `DdnsConfig` an invalid schema for validators. `make generate` rewrote `internal/api/gen/api.gen.go` (it embeds the spec); the generated Go types did not change.
- `internal/config`: `controller.commands.wake_deadline`/`shutdown_deadline` must be > 0 and `retention_days` >= 0 (REQ-RT-004).
- `internal/devices/device.go`, `internal/commands/commands.go`, `internal/monitoring/monitoring.go`: model fields and helpers the new code needs (`Device.Type/OS/LastLatencyMs/Agent`, `Command`, `NewTrackerFrom`).
- Monitor deviation from architecture §6.3, now documented there: the tracker is seeded from the stored status (not `unknown`) and the commands engine is notified through an `Observer` interface instead of an `Observe(id) <-chan Status` method.
- Timestamps created by the monitor and the engine are truncated to milliseconds, so what a response shows equals what the database stores (found in the smoke test: a fresh wake response carried microseconds, the list carried milliseconds).
- `README.md` status paragraph.

## Fixed
- Found by the spec checker while writing the tests: two DDNS description strings (see Changed).
- `limit=0` was silently treated as the default; it is now `400` (the spec says `minimum: 1`).

## Security
- Wake never takes arguments from the request beyond the device id; the packet target comes from the stored device. No secrets in device responses or audit details; a wake rejected by the throttle, with `409`, `400` or `404` writes no audit entry and no command.
- Unknown JSON fields, trailing data and non-object bodies are rejected on create/update (`400`).

## Verification
Run on 2026-11-14 (macOS, Go 1.27.2), after the last code change unless noted:

| Command | Result |
|---|---|
| `make test` | pass, all packages |
| `make test-race` | pass, all packages |
| `go test -race -count=5 ./internal/monitoring/ ./internal/commands/ ./internal/retention/` | pass |
| `go test -race -count=3 ./internal/api/` | pass (the API tests contain two real 1.1 s waits for the throttle window) |
| `make lint` | `go vet` and the gofmt check pass; `golangci-lint` is not installed, skipped |
| `make cross` | 7 targets built (linux amd64/arm64/arm, windows/amd64, darwin arm64/amd64, android/arm64) |
| `make generate` + `git diff --exit-code internal/api/gen` | **differs from the last commit, as expected**: the spec changed (see Changed), so the embedded spec in `api.gen.go` changed (86 lines). Running `make generate` a second time is byte-identical |
| `node .gitnexus/run.cjs analyze --index-only`, then `detect-changes --scope all --repo .` | index rebuilt; risk **critical**, 19 files, 129 symbols, 22 affected processes. Expected: the composition root (`runController`), `api.Deps`/`Handlers`, config `Validate` and the new device/command flows changed. The per-symbol `impact` command was **not** run before each edit |
| Smoke test, built binary, temp data dir `/tmp/hc-smoke`, port 55447, tick 1 s, `wake_deadline` 12 s, `curl`, a Python TCP listener on 55501 as the device | see below |

Smoke test results (real Controller, `homecore user add` for the account, login with `curl`):
- Create (`201`), duplicate name ignoring case (`409`), unknown field (`400`, my first request used the wrong field name `type` instead of `device_type` and the server rejected it), delete (`204`, then `404`).
- With nothing listening the monitor wrote `unknown -> offline` after two probes (history reason `probe`); `POST /probe` answered `reachable: false, status: offline`.
- Wake: `202`, command `sent`, `power_transition: waking`; second wake in the same second `429` with `Retry-After: 1`; after 1 s the **same** command id; starting the listener completed the command and set the device `online` within one tick (history `offline -> online`); a wake of the online device returned a new command already `completed`; no MAC `409`; listener stopped: device `offline` after about 10 s, wake then `timed_out` about 1 s after `deadline_at`.
- `kill -9` with a `sent` wake, restart: the command was still `sent`, the device `offline`/`waking`, then `timed_out` at the deadline.
- `PATCH` clear with `""` (description, broadcast address, MAC); agent binding set with defaults (port 9750, `http`), replaced (9800, `https`); `DELETE /agent` `204` three times, `404` for an unknown device; list pagination of 4 devices with `limit=2` and the cursor; `status=nope`, `limit=0` `400`; unknown command `404`; `shutdown` still `501`.
- Deleting a device removed its commands, history and binding (SQL counts 0) and wrote `device.delete`; `audit_log` held `device.create` 5, `device.update` 5, `device.wake` 4, `device.agent_remove` 3, `device.delete` 2.
- A `POST /devices` body built as the frontend's `toDeviceCreate` builds it returned `201` with every field `toDevice` reads (compared with `frontend/src/api/mappers.ts`, read-only; no mismatch found in the Device shape). The UI itself was not run in a browser.
- `GAP-API-01/04/08` verified and flipped, `GAP-API-10` verified for wake only (report §12).
- Controller logs: no `WARN`/`ERROR` lines in three server runs; SIGTERM stops cleanly.
- The smoke test ran on the build made **before** the millisecond-truncation change; after it, a second smoke run (restart scenario and the frontend-shape request) used the rebuilt binary and the timestamps were uniformly millisecond. The first scenarios were not repeated on the final binary; the unit and API tests were.

Not run: `golangci-lint` (not installed); the Android and Windows binaries (cross-compiled only); the UI against the real Controller in a browser; a real Wake-on-LAN packet to a sleeping machine (only a loopback UDP receiver in `commands.TestWakeDispatcherSendsARealMagicPacket`); load or soak tests.

## Known gaps / follow-ups
- `frontend/src/api/generated` is stale by the spec edits (no behaviour change, `check:api` will report drift); regenerate it in the submodule.
- `agent.status/version/last_ok_at` and the shutdown dispatcher are Phase 3.
- The monitor's failure counters are in memory only.
- The deadline sweep runs every 5 s, so `timed_out` can lag `deadline_at` by up to 5 s.
- The `ddns_sync_history` prune SQL is untested until Phase 4 fills the table.
