# 2026-11-26 — Probe by IP, `hostname` deprecated

| | |
|---|---|
| Author | Claude |
| Scope | `internal/devices`, `internal/agentctl` (comment), OpenAPI spec and generated code, frontend generated types, docs |
| Requirements | REQ-MON-010 (new), REQ-API-017, REQ-DEV-002 (unchanged) |

## Why

On the Pi, `MyPC` (a dual-boot PC) was reported offline while `telnet 192.168.100.12 3389` worked.
`Device.ProbeHost()` returned `hostname` when set, and the device had `hostname=tungpt-mint`. That
name exists in the router's DNS only while the PC runs Linux; under Windows the router knows the
machine as `MIAN-Design`, so the probe failed with "host name could not be resolved". The
shutdown agent request and the alert text used the same function and had the same problem.

## Changed

- `Device.ProbeHost()` always returns `IPAddress`. This covers the monitor probe, agent URL
  (`agentctl.agentURL`) and alert text (`alerts/hooks.go`).
- `Device.Hostname` is marked `// Deprecated:`. `hostname` is marked `deprecated: true` (with
  `x-deprecated-reason`) in `Device`, `DeviceCreate` and `DeviceUpdate` in `api/openapi.yaml`.
  The field is still stored, validated (RFC 1123), accepted and returned; nothing connects to it.
  No migration, no behaviour change for API clients.
- Handler lines that still read or write the deprecated field carry `//nolint:staticcheck`.
- Docs: architecture §6.2, database `devices.hostname`/`ip_address`, REQ-MON-010 (new),
  REQ-API-017, roadmap (the "hostname field in the device form" item is dropped; removal of the
  field added to the backlog).

## Tests

- New `devices.TestProbeHostIgnoresTheDeprecatedHostname` and
  `monitoring.TestProbeUsesTheIPNotTheDeprecatedHostname`.
- `agentctl.TestAgentURL` now expects the IP when a hostname is also set;
  `TestDispatchSendsASignedProtocolRequestAndAcknowledges` sets an unresolvable hostname to prove
  the IP is used.

## Verification

- Mutation check: with the old `ProbeHost` temporarily restored, the two new tests and the two
  `agentctl` tests failed; with the change they pass.
- `make test` — all Go packages passed.
- `make generate` and `go vet ./...` (via `make lint`) — passed. `golangci-lint` is not installed, so
  the `//nolint` lines were not exercised.
- `make cross` — passed.
- `pnpm run gen:api` then `pnpm run check:api` — up to date; `pnpm run lint` (`tsc --noEmit`) — passed.
- `pnpm run test` — 238 passed, **1 failed**:
  `endpoints.contract.test.ts > has a dummy call for every exported endpoint function`. It fails
  identically on an untouched `HEAD` (checked with `git stash`), so it predates this change.
- Not run: the new binary on the Pi (not deployed), `make check-generated`, `make ui-build`.

## Known gaps / follow-ups

- Not deployed. The Pi still runs the old binary (`v1.1.1 (ec99ec7)`), so `MyPC` stays offline in
  Windows until the Controller is rebuilt and restarted. Workaround without a deploy:
  `PATCH /devices/{id}` with `"hostname": ""`.
- Remove the deprecated field later (spec, handlers, a migration dropping `devices.hostname`).
- The pre-existing frontend contract test failure above.
