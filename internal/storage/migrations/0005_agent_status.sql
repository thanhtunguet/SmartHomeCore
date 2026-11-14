-- What the Controller knows about the Agent behind a binding (REQ-API-021, GAP-API-09): the result
-- of its last contact, written by the shutdown dispatcher and by the periodic health check.
-- Additive with defaults, so existing bindings start as 'unknown' until the first contact.
-- status:     ready | unreachable | unknown
-- version:    reported by GET /agent/v1/healthz on the last successful health check ('' = never)
-- last_ok_at: unix ms of the last successful contact (NULL = never)
ALTER TABLE agent_bindings ADD COLUMN status TEXT NOT NULL DEFAULT 'unknown'
    CHECK (status IN ('ready', 'unreachable', 'unknown'));
ALTER TABLE agent_bindings ADD COLUMN version TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_bindings ADD COLUMN last_ok_at INTEGER;
