-- Port forwarding rules (ADR-022): the Controller listens on listen_address:listen_port and relays
-- TCP connections and/or UDP datagrams to target_host:target_port.
--
-- protocol:       'tcp', 'udp' or 'universal' (TCP and UDP on the same port)
-- listen_address: IP literal of a local interface; '0.0.0.0' = every IPv4 interface, '::' = every
--                 interface. Whether it exists on this host is decided when the rule starts, not here
-- target_host:    IP literal or host name, resolved each time a connection is opened
-- enabled:        the on/off switch. It is the *desired* state: on start-up the Controller runs every
--                 enabled rule again, and retries one that could not bind. Whether a listener is open
--                 right now is runtime state and is not stored
CREATE TABLE port_forward_rules (
    id             TEXT    PRIMARY KEY,
    name           TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    protocol       TEXT    NOT NULL CHECK (protocol IN ('tcp', 'udp', 'universal')),
    listen_address TEXT    NOT NULL DEFAULT '0.0.0.0',
    listen_port    INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
    target_host    TEXT    NOT NULL CHECK (length(target_host) BETWEEN 1 AND 253),
    target_port    INTEGER NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
    enabled        INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX port_forward_rules_name ON port_forward_rules (name COLLATE NOCASE);
