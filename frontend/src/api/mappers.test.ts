import { describe, expect, it } from 'vitest';
import {
  agentState,
  buildDdnsCreate,
  buildDdnsUpdate,
  cloudflareLookup,
  type DdnsDraft,
  commandToActivity,
  needsAgentRemoval,
  normalizeMac,
  presetForPort,
  recordToDdnsFields,
  toDDNSConfig,
  toDevice,
  toDeviceCreate,
  toDeviceUpdate,
  toSystemMetrics,
} from './mappers';
import type { ApiCommand, ApiDdnsConfig, ApiDevice } from './schema';

const device = (extra: Partial<ApiDevice> = {}): ApiDevice => ({
  id: 'd1',
  name: 'NAS',
  ip_address: '10.0.0.2',
  monitor_port: 22,
  monitor_interval_seconds: 30,
  monitor_timeout_ms: 2000,
  enabled: true,
  status: 'online',
  power_transition: 'none',
  created_at: '2026-11-01T00:00:00Z',
  updated_at: '2026-11-01T00:00:00Z',
  ...extra,
});

describe('toDevice', () => {
  it('copes with every optional field absent (older or minimal Controller)', () => {
    const d = toDevice(device());
    expect(d).toMatchObject({
      description: '',
      type: 'desktop',
      os: 'linux',
      mac: '',
      lastSeen: 'Never',
      monitoring: { lastLatencyMs: undefined, lastCheckedAt: undefined, protocolPreset: 'SSH' },
      wol: { enabled: false, macAddress: '', broadcastAddress: '', port: 9 },
      shutdown: { enabled: false, agentId: '', agentPort: 9750, agentScheme: 'http', agentStatus: 'not_configured' },
    });
  });

  it('upper-cases the MAC and enables WOL exactly when a MAC exists', () => {
    const d = toDevice(device({ mac_address: 'aa:bb:cc:dd:ee:ff', wol_port: 7, wol_broadcast_address: '10.0.0.255' }));
    expect(d.mac).toBe('AA:BB:CC:DD:EE:FF');
    expect(d.wol).toEqual({ enabled: true, macAddress: 'AA:BB:CC:DD:EE:FF', broadcastAddress: '10.0.0.255', port: 7 });
  });

  it('uses stored type/os and prefers a session probe latency over the stored one', () => {
    const d = toDevice(device({ device_type: 'nas', os: 'freebsd', last_latency_ms: 40 }), 12);
    expect(d).toMatchObject({ type: 'nas', os: 'freebsd', monitoring: { lastLatencyMs: 12 } });
    expect(toDevice(device({ last_latency_ms: 40 })).monitoring.lastLatencyMs).toBe(40);
  });

  it('keeps the server order of a list (no client-side sorting)', () => {
    const list = [device({ id: 'b', name: 'beta' }), device({ id: 'a', name: 'Alpha' })];
    expect(list.map(x => toDevice(x).id)).toEqual(['b', 'a']);
  });

  it('derives the monitoring preset from the port', () => {
    expect([22, 3389, 80, 443, 9999].map(presetForPort)).toEqual(['SSH', 'RDP', 'HTTP', 'HTTPS', 'CUSTOM']);
  });
});

describe('toDevice hostname (deprecated, shown read-only)', () => {
  it('is empty when the Controller stores none, so the sheet shows N/A', () => {
    expect(toDevice(device()).hostname).toBe('');
    expect(toDevice(device({ hostname: '' })).hostname).toBe('');
  });
  it('carries the stored name through', () => {
    expect(toDevice(device({ hostname: 'tungpt-mint' })).hostname).toBe('tungpt-mint');
  });
  it('is never sent back: create and update bodies do not contain it', () => {
    const d = toDevice(device({ hostname: 'tungpt-mint' }));
    expect(toDeviceCreate(d)).not.toHaveProperty('hostname');
    expect(toDeviceUpdate(d, d)).not.toHaveProperty('hostname');
  });
});

