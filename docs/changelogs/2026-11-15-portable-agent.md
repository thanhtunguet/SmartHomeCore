# 2026-11-15 — Portable Agent: id and key as arguments, setup prompt, UI command per OS

| | |
|---|---|
| Author | agent |
| Scope | `internal/agentsetup` (new), `cmd/homecore`, `internal/agent`, `internal/config`, `configs/homecore.example.yaml`, `deploy/`, `frontend/` (submodule), docs |
| Requirements | REQ-AG-010 (changed), REQ-AG-016..019, REQ-UI-025 (new); REQ-RT-003/004/009 (updated). ADR-019 |

## Added
- **Agent arguments**: `--agent-id` (env `HOMECORE_AGENT_ID`, config `agent.id`), `--trusted-controller` (repeatable), `--agent-listen`. With an id given the Agent reads and writes nothing on disk; the id must be a non-nil UUID and is normalised to lower case.
- **Setup prompt** (`internal/agentsetup`): `homecore --mode agent` on a terminal with no identity asks for the command copied from the UI, validates it (only four options, every value checked, output rendered from the validated values), writes the files for the OS and prints the install notes:
  - Linux: `homecore-agent.service` (hardened like `deploy/systemd`, id and key in `ExecStart`, no state dir) and `50-homecore-agent.rules`, then `sudo` steps.
  - Windows: `HomeCore Agent.lnk` via PowerShell `WScript.Shell`, with the "Run as administrator" bit set in Go, then `Register-ScheduledTask` and `New-NetFirewallRule` lines.
  - Other systems: the command is printed.
  - Never overwrites a different existing file.
- **UI** (frontend submodule): `AgentSetupPanel` in the device form (Remote Shutdown tab) and in the device sheet: Agent ID (auto-generated UUIDv7 when the integration is enabled, **Generate**, **Copy**) and the start command per system (Windows, Linux, macOS) with **Copy**; `src/api/agentCommand.ts`, `src/hooks/useCopy.ts`.
- ADR-019; requirement rows; package README Option A (command from the UI) and Option B (config file); `deploy/README.md` Windows and Linux notes; `agent.id` in `configs/homecore.example.yaml`; roadmap items and backlog.

## Changed
- `agent.New` uses `cfg.ID` when set, else the persisted id as before (existing installs and non-interactive starts unchanged). Supersedes the "Agent generates and persists its id" clause of ADR-002 only.
- `loadConfig` now delegates to `loadConfigWith`, which takes the flag values keyed by environment variable.
- The key block in the device form is retitled "only for an agent configured with a file".

## Security
- The pasted command is data, never executed or copied into output. A test shows a newline smuggled into `--agent-id` is rejected and that a unit has exactly one `ExecStart`.
- The id and the Controller **public** key now appear in a unit file, a shortcut and the process list. Neither is a secret (ADR-019).
- No change to the verification order or to the allowlist; the Agent still holds no private key.
- The generated polkit rule is a Go constant; a test keeps it equal to `deploy/polkit/50-homecore-agent.rules`, and the hardening lines equal to `deploy/systemd/homecore-agent.service`.

## Verification
- `make test`: all packages pass. New tests: `internal/agentsetup` (parse, rejection table, unit rendering, drift, `.lnk` flag, Run on Linux/Windows/other with fakes, re-prompt, cancel, no overwrite), `internal/agent` (configured id, nothing on disk, invalid id, stored id still works), `internal/config` (`agent.id` yaml/env/validation), `cmd/homecore` (flag > env, `agentNeedsSetup`).
- `make lint` (`go vet`; `golangci-lint` not installed, skipped), `make cross` (all 7 targets, including `windows/amd64` and `android/arm64`).
- Smoke test on macOS: built binary, run in a pty with `--mode agent` and no id: prompt shown, a bad command explained, a good one accepted and the `darwin` notes printed. Run in the foreground with `--agent-id` (upper case), `--trusted-controller`, `--agent-listen 127.0.0.1:9791`: `GET /agent/v1/healthz` returned the lower-case id, log showed `trusting controller name=cli` and `source=configured`, and no `data/` directory was created.
- Frontend: `npx vitest run` (13 files, 198 tests, 6 new), `npx tsc --noEmit`, `vite build`.
- `scripts/package-backend.sh amd64 v0.0.0-test`: archive built from the changed README template, no unreplaced placeholders.
- GitNexus impact before editing: `LoadOrCreateID`, `applyEnv`, `Validate` and `agent.New` are **HIGH** (they sit on the start-up path of `main` and `mobile.Start`). The changes are additive (a new optional key, a new branch in `agent.New`); `LoadOrCreateID` and `Validate`'s existing rules are unchanged, and the whole suite passes. `detect-changes --scope all`: 18 files, 35 symbols, risk **high**, 15 affected flows, all of them the `main` start-up flows (`run`, `main`, `New`); the CLI printed no `partial`/`truncated` marker but cut its own symbol list ("and 20 more"), so the list above was not read in full.
- **Not run**: the generated unit and polkit rule on a systemd host; the PowerShell shortcut creation, the elevation bit and the printed PowerShell lines on Windows (compiled only); the UI in a browser (no screenshot taken; copy buttons and tab switching are unexercised); the GitHub Actions workflow.

## Known gaps / follow-ups
- No launchd generation for macOS; no TLS flags for the Agent (an `https` binding still needs `agent.tls` in a config file); no Windows Service wrapper.
- Two machines with the same id both accept its commands (accepted risk, ADR-019).
- The `frontend/` changes are in the HomeCoreUI submodule working tree and are **not committed**: commit them there and bump the pointer here.
