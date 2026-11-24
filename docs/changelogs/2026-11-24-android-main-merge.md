# 2026-11-24 — Merge main into Android

| | |
|---|---|
| Author | Codex |
| Scope | Android merge, architecture, runtime requirements, frontend API contract tests |
| Requirements | REQ-RT-008, REQ-RT-014, REQ-API-034 |

## Changed

- Resolved the changelog, architecture, and runtime documentation conflicts. The merged docs cover the Android Controller lifecycle and account methods, the persisted OpenAPI setting, and the frontend source now tracked in SmartHomeCore.
- Added the OpenAPI settings read and update calls to the frontend contract suite so it checks each HTTP method and path against `api/openapi.yaml`.

## Verification

- `make test lint cross` passed. `golangci-lint` was unavailable and skipped by Makefile.
- `pnpm install --frozen-lockfile`, `pnpm lint`, `pnpm test` (241 tests), `pnpm check:api`, and `pnpm build` passed from `frontend/`.
- `./gradlew testDebugUnitTest assembleDebug` passed in the Android parent checkout using the sibling submodule revisions present at build time. Android device and browser validation remain pending.
- The Git index had no unmerged entries, and `git diff --cached --check MERGE_HEAD` passed. The diff against the Android parent reports whitespace already present in two incoming frontend files.
- GitNexus `detect-changes --scope all` rated the full merge critical and capped its symbol listing. Reviewed the staged differences against both parents.
