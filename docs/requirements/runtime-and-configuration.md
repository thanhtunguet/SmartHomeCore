# Requirements: Runtime, Modes and Configuration

| | |
|---|---|
| Brief | §1 (roles, one build), §2, §5 ("One binary, several roles") |
| Code | `cmd/homecore` (`main.go`, `user.go`), `internal/app`, `internal/config`, `internal/logging`, `internal/httpx`, `internal/version`, `mobile` |
| Design | [architecture §4](../design/architecture.md#4-runtime-model) |
| Last updated | 2026-11-23 (deprecated `controller.api_docs` setting); 2026-11-15 (agent.id, `--agent-id`/`--trusted-controller`/`--agent-listen`; earlier: 2026-11-14 Phase 4: REQ-RT-013 DDNS keys; earlier: Phase 3 REQ-RT-012 `homecore key show`, Phase 2 REQ-RT-004 command-deadline and retention checks) |
| Status | **Implemented** |

## Functional requirements

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-RT-001 | The `homecore` binary runs in `controller`, `agent` or `both` mode, selected by `mode` in the config file, `HOMECORE_MODE`, or `--mode`. Default: `controller`. | Implemented | `TestLoadBytesOverridesAndEnv`; smoke test 2026-11-11 |
| REQ-RT-002 | In `both` mode, Controller and Agent run in one process on **separate listeners** with separate config sections and separate authentication. They share only the logger and the process lifecycle. Identical listen addresses are rejected. | Implemented | `TestLoadBytesRejectsUnknownFieldsAndBadValues/same_listener`; smoke test |
| REQ-RT-003 | Configuration precedence: CLI flags > `HOMECORE_*` environment > YAML file > built-in defaults. The config path comes from `--config` or `HOMECORE_CONFIG`. Supported overrides: `HOMECORE_MODE`, `HOMECORE_DATA_DIR`, `HOMECORE_LOG_LEVEL`, `HOMECORE_LOG_FORMAT`, `HOMECORE_CONTROLLER_LISTEN`, `HOMECORE_AGENT_LISTEN`, `HOMECORE_AGENT_ID`; flags `--mode`, `--data-dir`, `--agent-id`, `--agent-listen` (and `--trusted-controller`, which adds to the list, REQ-AG-016/017). | Implemented | `TestLoadBytesOverridesAndEnv` (env); flags by smoke test |
| REQ-RT-004 | Configuration is validated strictly at startup: unknown YAML keys fail; mode, log level/format, listen addresses, TLS pairs, `trusted_proxies` CIDRs, auth TTLs, monitor values, agent actions (`shutdown`/`reboot` only), `agent.id` (non-nil UUID), `clock_skew` (5 s–5 m) and trusted key presence are checked. Since Phase 2 `controller.commands.wake_deadline` and `shutdown_deadline` must be > 0 and `controller.retention_days` must be >= 0 (0 keeps everything). All errors are reported together. | Implemented | `TestLoadBytesRejectsUnknownFieldsAndBadValues` (incl. zero deadlines, negative retention), `TestRetentionZeroKeepsEverything`, `TestShippedConfigFilesAreValid` |
| REQ-RT-005 | Every long-running component exposes `Run(ctx) error`. On SIGINT/SIGTERM (or `mobile.Stop`) the context is cancelled and HTTP listeners shut down gracefully within `shutdown_timeout` (default 10 s). If one role fails, the other is stopped and the process exits non-zero. | Implemented | Smoke test (clean exit, both listeners stopped) |
| REQ-RT-006 | Logging uses `log/slog` with `text` or `json` format and `debug`/`info`/`warn`/`error` levels; entries carry a `role` attribute (`controller`/`agent`). | Implemented (untested) | Smoke test log |
| REQ-RT-007 | Build metadata (`Version`, `Commit`) is injected with `-ldflags` and printed by `--version`; `/healthz` and the agent health endpoint report the version. | Implemented | Smoke test |
| REQ-RT-008 | The `mobile` package exposes a gomobile-compatible API: `SetLogSink(LogSink)`, `Start(dataDir, configYAML, masterKey)`, `Stop(timeoutMillis)`, `IsRunning()`, `LastError()`, `Version()`. On Android only `controller` mode is accepted; the data dir is forced to the given app-private directory. | Implemented (untested) | Compiles for `android/arm64`; AAR not built yet |
| REQ-RT-009 | Data directory layout: `<data_dir>/homecore.db` (Controller DB), `<data_dir>/master.key`, `<agent.data_dir>/agent_id` (default `<data_dir>/agent`; only when the Agent has no supplied id, REQ-AG-016). Directories are created with mode `0700`, secret files with `0600`. | Implemented | `TestMasterKeyPersistsWithStrictPermissions`; manual permission check |
| REQ-RT-010 | The binary builds with `CGO_ENABLED=0` for linux/amd64, linux/arm64, linux/arm, windows/amd64, darwin/arm64, darwin/amd64 and android/arm64 (`make cross`). | Implemented | `make cross` 2026-11-11 |
| REQ-RT-011 | `homecore user add\|passwd\|list` subcommands share the server's configuration loading (`--config`, `--data-dir`, `HOMECORE_*`) and operate on the same `<data_dir>/homecore.db`; see [REQ-AUTH-015..018](authentication.md). The key `controller.auth.bootstrap_admin_user` no longer exists (removed 2026-11-14; a config that sets it fails strict validation). `controller.auth` keeps `access_token_ttl`, `refresh_token_ttl`, `login_rate_per_minute`, `api_rate_per_minute`. | Implemented | `TestUserDataDirFromEnvironment`, `TestLoadBytesRejectsUnknownFieldsAndBadValues/removed_bootstrap_key`, `TestShippedConfigFilesAreValid` |
| REQ-RT-012 | `homecore key show` shares the server's configuration loading (`--config`, `--data-dir`, `HOMECORE_*`), creates the Controller key if it does not exist yet (and audits `controller_key.create` with actor `cli`), and prints `key_id`, `algorithm`, the base64 public key and an Agent `trusted_controllers` snippet. It never prints the private key; see [REQ-SHD-002](shutdown-agent.md). | Implemented | `TestKeyShowCreatesThenReprintsTheSameKey`, `TestKeyUsageErrors` |
| REQ-RT-013 | `controller.ddns` keys: `ip_providers` (default: `https://api64.ipify.org`, `https://icanhazip.com`, `https://ifconfig.me/ip`, `https://checkip.amazonaws.com`; a YAML list **replaces** the defaults), `cloudflare_api_url` (default `https://api.cloudflare.com/client/v4`) and `allow_insecure_http` (default `false`). Validated only when the process runs a Controller: at least 2 providers on different hosts, absolute `https` URLs without user-info (`http` only with `allow_insecure_http`); the Cloudflare URL must be `https`, without query or fragment (same switch). Provider URLs may carry keys and are never logged or returned (only the host is). The `ip_providers` learn the Controller's public IP; see [ddns.md](ddns.md). | Implemented | `TestDDNSConfig`, `TestLoadBytesRejectsUnknownFieldsAndBadValues` (`one_ddns_provider`, `http_ddns_provider`, `same-host_providers`, `provider_credentials`, `http_cloudflare_url`, `cloudflare_url_query`), `TestShippedConfigFilesAreValid` |

| REQ-RT-014 | The legacy `controller.api_docs` YAML key remains accepted for existing configs but is ignored; availability is controlled only by the persisted OpenAPI setting (REQ-API-034). | Implemented (untested) | Config decoding and DB-controlled route coverage pending |

## Non-functional requirements

| ID | Requirement | Status |
|----|-------------|--------|
| REQ-RT-N1 | No goroutine is started outside a component's `Run`; no global mutable state except the mobile binding's single-instance guard. | Implemented |
| REQ-RT-N2 | Startup is idempotent: restarting reuses the DB, master key and agent id without manual steps (needed for Android restarts). | Implemented |
| REQ-RT-N3 | Example (`configs/homecore.example.yaml`) and dev (`configs/dev.yaml`) configs stay valid in every mode. | Implemented (`TestShippedConfigFilesAreValid`) |

## Known gaps

- The `flag` declarations of the server command (`cmd/homecore` without a subcommand) are not unit-tested; the config merge they feed (`loadConfigWith`, agent flags and environment) is (REQ-AG-016/017), and so are the `user` subcommands (REQ-AUTH-015).
- The `mobile` bindings expose no way to create accounts (no CLI on Android); to be designed with Phase 5. `mobile` still compiles for android/arm64.
- The gomobile AAR has never been built; `make android-aar` needs gomobile and the Android NDK.
- Not measured yet: idle CPU/RAM (goal G-3) and long-running stability (goal G-2).
