/** Shared test helpers: browser globals the API layer touches, and canned `fetch` responses. */
import { vi } from 'vitest';

/** Map-backed `localStorage` so the session code runs in Node. Returns the backing map. */
export function stubLocalStorage(): Map<string, string> {
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, String(v)),
    removeItem: (k: string) => void store.delete(k),
    clear: () => store.clear(),
    key: (i: number) => [...store.keys()][i] ?? null,
    get length() {
      return store.size;
    },
  });
  // Node >= 21 has a global `navigator` without Web Locks: make that explicit.
  vi.stubGlobal('navigator', {});
  return store;
}

export const SESSION_KEY = 'homecore_session_v1';

export function putSession(store: Map<string, string>, s: { accessToken: string; refreshToken: string; expiresAt: number }) {
  store.set(SESSION_KEY, JSON.stringify(s));
}

export function json(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' }, ...init });
}

export function apiError(status: number, code: string, message: string, headers: Record<string, string> = {}): Response {
  return json({ error: { code, message } }, { status, headers });
}

export const tokenPair = (suffix: string, expiresIn = 900) => ({
  access_token: `access-${suffix}`,
  refresh_token: `refresh-${suffix}`,
  token_type: 'Bearer',
  expires_in: expiresIn,
});

export interface RecordedCall {
  method: string;
  url: string;
  path: string;
  query: URLSearchParams;
  headers: Headers;
  body: unknown;
}

/**
 * Replace global `fetch` with a queue of handlers (a Response, or a function returning one).
 * Every call is recorded; an unexpected call fails the test.
 */
export function mockFetch(...handlers: Array<Response | ((call: RecordedCall) => Response | Promise<Response>)>) {
  const calls: RecordedCall[] = [];
  const queue = [...handlers];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const u = new URL(url, 'http://controller.test');
    const call: RecordedCall = {
      method: init?.method ?? 'GET',
      url,
      path: u.pathname,
      query: u.searchParams,
      headers: new Headers(init?.headers),
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
    };
    calls.push(call);
    const h = queue.shift();
    if (!h) throw new Error(`unexpected fetch ${call.method} ${url}`);
    return typeof h === 'function' ? h(call) : h;
  });
  vi.stubGlobal('fetch', fn);
  return { calls, fn };
}

/** Like `mockFetch`, but one handler answers every call. */
export function mockFetchAll(handler: (call: RecordedCall) => Response | Promise<Response>) {
  const calls: RecordedCall[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const u = new URL(url, 'http://controller.test');
    const call: RecordedCall = {
      method: init?.method ?? 'GET',
      url,
      path: u.pathname,
      query: u.searchParams,
      headers: new Headers(init?.headers),
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
    };
    calls.push(call);
    return handler(call);
  });
  vi.stubGlobal('fetch', fn);
  return { calls, fn };
}
