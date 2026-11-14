-- DDNS observed state (REQ-API-020, GAP-API-03): what the last discovery run saw, separately from
-- what the Controller last pushed (`last_ip`). Additive with defaults, so existing rows start empty.
-- detected_ip:     public address of the most recent successful discovery ('' = none yet)
-- dns_value:       content of the Cloudflare record as last read ('' = never read)
-- last_checked_at: unix ms of the last run, whether or not it changed the record (NULL = never)
ALTER TABLE ddns_configs ADD COLUMN detected_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE ddns_configs ADD COLUMN dns_value TEXT NOT NULL DEFAULT '';
ALTER TABLE ddns_configs ADD COLUMN last_checked_at INTEGER;

-- One configuration per Cloudflare record: two syncers fighting over one record id would flap it.
-- (zone_id, record_name, record_type) is already unique; this covers the id chosen from the lookup.
-- Rows that predate the required record_id have '' and are exempt.
CREATE UNIQUE INDEX ddns_configs_record ON ddns_configs (zone_id, cf_record_id) WHERE cf_record_id <> '';