describe('agent status (AgentBinding.status/version/last_ok_at)', () => {
  const agent = { agent_id: 'a1', port: 9750 };

  it('no binding: not configured', () => {
    expect(agentState({ status: 'online' })).toEqual({ agentStatus: 'not_configured', agentStatusSource: 'inferred' });
  });

  it('uses the reported status, version and last contact when present', () => {
    const s = agentState({ status: 'offline', agent: { ...agent, status: 'ready', version: '0.3.1', last_ok_at: '2026-11-14T09:59:00Z' } });
    expect(s).toEqual({ agentStatus: 'ready', agentStatusSource: 'reported', agentVersion: '0.3.1', agentLastOkAt: '2026-11-14T09:59:00Z' });
  });

  it('a reported status wins over the host status', () => {
    expect(agentState({ status: 'online', agent: { ...agent, status: 'unreachable' } }).agentStatus).toBe('unreachable');
    expect(agentState({ status: 'offline', agent: { ...agent, status: 'unknown' } }).agentStatus).toBe('unknown');
  });

  it('absent status falls back to the inference from the host status, marked as inferred', () => {
    expect(agentState({ status: 'online', agent })).toMatchObject({ agentStatus: 'ready', agentStatusSource: 'inferred' });
    expect(agentState({ status: 'offline', agent })).toMatchObject({ agentStatus: 'unreachable', agentStatusSource: 'inferred' });
    expect(agentState({ status: 'unknown', agent })).toMatchObject({ agentStatus: 'unreachable', agentStatusSource: 'inferred' });
  });

  it('flows through toDevice', () => {
    const d = toDevice(device({ agent: { ...agent, scheme: 'https', status: 'ready', version: '1.0.0' } }));
    expect(d.shutdown).toMatchObject({ enabled: true, agentScheme: 'https', agentStatus: 'ready', agentStatusSource: 'reported', agentVersion: '1.0.0' });
  });
});

describe('device form -> request bodies', () => {
  const form = (over: Record<string, unknown> = {}) =>
    ({
      name: ' NAS ',
      ip: ' 10.0.0.2 ',
      description: '',
      type: 'nas',
      os: 'linux',
      monitoring: { enabled: true, port: 22, protocolPreset: 'SSH', intervalSeconds: 30, timeoutMs: 2000 },
      wol: { enabled: false, macAddress: '', broadcastAddress: '', port: 9 },
      shutdown: { enabled: false, agentId: '', agentPort: 9750, agentScheme: 'http', agentStatus: 'not_configured', agentStatusSource: 'inferred' },
      ...over,
    }) as Parameters<typeof toDeviceCreate>[0];

  it('omits blanks so server defaults apply, and never sends read-only agent fields', () => {
    const body = toDeviceCreate(
      form({
        wol: { enabled: true, macAddress: 'AA-BB-CC-DD-EE-FF', broadcastAddress: '', port: 9 },
        shutdown: { enabled: true, agentId: ' a1 ', agentPort: 9750, agentScheme: 'http', agentStatus: 'ready', agentStatusSource: 'reported', agentVersion: '1' },
      }),
    );
    expect(body).toEqual({
      name: 'NAS',
      ip_address: '10.0.0.2',
      monitor_port: 22,
      device_type: 'nas',
      os: 'linux',
      monitor_interval_seconds: 30,
      monitor_timeout_ms: 2000,
      enabled: true,
      mac_address: 'aa:bb:cc:dd:ee:ff',
      wol_port: 9,
      agent: { agent_id: 'a1', port: 9750, scheme: 'http' },
    });
  });

  it('PATCH clears with empty strings; agent removal is a separate call', () => {
    const prev = toDevice(device({ mac_address: 'aa:bb:cc:dd:ee:ff', wol_broadcast_address: '10.0.0.255', description: 'old', agent: { agent_id: 'a1', port: 9750 } }));
    const f = form();
    expect(toDeviceUpdate(f, prev)).toMatchObject({ mac_address: '', description: '' });
    expect(needsAgentRemoval(f, prev)).toBe(true);
    expect(needsAgentRemoval(f, toDevice(device()))).toBe(false);
  });

  it('normalises MAC notations', () => {
    expect(normalizeMac('AA-BB-CC-DD-EE-FF')).toBe('aa:bb:cc:dd:ee:ff');
    expect(normalizeMac('aabbccddeeff')).toBe('aa:bb:cc:dd:ee:ff');
    expect(normalizeMac(' not a mac ')).toBe('not a mac');
  });
});

