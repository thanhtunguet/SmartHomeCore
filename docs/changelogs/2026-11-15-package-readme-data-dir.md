# 2026-11-15 — Package README: database location and how to move it

| | |
|---|---|
| Author | agent |
| Scope | `deploy/package/README.md.tmpl`, `docs/changelogs/` |
| Requirements | none (documentation of existing behaviour, REQ-RT-009) |

## Added
- Release-package README: the Controller database is stated at install time
  (`/var/lib/homecore/homecore.db`) and a new section "Where the database lives and how to move it" under
  *Data and secrets*:
  - `data_dir` precedence: `--data-dir` (user/token/key subcommands) > `HOMECORE_DATA_DIR` > `data_dir:` in YAML > `./data`.
  - The packaged unit sets `HOMECORE_DATA_DIR`, so `data_dir:` in `/etc/homecore/homecore.yaml` has **no effect**
    for the service; a systemd drop-in is the supported way to move the data.
  - `ReadWritePaths=` is needed for a custom path because the unit uses `ProtectSystem=strict`.

## Verification
- `OUT_DIR=/tmp/pkgout scripts/package-backend.sh amd64 v0.0.0-test`: archive built, generated `README.md`
  contains the new section, no unreplaced `@PLACEHOLDER@`.
- Precedence checked by reading `internal/config/config.go` (`applyEnv` runs after YAML decode) and
  `cmd/homecore/*.go` (`--data-dir`).
- **Not run**: the drop-in procedure on a real Linux host with systemd; the GitHub Actions workflow.
