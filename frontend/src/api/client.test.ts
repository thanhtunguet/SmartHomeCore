import { beforeEach, describe, expect, it, vi } from 'vitest';
import { apiError, json, mockFetch, mockFetchAll, putSession, SESSION_KEY, stubLocalStorage, tokenPair } from '../test/helpers';

/** Fresh client + session modules per test: the client keeps module-level refresh state. */
async function load() {
  vi.resetModules();
  const client = await import('./client');
  const session = await import('./session');
  return { ...client, ...session };
}

/** The module is re-imported per test, so its ApiError class differs: type-only view of the error. */
type ApiErr = import('./client').ApiError;
/** Await a promise that must reject and return the error (fails the test if it resolves). */
async function failure(p: Promise<unknown>): Promise<ApiErr> {
  try {
    await p;
  } catch (e) {
    return e as ApiErr;
  }
  throw new Error('expected the request to reject');
}
const FAR = () => Date.now() + 10 * 60_000;
let store: Map<string, string>;

beforeEach(() => {
  store = stubLocalStorage();
});

describe('request: auth header', () => {
  it('sends the bearer token and JSON headers', async () => {
    const { request } = await load();
    putSession(store, { accessToken: 'tok-1', refreshToken: 'ref-1', expiresAt: FAR() });
    const { calls } = mockFetch(json({ id: 'u1', username: 'alice' }));

    await expect(request('/api/v1/auth/me')).resolves.toEqual({ id: 'u1', username: 'alice' });

    expect(calls[0].method).toBe('GET');
    expect(calls[0].headers.get('Authorization')).toBe('Bearer tok-1');
    expect(calls[0].headers.get('Accept')).toBe('application/json');
    expect(calls[0].headers.get('Content-Type')).toBeNull();
  });

  it('serialises a body as JSON and sets Content-Type', async () => {
    const { request } = await load();
    putSession(store, { accessToken: 'tok-1', refreshToken: 'ref-1', expiresAt: FAR() });
    const { calls } = mockFetch(json({ ok: true }));
    await request('/api/v1/devices', { method: 'POST', body: { name: 'NAS' } });
    expect(calls[0].headers.get('Content-Type')).toBe('application/json');
    expect(calls[0].body).toEqual({ name: 'NAS' });
  });

  it('sends no bearer on anonymous calls, even with a stored session', async () => {
    const { request } = await load();
    putSession(store, { accessToken: 'tok-1', refreshToken: 'ref-1', expiresAt: FAR() });
    const { calls } = mockFetch(json(tokenPair('x')));
    await request('/api/v1/auth/login', { method: 'POST', body: { username: 'a', password: 'b' }, anonymous: true });
    expect(calls[0].headers.get('Authorization')).toBeNull();
  });

  it('drops undefined and empty query values', async () => {
    const { request } = await load();
    const { calls } = mockFetch(json({ items: [] }));
    await request('/api/v1/commands', { anonymous: true, query: { limit: 200, cursor: undefined, device_id: '', status: 'failed' } });
    expect(calls[0].query.get('limit')).toBe('200');
    expect(calls[0].query.has('cursor')).toBe(false);
    expect(calls[0].query.has('device_id')).toBe(false);
    expect(calls[0].query.get('status')).toBe('failed');
  });

  it('returns undefined for 204', async () => {
    const { request } = await load();
    putSession(store, { accessToken: 'tok-1', refreshToken: 'ref-1', expiresAt: FAR() });
    mockFetch(new Response(null, { status: 204 }));
    await expect(request('/api/v1/devices/d1', { method: 'DELETE' })).resolves.toBeUndefined();
  });
});

