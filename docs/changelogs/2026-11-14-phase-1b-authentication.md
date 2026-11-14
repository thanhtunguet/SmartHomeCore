# 2026-11-14 — Phase 1b: authentication (argon2id, JWT, refresh rotation, `homecore user`)

| | |
|---|---|
| Author | Claude Code agent session (delegated implementation of roadmap Phase 1b) |
| Scope | `internal/auth`, `internal/audit`, `internal/platform/tty`, `internal/api` (+ `handlers/handlers_auth.go`), `internal/app`, `internal/config`, `cmd/homecore`, `internal/storage/migrations/0004`, `configs/`, `go.mod`, `README.md`, `deploy/README.md`, `docs/` |
| Requirements | `REQ-AUTH-001..018` added (new [authentication.md](../requirements/authentication.md)); `REQ-API-007`, `REQ-API-022` Implemented; `REQ-API-012` verified for `/auth/*`; `REQ-SEC-003/005` partly implemented; `REQ-RT-011` added |
| Decisions | [ADR-013](../design/decisions.md#adr-013--authentication-implementation-argon2id-stdlib-hs256-jwt-refresh-grace-window) |

No change to `api/openapi.yaml` (the handlers implement the existing contract); `make generate` leaves `internal/api/gen` unchanged.

## Added
- `internal/auth`: argon2id hasher (parameters in the encoded hash, upgrade on login, 256-character limit, 12-character minimum when setting, at most 2 concurrent hashes); HS256 JWT issuer/verifier with strict `alg` pinning (stdlib only); `Service` (login, refresh with rotation, family reuse detection and a 10 s grace window, logout, maintenance job); `Accounts` (create, change password, list); SQLite `Store`.
- JWT signing key generated at first start, sealed in `system_settings` (`auth.jwt_signing_key`).
- Migration `0004_refresh_grace.sql`: `refresh_tokens.successor_sealed` (sealed copy of the pair issued at rotation, wiped after the window).
- `internal/audit.Writer` (`Write`, `WriteTx`), reusable for Phase 3; written for `auth.login`, `auth.logout`, `auth.refresh_reuse`, `user.create`, `user.password_change`.
- Handlers `Login`, `RefreshToken`, `Logout`, `GetCurrentUser` in `internal/api/handlers/handlers_auth.go` (answer `501` when no `auth.Service` is wired, so existing tests keep their meaning).
- `homecore user add|passwd|list` (`cmd/homecore/user.go`); password from `HOMECORE_PASSWORD`, stdin, or a no-echo prompt (`internal/platform/tty`, `golang.org/x/sys` only).
- Startup warning when the Controller has no accounts.
- `api.Deps.Auth`; `auth.Service` is the Authenticator when none is given.
- Dependency: `golang.org/x/crypto` v0.58.0 (approved); `golang.org/x/sys` is now a direct dependency (was indirect); `go mod` also bumped the indirect `golang.org/x/text` v0.40.0 to v0.43.0.

## Changed
- `internal/app` wires the auth service (replaces `auth.RejectAll`, which stays as the fail-closed default of `api.New`) and runs its maintenance job in the Controller's errgroup.
- `cmd/homecore/main.go`: config loading extracted into `loadConfig`, shared by the server and the `user` command.
- `REQ-API-007` Partial -> Implemented, `REQ-API-022` Planned -> Implemented; roadmap Phase 1b ticked; report GAP-BE-03 and GAP-INF-01 closed at the API level (§11 of the report).

## Removed
- Config key `controller.auth.bootstrap_admin_user` (struct field, default, `configs/homecore.example.yaml`). A config file that still sets it now fails strict validation (`unknown field`). There were no `HOMECORE_ADMIN_PASSWORD` mentions left in the docs besides the roadmap item, which is rewritten. `configs/dev.yaml` never had the key.

## Security
- Passwords: argon2id only; no plaintext anywhere (tests assert hash, DB and audit log contain none; the CLI never echoes it).
- Refresh tokens: SHA-256 at rest; the grace-window copy is sealed and wiped after 10 s.
- Login failures are indistinguishable (body and cost); the audit log never records an unknown username.
- Accepted: access tokens outlive logout and a password change by up to 15 min; a thief replaying a just-rotated refresh token within 10 s gets the same pair as the legitimate client.

## Verification
Run on 2026-11-14 (macOS, Go 1.27.2):

| Command | Result |
|---|---|
| `make test` | pass, all packages |
| `make test-race` | pass, all packages |
| `go test -race -count=3 ./internal/auth/` | pass |
| `make lint` | `go vet` and the gofmt check pass; `golangci-lint` is not installed, skipped |
| `make cross` | 7 targets built (linux amd64/arm64/arm, windows/amd64, darwin arm64/amd64, android/arm64) |
| `make generate` + `git diff --exit-code internal/api/gen` | unchanged |
| `GOOS=linux,windows,darwin,android go vet ./internal/platform/tty/` | pass for each |
| `node .gitnexus/run.cjs detect-changes --scope all --repo .` | risk **critical** (changes to `run`, `runController`, `Deps`, `Handlers`; expected: composition root and `main`); index re-built first |
| Smoke test, built binary, temp data dir, port 18080, `curl`, Controller running during every step | start: log line "no user accounts exist ... run `homecore user add`". `user add` without password source, with a short password, duplicate: clear errors; `user add` via stdin ok; `user list` shows no hash. Login with wrong password / unknown user: identical `401` body. Login `200` (`token_type: Bearer`, `expires_in: 900`, `Cache-Control: no-store`, header `{"alg":"HS256","typ":"JWT"}`, claims `sub,username,iat,exp,jti`). `GET /auth/me` `200`. `POST /devices` with token `501` (envelope), without token `401`. Refresh twice with one token within 10 s: **same** refresh and access token. Replay after 11 s: `401`, successor also `401` (family revoked). Logout `204`, then refresh with that token `401`. `user passwd` (via `HOMECORE_PASSWORD`) while running: old session `401`, old password `401`. `429` after 5-6 rapid `/auth/*` calls from one IP. `audit_log` holds `user.create`, `auth.login` ok/failed (unknown user stored without name), `auth.refresh_reuse`, `auth.logout`; no password or token in `server.log` (grep). SIGTERM: clean stop |
| Interactive prompt, via a Python `pty` | two prompts, password never present in the terminal output, account created |

Not run: the UI in a browser against the real Controller (no browser-free way was tried; the checks above are the HTTP contract only); two real browser tabs for the grace window; the Windows console path of `tty` (only compiles via `make cross`); Ctrl-C during the prompt; `golangci-lint`; the startup "no accounts" warning has no automated test; Android (`mobile` compiles, nothing else).

## Known gaps / follow-ups
- No `user disable|enable|delete` command (`users.disabled` is honoured but only settable with SQL).
- Access tokens are not revoked at logout / password change (≤ 15 min).
- `refresh` and `logout` share the 10/min, burst 5 per-IP bucket of `POST /auth/*`; one NAT with several tabs can hit `429`.
- Creating accounts on Android: `mobile` exposes nothing; decide with Phase 5 (suggested: a `mobile.AddUser(username, password)` calling `auth.Accounts.Create`).
- Grace window is a constant (`auth.DefaultRefreshGrace`), not a config key.
