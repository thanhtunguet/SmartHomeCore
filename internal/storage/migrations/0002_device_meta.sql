-- Device kind and OS (informational, used by clients for icons/labels) and the latest probe
-- latency. All additive with defaults, so existing rows and clients are unaffected.
ALTER TABLE devices ADD COLUMN device_type TEXT NOT NULL DEFAULT 'desktop'
    CHECK (device_type IN ('desktop', 'server', 'laptop', 'tv', 'nas', 'iot', 'router'));
ALTER TABLE devices ADD COLUMN os TEXT NOT NULL DEFAULT 'linux'
    CHECK (os IN ('windows', 'linux', 'macos', 'android', 'freebsd', 'embedded'));
ALTER TABLE devices ADD COLUMN last_latency_ms INTEGER;
