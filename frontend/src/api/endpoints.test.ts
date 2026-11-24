import { beforeEach, describe, expect, it } from 'vitest';
import { apiError, json, mockFetch, mockFetchAll, putSession, stubLocalStorage } from '../test/helpers';
import { createAlertChannel, deleteAlertChannel, listAlertChannels, testAlertChannel, updateAlertChannel, createDevice, createServiceAccountKey, listServiceAccountKeys, revokeServiceAccountKey, listActivity, getDeviceHistory, listCloudflareRecords, listCloudflareZones, listCommands, listDevices, removeDeviceAgent } from './endpoints';

const dev = (id: string, name: string) => ({ id, name });

beforeEach(() => {
  const store = stubLocalStorage();
  putSession(store, { accessToken: 'tok', refreshToken: 'ref', expiresAt: Date.now() + 600_000 });
});

describe('cursor pagination', () => {
  it('follows next_cursor until it is absent and keeps the server order', async () => {
    const { calls } = mockFetch(
      json({ items: [dev('1', 'alpha'), dev('2', 'Bravo')], next_cursor: 'c1' }),
      json({ items: [dev('3', 'charlie')], next_cursor: 'c2' }),
      json({ items: [dev('4', 'delta')] }),
    );

    const all = await listDevices();

    expect(all.map(d => d.id)).toEqual(['1', '2', '3', '4']); // concatenated in page order, not re-sorted
    expect(calls).toHaveLength(3);
    expect(calls[0].query.get('limit')).toBe('200'); // spec maximum
    expect(calls[0].query.has('cursor')).toBe(false);
    expect(calls[1].query.get('cursor')).toBe('c1');
    expect(calls[2].query.get('cursor')).toBe('c2');
  });

  it('stops after one page when there is no next_cursor', async () => {
    const { calls } = mockFetch(json({ items: [dev('1', 'a')] }));
    await listDevices();
    expect(calls).toHaveLength(1);
  });

  it('stops following once `max` items are collected and trims to max', async () => {
    const cmd = (id: string) => ({ id, device_id: 'd', type: 'wake', status: 'completed' });
    const { calls } = mockFetch(
      json({ items: [cmd('1'), cmd('2')], next_cursor: 'c1' }),
      json({ items: [cmd('3'), cmd('4')], next_cursor: 'c2' }),
    );
    const out = await listCommands({ max: 3, deviceId: 'dev-1', status: 'failed' });
    expect(out.map(c => c.id)).toEqual(['1', '2', '3']);
    expect(calls).toHaveLength(2);
    expect(calls[0].query.get('device_id')).toBe('dev-1');
    expect(calls[0].query.get('status')).toBe('failed');
  });

  it('gives up on a cursor loop after 50 pages', async () => {
    const { calls } = mockFetchAll(() => json({ items: [dev('x', 'x')], next_cursor: 'again' }));
    const all = await listDevices();
    expect(calls).toHaveLength(50);
    expect(all).toHaveLength(50);
  });

  it('history respects its own max', async () => {
    mockFetch(json({ items: [{ status: 'online', changed_at: 't1' }, { status: 'offline', changed_at: 't2' }], next_cursor: 'c' }));
    expect(await getDeviceHistory('d1', 1)).toHaveLength(1);
  });
});

