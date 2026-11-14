# Requirements: Authentication and Accounts

| | |
|---|---|
| Brief | §3.1 (JWT), §6 (public API zone, secrets), §6.6 |
| Code | `internal/auth` (`password.go`, `jwt.go`, `service.go`, `store.go`, `accounts.go`), `internal/audit`, `internal/api/handlers/handlers_auth.go`, `cmd/homecore/user.go`, `internal/platform/tty`, migration `0004` |
| Design | [architecture §10.1](../design/architecture.md#101-authentication-design), [database.md](../design/database.md) (`users`, `refresh_tokens`, `system_settings`, `audit_log`), [ADR-011](../design/decisions.md#adr-011--no-roles-every-authenticated-user-is-equal), [ADR-013](../design/decisions.md#adr-013--authentication-implementation-argon2id-stdlib-hs256-jwt-refresh-grace-window) |
| Last updated | 2026-11-14 (Phase 1b) |
| Status | **Implemented** |

No roles: every authenticated user is equal (ADR-011). Accounts are created with the
`homecore user` command, never by the API or the UI, and there is no default account.

## Passwords

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-AUTH-001 | Passwords are hashed with argon2id (`golang.org/x/crypto/argon2`) with a random 16-byte salt and stored as a PHC-style string `$argon2id$v=19$m=…,t=…,p=…$salt$key` in `users.password_hash`. Default cost `m=32 MiB, t=3, p=1`, 32-byte key (sized for a Raspberry Pi 4 / TV box; see [ADR-013](../design/decisions.md#adr-013--authentication-implementation-argon2id-stdlib-hs256-jwt-refresh-grace-window)). | Implemented | `TestHashVerifyRoundTrip`, `TestDefaultParamsAreSane` |
| REQ-AUTH-002 | Verification parses the parameters from the stored string, compares in constant time, treats any malformed or out-of-bounds hash (wrong algorithm/version, zero or huge parameters, bad base64, short key) as a non-match, and reports when a correct password's hash uses other parameters than the current ones; a successful login then re-hashes it. Parameters can therefore be raised later without a migration. | Implemented | `TestVerifyFlagsParameterUpgrade`, `TestVerifyRejectsMalformedHashes`, `TestLoginRehashesOutdatedParameters` |
| REQ-AUTH-003 | Passwords are at most 256 characters (the OpenAPI limit; counted in characters; longer input is rejected before hashing). A password being **set** must have at least 12 characters (`auth.MinPasswordLength`); the minimum is not applied at login. At most 2 argon2 computations run at once (`auth.MaxConcurrent`), waiting requests honour their context. | Implemented | `TestPasswordLengthLimits`, `TestHashHonoursCancelledContext`, `TestLoginFailuresAreIndistinguishable` |

## Access tokens

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-AUTH-004 | The JWT signing key (32 random bytes) is generated on first start, stored sealed (AES-GCM, associated data `system_settings.auth.jwt_signing_key`) in `system_settings`, and reused on every later start. It is independent of the master key and of the Controller Ed25519 key. If it cannot be opened (master key changed) startup fails with an explicit message instead of silently minting a new key. | Implemented | `TestSigningKeyIsGeneratedOnceSealedAndDistinctFromMasterKey` |
| REQ-AUTH-005 | Access tokens are HS256 JWTs with claims `sub` (user id), `username`, `iat`, `exp`, `jti`, lifetime `controller.auth.access_token_ttl` (default 15 min). Verification pins the algorithm: the header must be exactly `alg=HS256` with `typ` `JWT` or absent and no other members; `none`, other algorithms, `kid`/`crit`/unknown header members are rejected. The signature is checked in constant time before the payload is parsed; missing `sub`/`exp`/`jti` is rejected. `exp` is strict (no leeway); `iat` may be at most 1 minute ahead of the clock (`auth.ClockLeeway`). | Implemented | `TestJWTIssueAndAuthenticate`, `TestJWTExpiry`, `TestJWTClockLeewayOnIssuedAt`, `TestJWTRejectsTamperingAndWrongKey`, `TestJWTStrictAlgorithmPinning`, `TestJWTRejectsIncompleteClaims`, `TestAccessTokenExpiryAndRefresh` (fake clock, through the router) |
| REQ-AUTH-006 | The user is looked up on every authenticated request: a disabled or deleted account is rejected at once, not when its token expires. | Implemented | `TestJWTDisabledOrDeletedUserIsRejected`, `TestDisabledUserLosesAccessImmediately` |

## Login, refresh, logout, me

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-AUTH-007 | `POST /auth/login` returns `{access_token, refresh_token, token_type: "Bearer", expires_in}` with `Cache-Control: no-store`. Wrong password, unknown user and disabled account all return the same `401` (`unauthorized`, "invalid username or password") and each costs one argon2 verification (a dummy hash stands in for an unknown user). Malformed input (bad JSON, empty or over-long username/password) is `400 bad_request`. The per-IP limit on `POST /auth/*` (10/min, burst 5) applies: excess is `429 rate_limited`. | Implemented | `TestLoginAndMe`, `TestLoginFailureIsGeneric401`, `TestLoginRejectsMalformedRequests`, `TestLoginRateLimit`, `TestUnknownUserStillVerifiesAHash` |
| REQ-AUTH-008 | Refresh tokens are opaque (32 random bytes, base64url). Only their SHA-256 is stored (`refresh_tokens.token_hash`). Every `POST /auth/refresh` rotates: the presented token is marked used and a new access + refresh token (same family) is issued; the new refresh token lives `refresh_token_ttl` (default 30 d, sliding). Unknown, expired, revoked or disabled-user tokens give `401` with the standard envelope. | Implemented | `TestRefreshRotation`, `TestRefreshTokensAreStoredOnlyAsHashes`, `TestRefreshExpiryAndRevokedTokens`, `TestRefreshRejectsDisabledUserAndRevokesFamily`, `TestRefreshRejectsBadInput` |
| REQ-AUTH-009 | **Reuse detection.** Presenting a refresh token that was already rotated, after the grace window, revokes its whole family (every token of that login, including the legitimate successor), returns `401` and writes an `auth.refresh_reuse` audit row. Concurrent requests with one token are serialised by the database. | Implemented | `TestRefreshReuseOutsideGraceRevokesFamily`, `TestRefreshGraceWindowAndReuseThroughRouter`, `TestConcurrentRefreshWithSameTokenYieldsOneSuccessor` |
| REQ-AUTH-010 | **Grace window** (10 s, `auth.DefaultRefreshGrace`; GAP-INF-01): a rotated token presented again within the window gets the already issued successor pair (same refresh token, same access token, `expires_in` = remaining lifetime) instead of revoking the family. The pair is kept on the used row sealed with the master key (`refresh_tokens.successor_sealed`) and wiped as soon as the window has passed (on the next rotation of the family and by the maintenance job), so no plaintext refresh token is recoverable from the database for longer than the window. | Implemented | `TestRefreshReuseInsideGraceReturnsSamePair`, `TestGraceWindowBoundary`, `TestGraceWindowCanBeDisabled`, `TestSealedSuccessorIsWipedAfterTheWindow`, `TestSealedSuccessorIsWipedInlineOnNextRotation`, `TestRefreshTokensHaveNullableSuccessorColumn`, router: `TestRefreshGraceWindowAndReuseThroughRouter` |
| REQ-AUTH-011 | `POST /auth/logout` (bearer required) revokes the family of the given refresh token, if it belongs to the caller; it is idempotent (`204`, also for unknown or foreign tokens, which are ignored). Other sessions of the user are unaffected. The caller's access token stays valid until it expires (stateless). | Implemented | `TestLogoutRevokesFamily`, `TestLogoutIgnoresAnotherUsersToken`, `TestSessionsAreIndependentFamilies`, `TestLogoutThroughRouter` |
| REQ-AUTH-012 | `GET /auth/me` returns `{id, username}` of the authenticated user (no hash, no role). | Implemented | `TestLoginAndMe` |
| REQ-AUTH-013 | A maintenance job (`auth.Service.Run`, every minute, also at start) wipes expired sealed successor copies and deletes refresh rows that expired or were revoked more than 24 h ago, or were rotated more than 7 days ago. | Implemented | `TestMaintainPurgesOldRows`, `TestSealedSuccessorIsWipedAfterTheWindow` |

## Audit

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-AUTH-014 | `internal/audit.Writer` appends to `audit_log` (`Write`, or `WriteTx` inside a transaction); it is the writer Phase 3 reuses for shutdown. Authentication writes `auth.login` (`ok`/`failed`, client IP, reason `bad_password`/`unknown_user`/`disabled` in details), `auth.logout`, `auth.refresh_reuse`, and the CLI writes `user.create` and `user.password_change` (actor `cli`). Details never contain passwords or tokens. A failed login records the submitted username **only if it names an existing account** (people type passwords into the username field); unknown names are not stored. A failing audit write is logged, never turns a login into an error. | Implemented | `TestAuditRows`, `TestLoginWritesAuditRows`, `TestWriteStoresEntry`, `TestWriteTxRollsBackWithTheAction`, `TestRefreshReuseOutsideGraceRevokesFamily` |

## Account management (CLI)

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-AUTH-015 | `homecore user add USERNAME`, `user passwd USERNAME` and `user list` work directly on `<data_dir>/homecore.db` (same `--config`/`HOMECORE_CONFIG`/`--data-dir`/`HOMECORE_DATA_DIR` handling as the server; the Controller need not run). `add` may create the database; the others fail with a clear message if it does not exist. A locked database fails after the 5 s busy timeout with a message naming the file. Usernames: 1–64 characters of `A-Za-z0-9._@-`, unique ignoring case. | Implemented | `TestUserAddPasswdListEndToEnd`, `TestUserPasswordPolicyAndErrors`, `TestUserCommandsRequireExistingDatabaseExceptAdd`, `TestUserCommandReportsLockedDatabase`, `TestUserDataDirFromEnvironment`, `TestUserFlagsMayComeBeforeOrAfterTheUsername`; smoke test 2026-11-14 (server running concurrently) |
| REQ-AUTH-016 | Password input for `add`/`passwd`, first match wins: environment variable `HOMECORE_PASSWORD`; standard input (one line) when it is not a terminal; otherwise a no-echo prompt on the terminal, asked twice. No-echo uses only `golang.org/x/sys` (`internal/platform/tty`; termios on Linux/Android/macOS/BSD, console mode on Windows). The password is never printed, logged or placed in an error message; minimum 12, maximum 256 characters. | Implemented | `TestUserAddPrompt`, `TestUserAddPasswdListEndToEnd` (`assertNoSecret`), `TestPipesAndFilesAreNotTerminals`; pty smoke test 2026-11-14 (no echo, two prompts); the Windows console path only compiles (`make cross`) |
| REQ-AUTH-017 | `user passwd` replaces the hash and, in the same transaction, revokes all refresh tokens of the user and wipes sealed successor copies. Access tokens already issued remain valid for at most `access_token_ttl`. | Implemented | `TestPasswordChangeRevokesRefreshTokens`, `TestUserAddPasswdListEndToEnd`; smoke test |
| REQ-AUTH-018 | There is no first-run bootstrap account and no default password. The key `controller.auth.bootstrap_admin_user` was removed (a config that still sets it is rejected as an unknown key). A Controller that starts with no accounts runs and logs one warning telling the operator to run `homecore user add <username>`. | Implemented | `TestLoadBytesRejectsUnknownFieldsAndBadValues/removed_bootstrap_key`; startup warning by smoke test 2026-11-14 (no automated test) |

## Known gaps

- No `user disable|enable|delete` command: `users.disabled` is honoured everywhere (REQ-AUTH-006/007/008) but can only be set with SQL for now. Tracked in the roadmap backlog.
- Logout and password change do not invalidate access tokens already issued (up to 15 min); only refresh tokens are revoked.
- Rate limiting is per client IP only (no per-account lockout), and `refresh`/`logout` share the 10/min, burst 5 bucket of `POST /auth/*`; clients behind one NAT share it.
- Passwords are not Unicode-normalised (compared as the bytes the client sent).
- Creating accounts on Android (no CLI) is not designed; see roadmap Phase 5.
- The signing key cannot be rotated without signing everybody out (delete the `auth.jwt_signing_key` row of `system_settings`).
