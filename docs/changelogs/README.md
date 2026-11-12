# Changelogs

One file per working session or merged change set, named `YYYY-MM-DD-short-slug.md` (a session
that spans days uses its end date). Newest entries are listed first below.

| Date | Entry | Summary |
|------|-------|---------|
| 2026-11-12 | [2026-11-12-frontend-api-integration.md](2026-11-12-frontend-api-integration.md) | Frontend submodule wired to the REST API, embedded build, `REQ-UI`, gap report |
| 2026-11-11 | [2026-11-11-project-bootstrap.md](2026-11-11-project-bootstrap.md) | Brief analysis, design, project skeleton (Phase 1 foundation, agent protocol), documentation structure |

## Template

```markdown
# YYYY-MM-DD — Title

| | |
|---|---|
| Author | human / agent name |
| Scope | modules or docs touched |
| Requirements | REQ-… IDs added or changed |

## Added
## Changed
## Fixed
## Security
## Removed
## Verification
<!-- commands run and their results: tests, lint, cross-compile, smoke tests -->
## Known gaps / follow-ups
```

Only include sections that apply. State verification honestly: list what was run, and say plainly
what was not run.