describe('listActivity', () => {
  const item = (id: number) => ({ id, at: '2026-11-14T10:00:00Z', category: 'device', action: 'device.update', result: 'ok', message: 'm' });

  it('sends the category and device_id filters and the page limit', async () => {
    const { calls } = mockFetch(json({ items: [item(2), item(1)] }));
    const r = await listActivity({ category: 'device', deviceId: 'dev-1' });
    expect(calls[0].path).toBe('/api/v1/activity');
    expect(calls[0].query.get('category')).toBe('device');
    expect(calls[0].query.get('device_id')).toBe('dev-1');
    expect(calls[0].query.get('limit')).toBe('200');
    expect(r.items.map(i => i.id)).toEqual([2, 1]); // server order kept
    expect(r.more).toBe(false);
  });

  it('omits absent filters', async () => {
    const { calls } = mockFetch(json({ items: [] }));
    await listActivity();
    expect(calls[0].query.has('category')).toBe(false);
    expect(calls[0].query.has('device_id')).toBe(false);
  });

  it('follows next_cursor up to max, trims, and reports that older entries remain', async () => {
    const { calls } = mockFetch(
      json({ items: [item(5), item(4)], next_cursor: 'c1' }),
      json({ items: [item(3), item(2)], next_cursor: 'c2' }),
    );
    const r = await listActivity({ max: 3 });
    expect(r.items.map(i => i.id)).toEqual([5, 4, 3]);
    expect(r.more).toBe(true);
    expect(calls).toHaveLength(2);
    expect(calls[1].query.get('cursor')).toBe('c1');
  });

  it('reads every page when the feed is shorter than max (more = false)', async () => {
    mockFetch(json({ items: [item(3)], next_cursor: 'c1' }), json({ items: [item(2), item(1)] }));
    const r = await listActivity({ max: 50 });
    expect(r.items).toHaveLength(3);
    expect(r.more).toBe(false);
  });
});

describe('request shapes', () => {
  it('removeDeviceAgent is DELETE /devices/{id}/agent', async () => {
    const { calls } = mockFetch(new Response(null, { status: 204 }));
    await removeDeviceAgent('d1');
    expect(`${calls[0].method} ${calls[0].path}`).toBe('DELETE /api/v1/devices/d1/agent');
  });

  it('createDevice posts the body unchanged', async () => {
    const { calls } = mockFetch(json({ id: 'd1' }, { status: 201 }));
    await createDevice({ name: 'NAS', ip_address: '10.0.0.2', monitor_port: 22 });
    expect(calls[0].body).toEqual({ name: 'NAS', ip_address: '10.0.0.2', monitor_port: 22 });
  });

  it('Cloudflare lookups POST the credentials in the body (never the URL) and unwrap items', async () => {
    const { calls } = mockFetch(
      json({ items: [{ id: 'z1', name: 'example.com' }] }),
      json({ items: [{ id: 'r1', name: 'home.example.com', type: 'A', content: '1.2.3.4', proxied: false, ttl: 1 }] }),
    );

    const zones = await listCloudflareZones({ api_token: 'secret-token' });
    const records = await listCloudflareRecords('zone/1', { config_id: 'cfg-1' });

    expect(zones).toEqual([{ id: 'z1', name: 'example.com' }]);
    expect(records[0].content).toBe('1.2.3.4');
    expect(calls[0]).toMatchObject({ method: 'POST', path: '/api/v1/ddns/cloudflare/zones', body: { api_token: 'secret-token' } });
    expect(calls[1]).toMatchObject({ method: 'POST', path: '/api/v1/ddns/cloudflare/zones/zone%2F1/records', body: { config_id: 'cfg-1' } });
    expect(calls.every(c => !c.url.includes('secret-token'))).toBe(true);
  });
});