describe('request: token refresh', () => {
  it('refreshes silently on 401 and retries the call once with the new token', async () => {
    const { request, readSession } = await load();
    putSession(store, { accessToken: 'old', refreshToken: 'ref-old', expiresAt: FAR() });
    const { calls } = mockFetch(
      apiError(401, 'unauthorized', 'token expired'),
      json(tokenPair('new')),
      json({ items: [] }),
    );

    await expect(request('/api/v1/devices')).resolves.toEqual({ items: [] });

    expect(calls.map(c => `${c.method} ${c.path}`)).toEqual(['GET /api/v1/devices', 'POST /api/v1/auth/refresh', 'GET /api/v1/devices']);
    expect(calls[0].headers.get('Authorization')).toBe('Bearer old');
    expect(calls[1].body).toEqual({ refresh_token: 'ref-old' });
    expect(calls[1].headers.get('Authorization')).toBeNull();
    expect(calls[2].headers.get('Authorization')).toBe('Bearer access-new');
    expect(readSession()).toMatchObject({ accessToken: 'access-new', refreshToken: 'refresh-new' });
  });

  it('retries only once: a second 401 clears the session and signals auth lost', async () => {
    const { request, readSession, setAuthLostHandler } = await load();
    const lost = vi.fn();
    setAuthLostHandler(lost);
    putSession(store, { accessToken: 'old', refreshToken: 'ref-old', expiresAt: FAR() });
    const { calls } = mockFetch(apiError(401, 'unauthorized', 'nope'), json(tokenPair('new')), apiError(401, 'unauthorized', 'still nope'));

    await expect(request('/api/v1/devices')).rejects.toMatchObject({ status: 401, code: 'unauthorized' });

    expect(calls).toHaveLength(3); // no third attempt
    expect(readSession()).toBeNull();
    expect(lost).toHaveBeenCalled();
  });

  it('refresh failure (401) clears the session and signals auth lost', async () => {
    const { request, readSession, setAuthLostHandler } = await load();
    const lost = vi.fn();
    setAuthLostHandler(lost);
    putSession(store, { accessToken: 'old', refreshToken: 'ref-old', expiresAt: FAR() });
    const { calls } = mockFetch(apiError(401, 'unauthorized', 'expired'), apiError(401, 'invalid_token', 'refresh token revoked'));

    await expect(request('/api/v1/devices')).rejects.toMatchObject({ status: 401, code: 'invalid_token', message: 'refresh token revoked' });

    expect(calls).toHaveLength(2);
    expect(store.has(SESSION_KEY)).toBe(false);
    expect(readSession()).toBeNull();
    expect(lost).toHaveBeenCalledTimes(1);
  });

  it('refreshes before the call when the access token is about to expire', async () => {
    const { request } = await load();
    putSession(store, { accessToken: 'old', refreshToken: 'ref-old', expiresAt: Date.now() + 2_000 });
    const { calls } = mockFetch(json(tokenPair('early')), json({ id: 'u1', username: 'alice' }));

    await request('/api/v1/auth/me');

    expect(calls.map(c => c.path)).toEqual(['/api/v1/auth/refresh', '/api/v1/auth/me']);
    expect(calls[1].headers.get('Authorization')).toBe('Bearer access-early');
  });

  it('concurrent 401s share one refresh (a rotated refresh token must not be replayed)', async () => {
    const { request } = await load();
    putSession(store, { accessToken: 'old', refreshToken: 'ref-old', expiresAt: FAR() });
    const { calls } = mockFetchAll(call => {
      if (call.path === '/api/v1/auth/refresh') return json(tokenPair('new'));
      return call.headers.get('Authorization') === 'Bearer old' ? apiError(401, 'unauthorized', 'expired') : json({ ok: true });
    });

    await Promise.all([request('/api/v1/devices'), request('/api/v1/commands')]);

    expect(calls.filter(c => c.path === '/api/v1/auth/refresh')).toHaveLength(1);
  });

  it('a 401 without a stored session is just an error (nothing to refresh)', async () => {
    const { request } = await load();
    const { calls } = mockFetch(apiError(401, 'unauthorized', 'missing credentials'));
    await expect(request('/api/v1/devices')).rejects.toMatchObject({ status: 401 });
    expect(calls).toHaveLength(1);
  });
});

