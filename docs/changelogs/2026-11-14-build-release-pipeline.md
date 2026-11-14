# 2026-11-14 — Build and release pipeline

| | |
|---|---|
| Author | agent |
| Scope | `.github/workflows/build.yml`, `scripts/package-backend.sh`, `deploy/package/README.md.tmpl`, `Makefile`, `deploy/README.md`, `docs/plans/roadmap.md` |
| Requirements | none (infrastructure; roadmap Phase 6, GAP-INF-10) |

## Added
- `.github/workflows/build.yml`: jobs `meta` (version from `git describe`), `frontend`, `backend`
  (matrix linux/amd64 + linux/arm64) and `release` (tags `v*` only).
  - `frontend`: checks out the submodule at the pinned commit, `npm ci`, `vite build` into `web/static`,
    uploads `homecore-ui-<version>.zip` (+ `.sha256`).
  - `backend`: unzips that build into `web/static` so it is embedded in the binary, then runs
    `scripts/package-backend.sh`. Output `homecore-<version>-linux-<arch>.tar.gz` (+ `.sha256`) with the
    binary, `migrations/`, `configs/`, `deploy/systemd`, `deploy/polkit`, `VERSION` and `README.md`.
  - `release`: on a `v*` tag, attaches all files and `SHA256SUMS` to a GitHub Release.
- `scripts/package-backend.sh` and `make package` (same packaging locally).
- `deploy/package/README.md.tmpl`: install and upgrade guide shipped as `README.md` in every backend archive
  (install Controller/Agent, first account, upgrade, rollback, data/secrets, troubleshooting, uninstall).

## Notes
- `migrations/*.sql` in the archive are reference only: they are embedded in the binary and applied
  automatically at Controller start. The README says so.
- Rollback needs the pre-upgrade data backup: the migration runner has no downgrade and an older binary does not
  refuse a newer schema. This is documented in the archive README, not changed in code.
- `.gitmodules` keeps the SSH URL. The `frontend` job rewrites `git@github.com:` to `https://github.com/` with `git config --global url.<https>.insteadOf` and runs `git submodule update --init`, so no key or token is needed. **`HomeCoreUI` must be public.**

## Verification
- `scripts/package-backend.sh amd64|arm64 v0.0.0-test` run locally: both archives built, `file` shows a static
  ELF for the right arch, contents and generated README checked, no unreplaced placeholders.
- Workflow YAML parsed with PyYAML; the frontend zip/unzip commands run locally against `web/static`.
- **Not run**: the workflow on GitHub Actions itself (no runner here; no `actionlint`), the Node build in CI,
  the release job, and the install/upgrade commands from the README on a real Linux host with systemd.

## Known gaps / follow-ups
- Only linux/amd64 and linux/arm64; linux/arm, macOS, Windows and Android are still open (roadmap).
- No test/lint gate in this workflow yet (roadmap: CI pipeline).
