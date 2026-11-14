# 2026-11-14 — Phase 4: Cloudflare DDNS and system metrics

| | |
|---|---|
| Author | agent (Claude), Phase 4 |
| Scope | `internal/ddns` (new), `internal/system`, `internal/platform/metrics.go`, `internal/config`, `internal/activity`, `internal/app`, `internal/api` (handlers, server, tests), migration `0006`, `configs/homecore.example.yaml`, `go.mod`, docs |
| Requirements | REQ-DDNS-001..008 and REQ-SYS-001..004 (new, [ddns.md](../requirements/ddns.md)), REQ-API-020 (Implemented), REQ-API-025 (new), REQ-RT-013 (new), REQ-SEC-003/004, REQ-RET-001; ADR-016 |

## Added

- **Public-IP discovery** (`internal/ddns/discover.go`): at least 2 providers must agree and no other address may tie; family-pinned sockets, public ranges only, same-host redirects only, 128-byte bodies, shared in-flight request and 10 s success cache; only provider hosts are logged.
- **Cloudflare client** (`cloudflare.go`, `net/http`, no SDK): zones, A/AAAA records, get and `PATCH` by record id (only `content`, `proxied`, `ttl`); typed `CloudflareError` with `Retry-After`; the token appears in no error.
- **Repository, service and syncer**: sealed token (AAD `ddns_configs.api_token`), required `record_id` (no lookup by name), duplicate protection, verification against Cloudflare on create and on token/target changes, one sync path for scheduler and `POST /ddns/{id}/sync`, exponential backoff 1 s to 5 min with jitter, `Retry-After`, independent schedules, bounded concurrency.
- **Handlers** (`handlers_ddns.go`): list, create, update, delete, sync and the two Cloudflare lookups; `502 cloudflare_error`; audit `ddns.create|update|delete|sync`, shown in the activity feed (category `ddns`).
- **Migration `0006`**: `detected_ip`, `dns_value`, `last_checked_at`, and the partial unique index `ddns_configs_record (zone_id, cf_record_id)`.
- **System metrics**: `system.Gopsutil` (gopsutil v4) on top of `RuntimeProvider`, `restricted` for what cannot be read, delta CPU sampler, `system.Cached` (3 s); `GET /system/metrics`.
- **Config**: `controller.ddns.ip_providers`, `cloudflare_api_url`, `allow_insecure_http` with validation; documented in `configs/homecore.example.yaml`.
- `internal/ddns/ddnstest`: in-memory Cloudflare over `httptest` for tests.
- Docs: new [ddns.md](../requirements/ddns.md), ADR-016, architecture §6.7 and §6.10, database `0006`, REQ-API-020/025, REQ-RT-013, overview traceability, roadmap ticks, report §14.

## Changed

- `ddns.go` rewritten: the earlier interface sketch (name-based record lookup) is replaced by `DNSProvider` and `IPDiscoverer` with record ids and a per-call token. `PruneSyncHistory` is kept.
- `platform.Metrics` extended (`CPUPercent` pointer, cores, load, kernel, process fields, `Restricted`).
- `activity`: DDNS messages and whitelisted detail keys `record_name`, `ip`, `sync_result`.
- `handlers_devices.go`: `apiError` maps Cloudflare and DDNS errors; `auditTarget` takes any details type.
- `go.mod`: `github.com/shirou/gopsutil/v4` and its transitive modules (approved by the brief).
- `api/openapi.yaml` was **not** changed; `internal/api/gen` is byte-identical after `make generate`.

## Security

- Token sealed at rest, never returned (`has_api_token`), never in logs, errors, audit details or the activity feed (checked by tests and by the smoke test).
- Provider URLs may carry keys: only the host is shown. A mismatching record is never written. Non-public addresses are never published.

## Verification

Run on macOS (darwin/arm64), after the rate-limit interruption (the smoke test and all commands below were redone or finished after it):

| Command | Result |
|---------|--------|
| `make test` (`go test ./...`) and `go test -count=1 ./...` | pass, no failures |
| `make test-race` | pass; `go test -race -count=1` on `internal/ddns`, `internal/api`, `internal/system`, `internal/app`, `internal/activity` re-run uncached: pass |
| `make lint` | `go vet` and the gofmt check pass; **golangci-lint is not installed**, so it was skipped |
| `make cross` | all 7 targets build with `CGO_ENABLED=0` (linux amd64/arm64/arm, windows/amd64, darwin arm64/amd64, android/arm64) |
| `make generate` + `git diff --exit-code internal/api/gen` | clean, spec unchanged |
| `node .gitnexus/run.cjs detect-changes --scope all --repo .` (after `analyze --index-only`) | 15 files, 144 symbols, **risk critical**, 128 affected flows: the shared symbols `apiError`, `auditTarget`, `redact` and `runController` are on many flows. Expected for an additive change; the full suite passes |

Smoke test with the real built binary (Controller on `127.0.0.1`, temp data dir) against a throwaway
fake Cloudflare and two fake IP providers in `/tmp` (not in the repo): login; zones and records
dropdown calls; create with `record_id`; the first scheduled run set the record (`updated`) and wrote
`detected_ip`/`dns_value`/`last_checked_at`; a provider IP change was pushed within one interval;
disagreeing providers gave the quorum failure and backoff (attempts at 5 s steps, then a 15 s gap,
`ddns_sync_history` rows `failed`); manual sync gave `failed`, then `updated`, then `unchanged`; PATCH
variants (rename and ttl, `zone_id` alone `400`, mismatching or unknown `record_id` `400`, record
switch with a new record, disable/enable, interval 1 `400`, bad and good token) behaved as specified;
duplicate create `400`; `DELETE` `204` then `404` and the history was removed; `GET /activity?category=ddns`
showed create, update, sync and delete; `GET /system/metrics` matched `sysctl`/`df` (16 GiB, 8 cores,
free disk) and `top` in order of magnitude; `401` without a token. The fake token and the provider key
were **absent** from the Controller log, the database files (main, WAL, SHM) and the audit rows.

**Not run:** a real Cloudflare account or the real public-IP providers; the UI in a browser; the
metrics on Linux or Windows (only the cross build); the shipped example config as a live Controller
(only `TestShippedConfigFilesAreValid`); long-running stability.

## Known gaps / follow-ups

- One live DDNS run against Cloudflare and the real providers, and a metrics run on Linux (roadmap Phase 4).
- The 5 s syncer tick rounds short backoff steps up; no endpoint lists `ddns_sync_history`.
- Android metrics callback stays Phase 5 (`mobile`/`app.Options` untouched; the Android build uses gopsutil and restricts what `/proc` denies).
- The frontend generated types need no regeneration for Phase 4 (spec unchanged); `frontend/` was not touched.