describe('service account keys', () => {
  const meta = { id: 'k1', name: 'claude mcp', scope: 'mcp', status: 'active', ttl_seconds: 31_536_000, created_at: '2026-11-14T00:00:00Z', expires_at: '2027-11-14T00:00:00Z' };

  it('lists keys without paging: the list is `{items}` and holds no secret', async () => {
    const { calls } = mockFetchAll(() => json({ items: [meta] }));
    expect(await listServiceAccountKeys()).toEqual([meta]);
    expect(calls).toHaveLength(1);
    expect(calls[0].query.has('cursor')).toBe(false);
  });

  it('creates with exactly the body it is given, and returns the one-time key', async () => {
    const { calls } = mockFetchAll(() => json({ ...meta, key: 'eyJ.secret.sig' }, { status: 201 }));
    const made = await createServiceAccountKey({ name: 'claude mcp', scope: 'mcp', ttl_seconds: 86_400 });
    expect(calls[0].method).toBe('POST');
    expect(calls[0].path).toBe('/api/v1/service-account-keys');
    expect(calls[0].body).toEqual({ name: 'claude mcp', scope: 'mcp', ttl_seconds: 86_400 });
    expect(made.key).toBe('eyJ.secret.sig');
  });

  it('revokes with DELETE and treats 204 as success', async () => {
    const { calls } = mockFetchAll(() => new Response(null, { status: 204 }));
    await expect(revokeServiceAccountKey('k1')).resolves.toBeUndefined();
    expect(calls[0].method).toBe('DELETE');
    expect(calls[0].path).toBe('/api/v1/service-account-keys/k1');
  });

  it('surfaces the Controller error envelope (e.g. 400 for a bad ttl, 404 for a foreign key)', async () => {
    mockFetchAll(() => apiError(400, 'bad_request', 'ttl_seconds must be between 60 and 630720000'));
    await expect(createServiceAccountKey({ name: 'x', scope: 'mcp', ttl_seconds: 5 })).rejects.toMatchObject({ status: 400, code: 'bad_request' });
    mockFetchAll(() => apiError(404, 'not_found', 'service account key not found'));
    await expect(revokeServiceAccountKey('nope')).rejects.toMatchObject({ status: 404 });
  });
});

describe('alert channels', () => {
  const ch = { id: 'a1', name: 'phone', type: 'telegram', enabled: true, events: ['ip_changed'], settings: { target_user_id: '42' }, has_secret: true, created_at: '2026-11-15T00:00:00Z', updated_at: '2026-11-15T00:00:00Z' };

  it('lists channels without paging and returns no secret', async () => {
    const { calls } = mockFetchAll(() => json({ items: [ch] }));
    expect(await listAlertChannels()).toEqual([ch]);
    expect(calls).toHaveLength(1);
    expect(calls[0].query.has('cursor')).toBe(false);
  });

  it('creates with exactly the body it is given', async () => {
    const { calls } = mockFetchAll(() => json(ch, { status: 201 }));
    const body = { name: 'phone', type: 'telegram' as const, settings: { target_user_id: '42' }, secret: '123456789:AAH-test_token_0123456789abcdefghij' };
    await createAlertChannel(body);
    expect(calls[0].method).toBe('POST');
    expect(calls[0].path).toBe('/api/v1/alerts/channels');
    expect(calls[0].body).toEqual(body);
  });

  it('updates with PATCH and tests with POST .../test', async () => {
    const { calls } = mockFetchAll(c => (c.path.endsWith('/test') ? json({ channel_id: 'a1', result: 'failed', sent_at: '2026-11-15T00:00:00Z', error: 'telegram: 403 Forbidden' }) : json(ch)));
    await updateAlertChannel('a1', { enabled: false });
    expect(calls[0].method).toBe('PATCH');
    expect(calls[0].body).toEqual({ enabled: false });
    const r = await testAlertChannel('a1');
    expect(calls[1].path).toBe('/api/v1/alerts/channels/a1/test');
    expect(r).toMatchObject({ result: 'failed', error: 'telegram: 403 Forbidden' });
  });

  it('deletes with DELETE and treats 204 as success; surfaces 409 for a taken name', async () => {
    const { calls } = mockFetchAll(() => new Response(null, { status: 204 }));
    await expect(deleteAlertChannel('a1')).resolves.toBeUndefined();
    expect(calls[0].method).toBe('DELETE');
    mockFetchAll(() => apiError(409, 'conflict', 'an alert channel with this name already exists'));
    await expect(createAlertChannel({ name: 'phone', type: 'telegram', settings: { target_user_id: '1' }, secret: 'x' })).rejects.toMatchObject({ status: 409 });
  });
});