describe('request: error envelope', () => {
  it('parses {"error":{"code","message"}}', async () => {
    const { request, ApiError } = await load();
    mockFetch(apiError(409, 'conflict', 'device has no MAC address'));
    const err = await failure(request('/api/v1/devices/d1/wake', { method: 'POST', anonymous: true }));
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 409, code: 'conflict', message: 'device has no MAC address' });
    expect(err.rateLimited).toBe(false);
  });

  it('copes with an empty-body 501 (no envelope)', async () => {
    const { request } = await load();
    mockFetch(new Response(null, { status: 501 }));
    const err = await failure(request('/api/v1/ddns/cloudflare/zones', { method: 'POST', body: { api_token: 't' }, anonymous: true }));
    expect(err).toMatchObject({ status: 501, code: 'http_501', message: 'HTTP 501' });
    expect(err.notImplemented).toBe(true);
  });

  it('reads the envelope of a 501 when the Controller sends one', async () => {
    const { request } = await load();
    mockFetch(apiError(501, 'not_implemented', 'cloudflare zones are not implemented yet'));
    const err = await failure(request('/x', { anonymous: true }));
    expect(err).toMatchObject({ status: 501, code: 'not_implemented', message: 'cloudflare zones are not implemented yet' });
    expect(err.notImplemented).toBe(true);
  });

  it('429 carries Retry-After and the rate_limited code', async () => {
    const { request } = await load();
    mockFetch(apiError(429, 'rate_limited', 'too many requests', { 'Retry-After': '1' }));
    const err = await failure(request('/api/v1/devices/d1/wake', { method: 'POST', anonymous: true }));
    expect(err).toMatchObject({ status: 429, code: 'rate_limited', retryAfterSeconds: 1 });
    expect(err.rateLimited).toBe(true);
    expect(err.message).toBe('too many requests (retry in 1s)');
  });

  it('429 without a usable Retry-After leaves retryAfterSeconds undefined', async () => {
    const { request } = await load();
    mockFetch(apiError(429, 'rate_limited', 'slow down'), apiError(429, 'rate_limited', 'slow down', { 'Retry-After': 'soon' }));
    const a = await failure(request('/x', { anonymous: true }));
    const b = await failure(request('/x', { anonymous: true }));
    expect(a.retryAfterSeconds).toBeUndefined();
    expect(b.retryAfterSeconds).toBeUndefined();
    expect(a.message).toBe('slow down');
  });

  it('keeps the message of a 502 from Cloudflare', async () => {
    const { request } = await load();
    mockFetch(apiError(502, 'cloudflare_error', 'Invalid API Token'));
    const err = await failure(request('/x', { anonymous: true }));
    expect(err).toMatchObject({ status: 502, code: 'cloudflare_error', message: 'Invalid API Token' });
  });

  it('falls back to the status for a non-JSON error body (proxy page)', async () => {
    const { request } = await load();
    mockFetch(new Response('<html>Bad gateway</html>', { status: 502, statusText: 'Bad Gateway' }));
    const err = await failure(request('/x', { anonymous: true }));
    expect(err).toMatchObject({ status: 502, code: 'http_502', message: 'Bad Gateway' });
  });

  it('403 is generic: no role semantics', async () => {
    const { request } = await load();
    mockFetch(apiError(403, 'forbidden', 'forbidden'));
    const err = await failure(request('/x', { anonymous: true }));
    expect(err.forbidden).toBe(true);
    expect(err.message).toBe('forbidden');
  });

  it('maps a failed fetch to a network ApiError', async () => {
    const { request } = await load();
    mockFetchAll(() => {
      throw new TypeError('Failed to fetch');
    });
    const err = await failure(request('/x', { anonymous: true }));
    expect(err).toMatchObject({ status: 0, code: 'network' });
    expect(err.network).toBe(true);
  });
});
