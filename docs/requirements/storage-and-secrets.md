# Requirements: Storage (SQLite) and Secrets at Rest

| | |
|---|---|
| Brief | §3.7, §6 (protection of keys and tokens) |
| Code | `internal/storage`, `internal/storage/migrations`, `internal/secrets` |
| Design | [database.md](../design/database.md), [architecture §6.8–6.9](../design/architecture.md), [ADR-004](../design/decisions.md#adr-004--sqlite-driver-moderncorgsqlite-everywhere) |
| Status | **Implemented** (storage layer + schema); domain repositories planned |

## Storage

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-ST-001 | The Controller keeps all state in one embedded SQLite file, `<data_dir>/homecore.db`, using the pure-Go driver `modernc.org/sqlite` (no CGO, no DB server). The file is created with mode `0600` inside a `0700` directory. | Implemented | Manual permission check 2026-11-11 |
| REQ-ST-002 | Every connection uses `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL`, and `BEGIN IMMEDIATE` transactions (`_txlock=immediate`). Pool: 4 connections. Open fails if the DB cannot be pinged within 5 s. | Implemented | `TestMigrateIsIdempotentAndEnablesPragmas` (WAL, foreign keys) |
| REQ-ST-003 | Schema changes are versioned SQL files embedded from `internal/storage/migrations/NNNN_description.sql`. At startup all pending migrations run in order, each in its own transaction, and are recorded in `schema_migrations`. Re-running is a no-op. Bad names and duplicate versions are rejected. | Implemented | `TestMigrateIsIdempotentAndEnablesPragmas` |
| REQ-ST-004 | Multi-statement changes use `DB.WithTx`, which commits on success and rolls back on error or panic. | Implemented | Exercised by the migration runner |
| REQ-ST-005 | The schema enforces integrity in the DB itself: at most one active (`pending`/`sent`/`acknowledged`) command per device and type; at most one active Controller signing key; enum `CHECK`s; port/interval ranges; FK cascades. | Implemented | `TestActiveCommandUniqueness` |
| REQ-ST-006 | `DB.Backup(ctx, dst)` writes a consistent online snapshot with `VACUUM INTO` and refuses to overwrite an existing file. | Implemented (untested); no API yet | — |
| REQ-ST-007 | Conventions: STRICT tables; IDs TEXT UUIDv7; timestamps INTEGER unix milliseconds UTC (`storage.Millis` / `FromMillis`); booleans INTEGER 0/1. | Implemented | Schema review |

## Secrets at rest

| ID | Requirement | Status | Verified by |
|----|-------------|--------|-------------|
| REQ-SEC-001 | A 32-byte master key is resolved in this order: `HOMECORE_MASTER_KEY` (base64) → key supplied by the host (Android Keystore through `mobile.Start`) → `<data_dir>/master.key`. The key file is created exclusively, mode `0600`, and fsynced. A malformed key stops startup. | Implemented | `TestMasterKeyPersistsWithStrictPermissions` |
| REQ-SEC-002 | Secrets are sealed with AES-256-GCM as `nonce ‖ ciphertext`, with associated data naming the column (e.g. `ddns_configs.api_token`). Opening fails on tampering or wrong associated data. | Implemented | `TestSealOpenBindsAssociatedData` |
| REQ-SEC-003 | Secret material is stored only in sealed BLOB columns: `controller_keys.private_key_sealed`, `ddns_configs.api_token_sealed`, secret entries of `system_settings`. Refresh tokens are stored only as SHA-256 hashes. | **Partial.** Schema implemented; writers (auth, key management, DDNS) planned | Schema review |
| REQ-SEC-004 | API responses never contain secrets: devices have no secret fields; the DDNS `api_token` is `writeOnly` and reported only as `has_api_token`. | Implemented (spec) | `api/openapi.yaml` |
| REQ-SEC-005 | The master key, the JWT signing key and the Controller Ed25519 signing key are distinct keys. | **Partial.** Master key exists; the other two are created when auth / key management land | Design |

## Known gaps

- Domain repositories (devices, commands, users, DDNS) do not exist yet; the tables are unused.
- `Backup` has no test and no admin endpoint.
- Retention pruning (`retention_days`) is configured but not implemented.
- SQLite has not been run on a real Android device (Phase 0 spike).