describe('commandToActivity', () => {
  const c = (over: Partial<ApiCommand>): ApiCommand => ({
    id: 'c1',
    device_id: 'd1',
    type: 'wake',
    status: 'pending',
    created_at: '2026-11-14T10:00:00Z',
    updated_at: '2026-11-14T10:00:05Z',
    ...over,
  });

  it('maps outcome by status', () => {
    expect(commandToActivity(c({ status: 'completed' })).outcome).toBe('success');
    expect(commandToActivity(c({ status: 'failed', error: 'boom' }))).toMatchObject({ outcome: 'error', description: 'boom' });
    expect(commandToActivity(c({ status: 'timed_out' })).outcome).toBe('warning');
    expect(commandToActivity(c({ status: 'sent' })).outcome).toBe('info');
  });

  it('an immediately completed wake/shutdown (already online/offline) reads as success', () => {
    expect(commandToActivity(c({ status: 'completed' })).description).toBe('Device confirmed online by the monitor.');
    expect(commandToActivity(c({ type: 'shutdown', status: 'completed' })).description).toBe('Device confirmed offline by the monitor.');
  });

  it('timestamps by completion, then last update, so newest-first commands sort consistently', () => {
    expect(commandToActivity(c({ status: 'completed', completed_at: '2026-11-14T10:00:09Z' })).timestamp).toBe('2026-11-14T10:00:09Z');
    expect(commandToActivity(c({})).timestamp).toBe('2026-11-14T10:00:05Z');
  });

  it('puts error_code in the raw output and uses the wake -> wol category', () => {
    const a = commandToActivity(c({ status: 'failed', error_code: 'clock_skew', error: 'skew' }), 'NAS');
    expect(a).toMatchObject({ category: 'wol', deviceName: 'NAS', details: { endpoint: '/api/v1/devices/d1/wake', method: 'POST' } });
    expect(a.details?.rawOutput).toContain('error_code=clock_skew');
    expect(commandToActivity(c({ type: 'shutdown' })).category).toBe('shutdown');
  });
});

describe('toDDNSConfig status derivation', () => {
  const cfg = (over: Partial<ApiDdnsConfig> = {}): ApiDdnsConfig => ({
    id: 'x1',
    name: 'home',
    zone_id: 'z1',
    record_id: 'rec-1',
    record_name: 'home.example.com',
    record_type: 'A',
    proxied: false,
    ttl: 1,
    interval_seconds: 300,
    enabled: true,
    has_api_token: true,
    created_at: '2026-11-01T00:00:00Z',
    updated_at: '2026-11-01T00:00:00Z',
    ...over,
  });

  it.each([
    ['never synced', cfg(), false, 'never_synced'],
    ['synced', cfg({ last_synced_at: '2026-11-14T10:00:00Z' }), false, 'synced'],
    ['disabled beats everything', cfg({ enabled: false, last_error: 'x', last_error_at: '2026-11-14T10:00:00Z' }), false, 'disabled'],
    ['error with no sync yet', cfg({ last_error: 'x', last_error_at: '2026-11-14T10:00:00Z' }), false, 'error'],
    ['error newer than the last sync', cfg({ last_synced_at: '2026-11-14T10:00:00Z', last_error: 'x', last_error_at: '2026-11-14T10:05:00Z' }), false, 'error'],
    ['error older than the last sync is stale', cfg({ last_synced_at: '2026-11-14T10:05:00Z', last_error: 'x', last_error_at: '2026-11-14T10:00:00Z' }), false, 'synced'],
    ['updating wins while a sync runs', cfg({ enabled: false, last_error: 'x' }), true, 'updating'],
  ])('%s', (_label, c, updating, expected) => {
    expect(toDDNSConfig(c, updating).status).toBe(expected);
  });

  it('carries record_id and the optional fields through', () => {
    const d = toDDNSConfig(cfg({ record_id: 'r1', detected_ip: '1.2.3.4', dns_value: '1.2.3.3', last_ip: '1.2.3.3' }));
    expect(d).toMatchObject({ recordId: 'r1', zoneId: 'z1', detectedIp: '1.2.3.4', dnsValue: '1.2.3.3', lastIp: '1.2.3.3', hasApiToken: true });
    expect(toDDNSConfig(cfg()).detectedIp).toBeUndefined();
  });
});

