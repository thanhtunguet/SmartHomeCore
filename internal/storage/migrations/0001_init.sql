-- 0001_init: initial HomeCore Controller schema.
-- Conventions: IDs are TEXT UUIDv7; timestamps are INTEGER unix milliseconds (UTC);
-- booleans are INTEGER 0/1; secrets are BLOBs sealed with the master key (never plaintext).

CREATE TABLE users (
    id            TEXT    PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL CHECK (role IN ('admin', 'viewer')),
    disabled      INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;

-- Only a SHA-256 hash of each refresh token is stored. Tokens rotate on use; reuse of a used
-- token revokes the whole family.
CREATE TABLE refresh_tokens (
    id          TEXT    PRIMARY KEY,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id   TEXT    NOT NULL,
    token_hash  BLOB    NOT NULL UNIQUE,
    expires_at  INTEGER NOT NULL,
    used_at     INTEGER,
    revoked_at  INTEGER,
    created_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX refresh_tokens_user ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family ON refresh_tokens (family_id);

CREATE TABLE devices (
    id                       TEXT    PRIMARY KEY,
    name                     TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    description              TEXT    NOT NULL DEFAULT '',
    hostname                 TEXT    NOT NULL DEFAULT '',
    ip_address               TEXT    NOT NULL,
    mac_address              TEXT    NOT NULL DEFAULT '',
    wol_broadcast_address    TEXT    NOT NULL DEFAULT '',
    wol_port                 INTEGER NOT NULL DEFAULT 9 CHECK (wol_port BETWEEN 1 AND 65535),
    monitor_port             INTEGER NOT NULL CHECK (monitor_port BETWEEN 1 AND 65535),
    monitor_interval_seconds INTEGER NOT NULL DEFAULT 30 CHECK (monitor_interval_seconds BETWEEN 5 AND 3600),
    monitor_timeout_ms       INTEGER NOT NULL DEFAULT 2000 CHECK (monitor_timeout_ms BETWEEN 100 AND 10000),
    enabled                  INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    status                   TEXT    NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'online', 'offline')),
    last_seen_at             INTEGER,
    last_checked_at          INTEGER,
    created_at               INTEGER NOT NULL,
    updated_at               INTEGER NOT NULL
) STRICT;

-- Device <-> Agent binding (ADR-002). No shared secret: the agent trusts controller public keys.
CREATE TABLE agent_bindings (
    device_id        TEXT    PRIMARY KEY REFERENCES devices (id) ON DELETE CASCADE,
    agent_id         TEXT    NOT NULL UNIQUE,
    port             INTEGER NOT NULL DEFAULT 9750 CHECK (port BETWEEN 1 AND 65535),
    scheme           TEXT    NOT NULL DEFAULT 'http' CHECK (scheme IN ('http', 'https')),
    agent_public_key BLOB,              -- reserved for signed acknowledgements (protocol v2)
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
) STRICT;

-- Controller Ed25519 signing keys (ADR-001). Exactly one row has retired_at IS NULL.
CREATE TABLE controller_keys (
    key_id             TEXT    PRIMARY KEY,
    public_key         BLOB    NOT NULL,
    private_key_sealed BLOB    NOT NULL,
    created_at         INTEGER NOT NULL,
    retired_at         INTEGER
) STRICT;
CREATE UNIQUE INDEX controller_keys_one_active ON controller_keys ((1)) WHERE retired_at IS NULL;

-- Status transitions only (not every probe).
CREATE TABLE device_status_history (
    id              INTEGER PRIMARY KEY,
    device_id       TEXT    NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    status          TEXT    NOT NULL CHECK (status IN ('unknown', 'online', 'offline')),
    previous_status TEXT             CHECK (previous_status IN ('unknown', 'online', 'offline')),
    reason          TEXT    NOT NULL DEFAULT '',
    changed_at      INTEGER NOT NULL
) STRICT;
CREATE INDEX device_status_history_device ON device_status_history (device_id, changed_at DESC);

-- Wake/shutdown commands (brief: command_history). State machine: docs/design/architecture.md §8.2.
CREATE TABLE commands (
    id              TEXT    PRIMARY KEY,
    device_id       TEXT    NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    type            TEXT    NOT NULL CHECK (type IN ('wake', 'shutdown')),
    status          TEXT    NOT NULL CHECK (status IN ('pending', 'sent', 'acknowledged', 'completed', 'failed', 'timed_out')),
    requested_by    TEXT             REFERENCES users (id) ON DELETE SET NULL,
    error_code      TEXT    NOT NULL DEFAULT '',
    error           TEXT    NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    deadline_at     INTEGER,
    sent_at         INTEGER,
    acknowledged_at INTEGER,
    completed_at    INTEGER,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
) STRICT;
CREATE INDEX commands_device ON commands (device_id, created_at DESC);
CREATE INDEX commands_created ON commands (created_at DESC);
-- At most one active command per (device, type): enforces ADR-003 dedup at the DB level.
CREATE UNIQUE INDEX commands_one_active ON commands (device_id, type)
    WHERE status IN ('pending', 'sent', 'acknowledged');

CREATE TABLE audit_log (
    id            INTEGER PRIMARY KEY,
    at            INTEGER NOT NULL,
    actor_user_id TEXT,
    actor         TEXT    NOT NULL DEFAULT '',
    action        TEXT    NOT NULL,
    target_type   TEXT    NOT NULL DEFAULT '',
    target_id     TEXT    NOT NULL DEFAULT '',
    result        TEXT    NOT NULL DEFAULT '',
    remote_addr   TEXT    NOT NULL DEFAULT '',
    details       TEXT    NOT NULL DEFAULT '{}'
) STRICT;
CREATE INDEX audit_log_at ON audit_log (at DESC);

CREATE TABLE ddns_configs (
    id               TEXT    PRIMARY KEY,
    name             TEXT    NOT NULL,
    zone_id          TEXT    NOT NULL,
    record_name      TEXT    NOT NULL,
    record_type      TEXT    NOT NULL CHECK (record_type IN ('A', 'AAAA')),
    proxied          INTEGER NOT NULL DEFAULT 0 CHECK (proxied IN (0, 1)),
    ttl              INTEGER NOT NULL DEFAULT 1,
    interval_seconds INTEGER NOT NULL DEFAULT 300,
    enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    api_token_sealed BLOB    NOT NULL,
    cf_record_id     TEXT    NOT NULL DEFAULT '',
    last_ip          TEXT    NOT NULL DEFAULT '',
    last_synced_at   INTEGER,
    last_error       TEXT    NOT NULL DEFAULT '',
    last_error_at    INTEGER,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    UNIQUE (zone_id, record_name, record_type)
) STRICT;

CREATE TABLE ddns_sync_history (
    id        INTEGER PRIMARY KEY,
    config_id TEXT    NOT NULL REFERENCES ddns_configs (id) ON DELETE CASCADE,
    result    TEXT    NOT NULL CHECK (result IN ('updated', 'unchanged', 'failed')),
    ip        TEXT    NOT NULL DEFAULT '',
    error     TEXT    NOT NULL DEFAULT '',
    at        INTEGER NOT NULL
) STRICT;
CREATE INDEX ddns_sync_history_config ON ddns_sync_history (config_id, at DESC);

-- Key/value settings. Secret values (e.g. the JWT signing key) are sealed.
CREATE TABLE system_settings (
    key        TEXT    PRIMARY KEY,
    value      BLOB    NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
