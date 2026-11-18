# Requirements: Port forwarding

| | |
|---|---|
| Brief | not in the brief: added at the owner's request (ADR-022) |
| Code | `internal/portforward` (`portforward.go`, `repo.go`, `repo_sqlite.go`, `service.go`, `runner.go`), `internal/api/handlers/handlers_portforward.go`, `internal/storage/migrations/0009_port_forward_rules.sql`, `internal/app/app.go`, `internal/activity/activity.go` (messages); UI `frontend/src/components/portforward/` and `frontend/src/api/portForward.ts` |
| Design | [architecture §6.13](../design/architecture.md), [database.md](../design/database.md) (`port_forward_rules`), [ADR-022](../design/decisions.md#adr-022--port-forwarding-a-userspace-relay-driven-by-stored-rules) |
| Last updated | 2026-11-18 |
| Status | **Implemented**, verified by unit and integration tests on real loopback sockets (`-race`), tests through the real router, and a smoke test with the built binary (restart persistence). **Not** verified on Linux, with IPv6 or in a browser (see Known gaps) |

A **rule** makes the Controller listen on `listen_address:listen_port` and relay every TCP
connection and/or UDP datagram to `target_host:target_port`. The relay runs inside the Controller
process (no `iptables`, no extra daemon). A rule has a name, a connection type, a local interface and
a stored on/off switch. Tests named here are in `internal/portforward` and `internal/api`
(`portforward_test.go`; every response is checked against `api/openapi.yaml`).

## Rules and listeners

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-PF-001 | **Rule.** A rule has a `name` (1..100 characters, no control characters, unique ignoring case: `409`), a `protocol`, a `listen_address`, a `listen_port` and a `target_host`/`target_port` (ports 1..65535), and `enabled` (default `true`). `target_host` is an IP literal or a host name (RFC 1123 labels, `_` allowed; stored lower case). It is resolved each time a connection is opened, so a DHCP or DNS change needs no edit. All problems of one request are returned together as one `400`. | Implemented | `portforward.TestCreateAndUpdateValidation`, `TestCleanTargetHost`, `TestDefaultsAreWildcardAndEnabled`, `TestNamesAreUniqueIgnoringCase`, `api.TestPortForwardErrors` |
| REQ-PF-002 | **Connection type.** `tcp`, `udp` or `universal` (TCP **and** UDP on the same port number, to the same target port). A `universal` rule binds both sockets or neither: if one bind fails, the other is closed and the rule is in error. | Implemented | `portforward.TestUniversalRelaysTCPAndUDPOnOnePort`, `TestAUniversalRuleThatHalfBindsLeavesNothingOpen` |
| REQ-PF-003 | **Local interface.** `listen_address` is an IP literal and defaults to `0.0.0.0`. An IPv4 address binds IPv4 only (so `0.0.0.0` does not also listen on IPv6), `::` binds dual stack, another IPv6 address binds IPv6 only. IPv4-mapped IPv6 is stored as IPv4; a zone (`%eth0`) and multicast addresses are refused. The address is **not** checked against the host's interfaces when the rule is saved (an address that is down at boot must not make a rule unsavable): the bind decides, and a failure is the rule's `error` status (REQ-PF-007). The UI offers the host's addresses from `GET /system/metrics` (`local_addresses`, interface name and IP). | Implemented | `portforward.TestCleanListenAddress`, `TestNetworks`, `api.TestPortForwardCRUDAndSwitchOverHTTP` (default address) |
| REQ-PF-004 | **TCP relay.** Each accepted connection is paired with a new connection to the target (5 s connect timeout, keep-alive 30 s) and bytes are copied in both directions. When one side finishes writing, the other side is half-closed (`CloseWrite`) so the reply still arrives. A target that cannot be reached closes the client's connection. At most 512 relayed connections per rule; more are closed at once. | Implemented | `portforward.TestTCPRelayCarriesBothDirectionsAndHalfClose`, `TestTCPRelayToADeadTargetClosesTheClient`; the 512 limit **Implemented (untested)** |
| REQ-PF-005 | **UDP relay.** Each client address gets its own upstream socket to the target, so replies find their way back through the listening socket; datagrams up to 64 KiB. A client with no traffic in either direction for 60 s is forgotten (its socket is closed). At most 256 clients per rule; datagrams of more are dropped. | Implemented (relay and one session per client tested; the 60 s idle timeout and the 256 limit are untested) | `portforward.TestUDPRelayKeepsClientsApart`, `TestUDPClientsGetOneSessionEach` |

## State: the switch, restart and retry

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-PF-006 | **The switch is stored.** `enabled` is the rule's desired state and lives in `port_forward_rules.enabled`. `PATCH /port-forwards/{id}` with `{"enabled": false}` closes the rule's listeners **and** its established connections; `true` opens them. Nothing else about the rule changes. | Implemented | `portforward.TestSwitchOffClosesTheListenerAndOnReopensIt`, `api.TestPortForwardCRUDAndSwitchOverHTTP` |
| REQ-PF-007 | **Restart and retry.** When the Controller starts, `Service.Run` reads every rule and starts the enabled ones; disabled rules stay closed. A rule that is enabled but cannot listen (port in use, address not on the host, no privilege) never stops the Controller: it stays `enabled`, shows `status: error` with the reason in `last_error`, and is retried every 15 s until it binds (so it also comes up once an interface or a port becomes available). `status` is `running` (listening), `stopped` (disabled, or the Controller has not started it yet) or `error`. A rule created before `Run` starts is started by `Run`. | Implemented | `portforward.TestEnabledRulesComeBackAfterARestart`, `TestRulesDoNotListenBeforeRun`, `TestARuleThatCannotBindIsRetriedUntilThePortIsFree`, `api.TestPortForwardThatCannotBindShowsItsErrorAndRecovers`; smoke test with the built binary (changelog 2026-11-18) |
| REQ-PF-008 | **Changes follow the rule.** A change of the protocol, listen address/port or target restarts a running rule (its connections drop). A rename alone, or any change to a disabled rule, touches no listener. Deleting a rule closes its listeners and connections first. Stopping the Controller (context cancelled) closes every listener and connection and returns only after every relay goroutine has finished. | Implemented | `portforward.TestUpdateRestartsOnlyWhenTheRelayChanges`, `TestDeleteClosesTheListenerAndConnections`, `TestStoppingTheServiceClosesEstablishedConnections` |
| REQ-PF-009 | **No overlap, no loop.** Two **enabled** rules cannot bind the same socket: same port, a protocol in common (`tcp`/`udp` ports are separate; `universal` has both) and equal addresses or one of them a wildcard: `409`, naming the other rule. A disabled rule reserves nothing; enabling it re-checks. A rule that would forward to its own listener (same port, and the target is the listen address, or loopback/`localhost`/`0.0.0.0` behind a wildcard listener) is `400`. A rule that is already bound elsewhere on the host is not detected here: the bind fails (REQ-PF-007). | Implemented | `portforward.TestOverlapIsRefusedOnlyBetweenEnabledRules`, `TestOverlaps`, `TestSelfLoop`, `api.TestPortForwardErrors` |

## Audit and access

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-PF-010 | **Audit.** `port_forward.create`, `.update`, `.enable`, `.disable` (a request that only flips `enabled`) and `.delete` are written to `audit_log` with the acting user. Details: name, protocol, `listen` and `target` as `host:port` and, for `.update`, the **names** of the changed fields. They appear in the activity feed (category `system`) as "Port forwarding rule *name* created / updated / switched on / switched off / deleted". Individual connections and datagrams are not logged (debug level only, never addresses of clients at `info`). | Implemented | `api.TestPortForwardAudit` |
| REQ-PF-011 | **Session only.** Every port forwarding operation needs a user session token. A service account key (`legacy`, `mcp`) is refused with `403`, so an AI agent or a legacy client cannot open a port on the Controller host. There are no roles (ADR-011): every signed-in user may manage every rule. | Implemented | `api.TestPortForwardEndpointsRequireASession` |

The operations (REQ-API-033) are in [http-api.md](http-api.md); the screen is REQ-UI-031 in [web-ui.md](web-ui.md).

## Known gaps

- **Anyone who can reach the listen port can use the relay.** There is no source-address allow-list and no
  per-client rate limit (only the connection/session caps of REQ-PF-004/005). The default `0.0.0.0` exposes
  the port on every interface, as the owner asked; choose a specific interface for a LAN-only rule. A rule may
  target loopback, which exposes a service that listens only on the Controller host to whoever reaches the
  listen port. Both are consequences of "any signed-in user manages everything" (ADR-011) and of the feature
  itself; see ADR-022.
- **Ports below 1024 fail under the packaged systemd unit** (`CapabilityBoundingSet=` is empty): the rule shows
  `error: ... permission denied`. [deploy/README.md](../../deploy/README.md) gives an opt-in drop-in that adds
  `CAP_NET_BIND_SERVICE`. The unit itself is unchanged.
- **Not run on Linux or Android, and IPv6 only at unit level.** The tests and the smoke test ran on macOS with
  IPv4 loopback. IPv6 literals are validated and mapped to the right network name in tests, but no IPv6 socket
  was exercised.
- **No traffic figures** (bytes, connections) per rule, and no live view of connections.
- **A firewall on the host** (ufw, firewalld, Windows Firewall) still decides who can reach the listen port.
- **The UI is not run in a browser** (REQ-UI-031: type-check, build and unit tests of its logic only).