describe('Cloudflare lookup helpers', () => {
  it('a typed token wins; else the stored token of the config; else nothing', () => {
    expect(cloudflareLookup(' tok ', { id: 'c1', hasApiToken: true })).toEqual({ api_token: 'tok' });
    expect(cloudflareLookup('', { id: 'c1', hasApiToken: true })).toEqual({ config_id: 'c1' });
    expect(cloudflareLookup('   ', { id: 'c1', hasApiToken: false })).toBeNull();
    expect(cloudflareLookup('', null)).toBeNull();
  });

  it('never yields both credentials (the API rejects that with 400)', () => {
    const l = cloudflareLookup('tok', { id: 'c1', hasApiToken: true })!;
    expect('config_id' in l && 'api_token' in l).toBe(false);
  });

  it('picking a record fills zone, id, name and type together, plus proxied and ttl defaults', () => {
    expect(recordToDdnsFields('z9', { id: 'r1', name: 'home.example.com', type: 'AAAA', content: '::1', proxied: true, ttl: 120 })).toEqual({
      target: { zone_id: 'z9', record_id: 'r1', record_name: 'home.example.com', record_type: 'AAAA' },
      proxied: true,
      ttl: 120,
    });
  });
});

describe('DDNS payload builders: zone and record always travel together', () => {
  const TARGET_KEYS = ['zone_id', 'record_id', 'record_name', 'record_type'];
  const target = { zone_id: 'z1', record_id: 'r1', record_name: 'home.example.com', record_type: 'A' as const };
  const draft = (over: Partial<DdnsDraft> = {}): DdnsDraft => ({
    name: 'home',
    proxied: false,
    ttl: 1,
    interval_seconds: 300,
    enabled: true,
    api_token: 'tok',
    target,
    ...over,
  });

  it('create always carries zone_id, record_id, record_name, record_type and the token', () => {
    const body = buildDdnsCreate(draft());
    for (const k of TARGET_KEYS) expect(body[k as keyof typeof body], k).toBeTruthy();
    expect(body).toEqual({ name: 'home', ...target, proxied: false, ttl: 1, interval_seconds: 300, enabled: true, api_token: 'tok' });
  });

  it('create refuses a draft without a picked record or without a token', () => {
    expect(() => buildDdnsCreate(draft({ target: null }))).toThrow(/zone and a record/i);
    expect(() => buildDdnsCreate(draft({ api_token: '' }))).toThrow(/token/i);
  });

  it('update without a changed record sends none of the four target fields', () => {
    const body = buildDdnsUpdate(draft({ target: null, api_token: '' }));
    for (const k of TARGET_KEYS) expect(body, k).not.toHaveProperty(k);
    expect(body).toEqual({ name: 'home', proxied: false, ttl: 1, interval_seconds: 300, enabled: true }); // no api_token either
  });

  it('update with a changed record sends all four together (never record_id or zone_id alone)', () => {
    const body = buildDdnsUpdate(draft());
    for (const k of TARGET_KEYS) expect(body, k).toHaveProperty(k);
    expect(body).toMatchObject(target);
    expect(body.api_token).toBe('tok');
  });

  it('for every draft, the target fields are either all present or all absent', () => {
    for (const t of [null, target]) {
      const present = TARGET_KEYS.filter(k => k in buildDdnsUpdate(draft({ target: t })));
      expect([0, 4]).toContain(present.length);
    }
  });
});

describe('toSystemMetrics', () => {
  it('copes with a minimal payload', () => {
    const m = toSystemMetrics({ collected_at: '2026-11-14T10:00:00Z', platform: 'linux/arm64' });
    expect(m).toMatchObject({ os: 'linux', arch: 'arm64', localIps: [], restricted: [], isAndroidHost: false, process: {} });
    expect(m.cpuPercent).toBeUndefined();
  });

  it('flags an Android host and passes the optional figures', () => {
    const m = toSystemMetrics({
      collected_at: 't',
      platform: 'android/arm64',
      load_average: [0.1, 0.2, 0.3],
      restricted: ['load_average'],
      memory: { used_bytes: 1, total_bytes: 2 },
      process: { pid: 7 },
    });
    expect(m).toMatchObject({ isAndroidHost: true, restricted: ['load_average'], memoryUsedBytes: 1, memoryTotalBytes: 2, process: { pid: 7 } });
  });
});
