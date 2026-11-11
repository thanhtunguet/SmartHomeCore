# Requirements: Runtime, Modes and Configuration

| | |
|---|---|
| Brief | §1 (roles, one build), §2, §5 ("One binary, several roles") |
| Code | `cmd/homecore`, `internal/app`, `internal/config`, `internal/logging`, `internal/httpx`, `internal/version`, `mobile` |
| Design | [architecture §4](../design/architecture.md#4-runtime-model) |
| Status | **Implemented** |

## Functional requirements

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-RT-001 | The `homecore` binary runs in `controller`, `agent` or `both` mode, selected by `mode` in the config file, `HOMECORE_MODE`, or `--mode`. Default: `controller`. | Implemented | `TestLoadBytesOverridesAndEnv`; smoke test 2026-11-11 |
| REQ-RT-002 | In `both` mode, Controller and Agent run in one process on **separate listeners** with separate config sections and separate authentication. They share only the logger and the process lifecycle. Identical listen addresses are rejected. | Implemented | `TestLoadBytesRejectsUnknownFieldsAndBadValues/same_listener`; smoke test |
| REQ-RT-003 | Configuration precedence: CLI flags > `HOMECORE_*` environment > YAML file > built-in defaults. The config path comes from `--config` or `HOMECORE_CONFIG`. Supported overrides: `HOMECORE_MODE`, `HOMECORE_DATA_DIR`, `HOMECORE_LOG_LEVEL`, `HOMECORE_LOG_FORMAT`, `HOMECORE_CONTROLLER_LISTEN`, `HOMECORE_AGENT_LISTEN`; flags `--mode`, `--data-dir`. | Implemented | `TestLoadBytesOverridesAndEnv` (env); flags by smoke test |
| REQ-RT-004 | Configuration is validated strictly at startup: unknown YAML keys fail; mode, log level/format, listen addresses, TLS pairs, `trusted_proxies` CIDRs, auth TTLs, monitor values, agent actions (`shutdown`/`reboot` only), `clock_skew` (5 s–5 m) and trusted key presence are checked. All errors are reported together. | Implemented | `TestLoadBytesRejectsUnknownFieldsAndBadValues`, `TestShippedConfigFilesAreValid` |
| REQ-RT-005 | Every long-running component exposes `Run(ctx) error`. On SIGINT/SIGTERM (or `mobile.Stop`) the context is cancelled and HTTP listeners shut down gracefully within `shutdown_timeout` (default 10 s). If one role fails, the other is stopped and the process exits non-zero. | Implemented | Smoke test (clean exit, both listeners stopped) |
| REQ-RT-006 | Logging uses `log/slog` with `text` or `json` format and `debug`/`info`/`warn`/`error` levels; entries carry a `role` attribute (`controller`/`agent`). | Implemented (untested) | Smoke test log |
| REQ-RT-007 | Build metadata (`Version`, `Commit`) is injected with `-ldflags` and printed by `--version`; `/healthz` and the agent health endpoint report the version. | Implemented | Smoke test |
| REQ-RT-008 | The `mobile` package exposes a gomobile-compatible API: `SetLogSink(LogSink)`, `Start(dataDir, configYAML, masterKey)`, `Stop(timeoutMillis)`, `IsRunning()`, `LastError()`, `Version()`. On Android only `controller` mode is accepted; the data dir is forced to the given app-private directory. | Implemented (untested) | Compiles for `android/arm64`; AAR not built yet |
| REQ-RT-009 | Data directory layout: `<data_dir>/homecore.db` (Controller DB), `<data_dir>/master.key`, `<agent.data_dir>/agent_id` (default `<data_dir>/agent`). Directories are created with mode `0700`, secret files with `0600`. | Implemented | `TestMasterKeyPersistsWithStrictPermissions`; manual permission check |
| REQ-RT-010 | The binary builds with `CGO_ENABLED=0` for linux/amd64, linux/arm64, linux/arm, windows/amd64, darwin/arm64, darwin/amd64 and android/arm64 (`make cross`). | Implemented | `make cross` 2026-11-11 |

## Non-functional requirements

| ID | Requirement | Status |
|----|-------------|--------|
| REQ-RT-N1 | No goroutine is started outside a component's `Run`; no global mutable state except the mobile binding's single-instance guard. | Implemented |
| REQ-RT-N2 | Startup is idempotent: restarting reuses the DB, master key and agent id without manual steps (needed for Android restarts). | Implemented |
| REQ-RT-N3 | Example (`configs/homecore.example.yaml`) and dev (`configs/dev.yaml`) configs stay valid in every mode. | Implemented (`TestShippedConfigFilesAreValid`) |

## Known gaps

- CLI flag handling (`cmd/homecore`) has no automated test.
- The gomobile AAR has never been built; `make android-aar` needs gomobile and the Android NDK.
- Not measured yet: idle CPU/RAM (goal G-3) and long-running stability (goal G-2).
