# Changelogs

One file per working session or merged change set, named `YYYY-MM-DD-short-slug.md` (a session
that spans days uses its end date). Newest entries are listed first below.

| Date | Entry | Summary |
|------|-------|---------|
| 2026-11-14 | [2026-11-14-local-address-interfaces.md](2026-11-14-local-address-interfaces.md) | Show network interface names next to local IP addresses |
| 2026-11-14 | [2026-11-14-disk-tooltip.md](2026-11-14-disk-tooltip.md) | Keep the disk path in a tooltip to prevent overflow |
| 2026-11-14 | [2026-11-14-activity-feed-ui.md](2026-11-14-activity-feed-ui.md) | Activity screen wired to `GET /activity`, merged with command outcomes |
| 2026-11-14 | [2026-11-14-phase-4-ddns-metrics.md](2026-11-14-phase-4-ddns-metrics.md) | Phase 4: public-IP quorum, Cloudflare client, DDNS configs and syncer with backoff, Cloudflare lookups, gopsutil system metrics, migration `0006` (ADR-016) |
| 2026-11-14 | [2026-11-14-phase-3-shutdown.md](2026-11-14-phase-3-shutdown.md) | Phase 3 Controller side: sealed Controller key, signed shutdown dispatcher, agent status + health job, `GET /activity`, `homecore key show` (ADR-015) |
| 2026-11-14 | [2026-11-14-phase-2-devices.md](2026-11-14-phase-2-devices.md) | Phase 2: device registry and API, monitor with hysteresis, type-generic command engine and wake, command history, retention, spec conformance tests (ADR-014) |
| 2026-11-14 | [2026-11-14-phase-1b-authentication.md](2026-11-14-phase-1b-authentication.md) | Phase 1b: argon2id, HS256 JWT, refresh rotation with reuse detection and grace window, `internal/audit`, `homecore user add\|passwd\|list`, bootstrap key removed (ADR-013) |
| 2026-11-14 | [2026-11-14-no-roles-throttle-ddns-lookup.md](2026-11-14-no-roles-throttle-ddns-lookup.md) | No roles (ADR-011), idempotent commands + 1 s per-device throttle (ADR-012), Cloudflare lookups, generated UI types and tests |
| 2026-11-12 | [2026-11-12-api-gap-quick-wins.md](2026-11-12-api-gap-quick-wins.md) | Additive API gaps closed: `501` envelope, cache headers, device type/OS, `/system/info`, viewer gating |
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
