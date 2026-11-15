-- Alert channels: destinations for the owner's alerts (IP changed, power command, unexpected status
-- change). One row per destination, served by an adapter named in `type`.
--
-- type:          adapter name ('telegram'; more later). Not a CHECK: a new adapter needs no migration,
--                the service validates the name against the adapters it knows.
-- events:        JSON array of the events this channel receives (ip_changed, power_command,
--                status_changed)
-- settings:      JSON object of non-secret string settings (telegram: target_user_id)
-- secret_sealed: the adapter's one secret (telegram: the bot token), sealed with the master key
--                (AAD "alert_channels.secret")
-- last_sent_at:  unix ms of the last successful delivery (NULL = never)
-- last_error:    reason of the last failed delivery, '' after a success (never contains the secret)
-- last_error_at: unix ms of that failure (NULL = none)
CREATE TABLE alert_channels (
    id            TEXT    PRIMARY KEY,
    name          TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    type          TEXT    NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    events        TEXT    NOT NULL,
    settings      TEXT    NOT NULL DEFAULT '{}',
    secret_sealed BLOB    NOT NULL,
    last_sent_at  INTEGER,
    last_error    TEXT    NOT NULL DEFAULT '',
    last_error_at INTEGER,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX alert_channels_name ON alert_channels (name COLLATE NOCASE);
