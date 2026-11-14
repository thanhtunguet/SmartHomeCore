-- Service account keys (ADR-018): long-lived, individually revocable credentials for the legacy
-- endpoints and AI-agent (MCP) integrations. The Controller generates an Ed25519 key pair per key
-- and keeps the private key sealed with the master key (AAD "service_account_keys.private_key:<id>");
-- the credential handed to the client is a JWT signed with it, so it is derivable from this row and
-- never stored. Actions are attributed to the owning user.
--
-- id:            UUIDv7; also the JWT `kid` and `jti`
-- scope:         what the key may call; enforced from the OpenAPI `x-token-scopes` (legacy | mcp)
-- ttl_seconds:   lifetime requested at creation; a rotation reuses it unless it asks for another
-- expires_at:    unix ms, immutable end of life
-- revoked_at:    unix ms when the owner revoked it (NULL = not revoked)
-- superseded_at: unix ms when a rotation replaced it (NULL = current). The key keeps working for a
--                short grace window afterwards (servicekey.RotationGrace), then is dead.
-- rotated_to:    id of the successor created by that rotation (no FK: pruning may delete either row)
-- last_used_at:  unix ms of the last authenticated request, refreshed at most once a minute
CREATE TABLE service_account_keys (
    id                 TEXT    PRIMARY KEY,
    user_id            TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name               TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    scope              TEXT    NOT NULL CHECK (scope IN ('legacy', 'mcp')),
    public_key         BLOB    NOT NULL,
    private_key_sealed BLOB    NOT NULL,
    ttl_seconds        INTEGER NOT NULL CHECK (ttl_seconds > 0),
    created_at         INTEGER NOT NULL,
    expires_at         INTEGER NOT NULL,
    revoked_at         INTEGER,
    superseded_at      INTEGER,
    rotated_to         TEXT,
    last_used_at       INTEGER
) STRICT;
CREATE INDEX service_account_keys_user ON service_account_keys (user_id, created_at);
