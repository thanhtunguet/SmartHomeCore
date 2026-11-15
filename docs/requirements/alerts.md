# Requirements: Alerts

| | |
|---|---|
| Brief | not in the brief: added at the owner's request (ADR-021) |
| Code | `internal/alerts` (`alerts.go`, `telegram.go`, `repo.go`, `repo_sqlite.go`, `service.go`, `hooks.go`, test fake `alertstest/`), hook points in `internal/ddns/service.go`, `internal/commands/engine.go`, `internal/monitoring/monitor.go`, `internal/api/handlers/handlers_alerts.go`, `internal/storage/migrations/0008_alert_channels.sql`, `internal/app/app.go` |
| Design | [architecture §6.12](../design/architecture.md), [database.md](../design/database.md) (`alert_channels`), [ADR-021](../design/decisions.md#adr-021--alerts-channels-adapters-and-hooks-in-the-producers) |
| Last updated | 2026-11-15 |
| Status | **Implemented**, verified by unit tests, integration tests through the real router with a fake Telegram Bot API, and `-race`. **Not** verified against the real Telegram (see Known gaps) |

An alert tells the owner something happened. A **channel** is one destination (today a Telegram
user) served by an **adapter**; it subscribes to **events**. Tests named here are in
`internal/alerts`, `internal/ddns`, `internal/commands`, `internal/monitoring`, `internal/activity`
and `internal/api` (`alerts_test.go`; every response is checked against `api/openapi.yaml`).

## Events

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-ALERT-002 | **`ip_changed`.** When a DDNS sync (the scheduler or `POST /ddns/{id}/sync`) moves a record to a different address, the channels subscribed to `ip_changed` get "🌐 Public IP changed" with the record name, its type and `old → new`. Only a change of the **address** counts: a record that is already right, a fix of only the proxy flag or TTL, and a failed sync are silent. A record whose old content was empty or unreadable is reported as `(empty) → new`. The alert is sent whether or not the sync result could be stored (the record was moved at Cloudflare either way). | Implemented | `ddns.TestIPObserverHearsOnlyAboutAddressChanges`, `alerts.TestIPHook`, `api.TestIPChangeAlert` |
| REQ-ALERT-003 | **`power_command`.** When the command engine creates **and dispatches** a wake or shutdown command, whoever asked (UI, the legacy endpoints, MCP: all go through `Engine.Request`), the channels subscribed to `power_command` get one message: "⚡ Wake requested" / "⏻ Shutdown requested: *device*", what happened ("The Wake-on-LAN packet was sent." / "The agent accepted the shutdown.") and who asked ("By alice", "By alice (service key)"). A dispatch that failed is announced as "❌ Wake failed" / "❌ Shutdown failed" with the error code. **Not** announced: a request answered with the command already in progress (idempotent repeat, ADR-012), and a command born completed because the device was already in the target state (nothing was sent). | Implemented | `commands.TestIssueObserverHearsOnlyAboutNewDispatchedCommands`, `alerts.TestCommandHook`, `api.TestPowerCommandAlerts` |
| REQ-ALERT-004 | **`status_changed`: a device changes status outside HomeCore's control.** When the monitor records a transition (after hysteresis, REQ-MON-004) between `online` and `offline`, the channels subscribed to `status_changed` get "🔴 *device* went offline" or "🟢 *device* is online", with the probe address. It is suppressed when the change is explained by a HomeCore command in progress: a wake explains `online`, a shutdown explains `offline` (a wake does not excuse going offline, nor a shutdown coming online). Never reported: the first sighting of a device (`unknown` → anything), and a device with monitoring switched off (`enabled: false`; a manual probe of one included). When the lookup of the active command fails the change is reported anyway (a missing alert is worse than a redundant one). A command that already timed out no longer explains a late change, which is then reported. The monitor tells the alert hook **before** the commands engine completes the command (otherwise the explanation would be gone). | Implemented | `monitoring.TestTransitionObserverSeesChangesBeforeTheObserver`, `alerts.TestStatusHook`, `api.TestUnexpectedStatusChangeAlerts` |

## Channels and adapters

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-ALERT-001 | **Channels.** A channel has a `name` (1..100 characters, no control characters, unique ignoring case: `409`), a `type`, `enabled` (default true), `events` (a non-empty subset of `ip_changed`, `power_command`, `status_changed`, no duplicates; default all; stored in that canonical order), non-secret `settings` (string values, at most 16) and one `secret`. `type` cannot change. The adapter validates `settings` and `secret` together: on create, and on update whenever either changes (the other one is the stored value), so a refused update leaves the channel as it was. Every problem is a `400` that never echoes the secret. | Implemented | `alerts.TestCreateChannel`, `TestEventsAreStoredInCanonicalOrder`, `TestCreateValidation`, `TestNamesAreUniqueIgnoringCase`, `TestUpdate`, `TestDelete`, `api.TestAlertChannelCRUDOverHTTP` |
| REQ-ALERT-005 | **Telegram adapter** (`type: telegram`), the first adapter, takes exactly two parameters: the **bot token** (`secret`, from BotFather: `<bot id>:<secret>`) and the **target user id** (`settings.target_user_id`, a number; negative for a group). It sends plain text (title, newline, body, cut to Telegram's 4096 characters) with the Bot API `sendMessage`; no markup, so nothing in a device name can break the message. Requests have a 10 s timeout, follow no redirects and honour the environment's proxy. Telegram's own refusal is the error text ("Forbidden: bot can't initiate conversation with a user"); `429` and `5xx` and network errors are *temporary* (REQ-ALERT-008), anything else is final. The target must have started a chat with the bot (Telegram's rule); a refusal shows in `last_error`. | Implemented | `alerts.TestTelegramFactoryValidation`, `TestTelegramSendsPlainTextToTheTarget`, `TestTelegramTruncatesLongText`, `TestTelegramErrors`, `TestTelegramHonoursContext` |
| REQ-ALERT-011 | **Adding an adapter** needs one `Factory` (`func(settings, secret) (Adapter, error)`) registered by `internal/app`, an entry in the `AlertChannelType` enum of the spec, and a paragraph here. No migration (`alert_channels.type` is not a `CHECK`), no change to the producers or the delivery code. A channel whose type is no longer registered is reported as a failed delivery, not a crash. | Implemented (one adapter exists) | `alerts.TestNotifyDeliversToSubscribedEnabledChannels` (a second adapter type next to Telegram), `TestCreateValidation` (unknown type) |

## Secrets

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-ALERT-006 | **The secret is sealed and write-only.** The bot token is stored only as `alert_channels.secret_sealed` (AES-256-GCM with the master key, AAD `alert_channels.secret`, REQ-SEC-002) and is returned by no endpoint: responses carry `has_secret`. It is not in `settings`, in `last_error`, in the audit log, in the activity feed or in the log. The Telegram URL contains the token (that is how the Bot API works), so the adapter strips the URL from transport errors and scrubs the token from Telegram's replies before any error leaves it. See REQ-SEC-007. | Implemented | `alerts.TestSecretIsSealedAtRest`, `TestTelegramErrorsNeverContainTheToken`, `TestFailedDeliveryNeverStoresTheToken`, `api.TestAlertChannelTestEndpoint` (token absent from audit rows, `last_error`, `settings` and the log), `api.TestAlertChannelAudit` |

## Delivery

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-ALERT-007 | **Producers never wait.** `Service.Notify` only puts the alert in an in-memory queue (64 entries) and returns; `Service.Run(ctx)` (an errgroup member of the Controller) is the single consumer. Alerts are delivered in the order they were queued, to the matching channels in parallel (enabled, subscribed to the event); one failing or slow channel does not hold back another. A full queue drops the **new** alert and logs a warning. `Run` stops when its context is cancelled; alerts still queued then are lost (alerts describe a moment; replaying them later would mislead). Notify before `Run` starts is fine (it waits in the queue). | Implemented | `alerts.TestNotifyDeliversToSubscribedEnabledChannels`, `TestOneFailingChannelDoesNotBlockAnother`, `TestNotifyDropsWhenTheQueueIsFull`, `TestRunStopsWhenTheContextIsCancelled` |
| REQ-ALERT-008 | **Outcome and retries.** One delivery is up to three attempts (15 s each): the first, then after 1 s and 5 s, **only** while the failure is temporary. The outcome is stored on the channel: `last_sent_at` on success (clearing `last_error`), `last_error` (cut to 500 characters) and `last_error_at` on failure. A delivery cut short by shutdown is not recorded. A panic in an adapter is contained and logged. | Implemented | `alerts.TestDeliveryOutcomeAndRetries` |
| REQ-ALERT-009 | **Test message.** `POST /alerts/channels/{id}/test` sends one test message through the channel now, **one attempt**, also when the channel is disabled, and stores the outcome like any delivery. Answer `200` with `result` `sent` or `failed` and `error`: a failed delivery is a result, not an HTTP error (as `syncDdnsConfig`). Unknown id `404`. | Implemented | `alerts.TestTestMessage`, `api.TestAlertChannelTestEndpoint` |

## API, audit and activity

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-ALERT-010 | **Audit.** `alert_channel.create`, `.update`, `.delete` and `.test` are written to `audit_log` with the acting user. Details: name, type, the **names** of the changed fields (never a value) and, for a failed test, the error text. They appear in the activity feed under category `system` with English messages ("Alert channel My phone created", "Test message sent to alert channel My phone"). The alerts themselves are not audited (they are not an action of a user). | Implemented | `api.TestAlertChannelAudit`, `activity.TestMessages` |

REQ-API-032 (operations, authentication) is in [http-api.md](http-api.md).

## Known gaps

- **Never run against the real Telegram.** The adapter follows the documented Bot API (`sendMessage`,
  `{ok, description}` replies); the tests use `alertstest`, a fake of it. First live run is an owner
  action: create a bot with BotFather, send it `/start` from the target account, add the channel and
  press the test button.
- **No UI yet.** The REST API is complete; the frontend submodule has no screen for channels and its
  generated types are stale until regenerated (`make ui-check-api`).
- **Messages are in English** and fixed; there is no template, language or quiet-hours setting.
- **Not alerted** (not asked for): a DDNS sync that keeps failing, a command that timed out or
  completed, an agent that became `unreachable`, a Controller that restarted. The Controller cannot
  alert about its own absence.
- **No flap suppression** beyond the monitor's hysteresis (2 failed probes to go offline, 1 success to
  go online): a device that bounces every few minutes produces a message for each change.
- **No history of alerts**: only the last outcome per channel (`last_sent_at`, `last_error`).
- The queue is in memory (REQ-ALERT-007): a Controller stop loses undelivered alerts, and a long
  Telegram outage (retries are about 6 s) loses the alerts of that time.
- Only Telegram exists; the settings of a future adapter must be flat string values.
