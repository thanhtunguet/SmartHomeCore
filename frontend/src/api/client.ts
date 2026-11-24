/**
 * Minimal fetch wrapper for the HomeCore API: bearer auth, one transparent token refresh on 401,
 * and the `{"error":{"code","message"}}` envelope surfaced as `ApiError`.
 */
import { clearSession, readSession, writeSession } from './session';
import type { ApiErrorBody, TokenPair } from './schema';

/** Empty = same origin (embedded UI, or the Vite dev proxy). Set VITE_API_BASE to target another host. */
const BASE = (import.meta.env.VITE_API_BASE as string | undefined)?.replace(/\/$/, '') ?? '';

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    /** `Retry-After` of a 429, in seconds. */
    public readonly retryAfterSeconds?: number,
  ) {
    super(message);
    this.name = 'ApiError';
  }

  /** Operation exists in the spec but the Controller has not implemented it yet. */
  get notImplemented() {
    return this.status === 501;
  }
  get unauthorized() {
    return this.status === 401;
  }
  get forbidden() {
    return this.status === 403;
  }
  /** `429 rate_limited`: not a failure of the operation, retry after `retryAfterSeconds`. */
  get rateLimited() {
    return this.status === 429;
  }
  get network() {
    return this.status === 0;
  }
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  query?: Record<string, string | number | undefined>;
  /** Public endpoint: no bearer, no refresh-on-401. */
  anonymous?: boolean;
  signal?: AbortSignal;
}

let onAuthLost: (() => void) | null = null;
/** The auth layer registers this to learn that the session can no longer be refreshed. */
export function setAuthLostHandler(fn: (() => void) | null) {
  onAuthLost = fn;
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
  const url = BASE + path;
  if (!query) return url;
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== '') qs.set(k, String(v));
  const s = qs.toString();
  return s ? `${url}?${s}` : url;
}

async function parseError(res: Response): Promise<ApiError> {
  let code = `http_${res.status}`;
  let message = res.statusText || `HTTP ${res.status}`;
  try {
    const body = (await res.json()) as Partial<ApiErrorBody>;
    if (body.error) {
      code = body.error.code || code;
      message = body.error.message || message;
    }
  } catch {
    /* non-JSON body (e.g. proxy error page) */
  }
  let retryAfter: number | undefined;
  if (res.status === 429) {
    const retry = res.headers.get('Retry-After');
    const n = retry === null ? NaN : Number(retry);
    if (Number.isFinite(n) && n >= 0) retryAfter = n;
    message = `${message}${retry ? ` (retry in ${retry}s)` : ''}`;
  }
  return new ApiError(res.status, code, message, retryAfter);
}

async function send(path: string, opts: RequestOptions, token: string | null): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers.Authorization = `Bearer ${token}`;
  try {
    return await fetch(buildUrl(path, opts.query), {
      method: opts.method ?? 'GET',
      headers,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      signal: opts.signal,
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new ApiError(0, 'network', 'Cannot reach the HomeCore Controller');
  }
}

/**
 * Exchange the refresh token for a new pair. Serialised across tabs with the Web Locks API; a tab
 * that loses the race adopts the session the winner stored instead of replaying a rotated token.
 */
let refreshing: Promise<string> | null = null;

export function refreshAccessToken(staleToken: string | null): Promise<string> {
  if (refreshing) return refreshing;
  const run = async (): Promise<string> => {
    const current = readSession();
    if (!current) throw new ApiError(401, 'no_session', 'Not signed in');
    // Another tab already rotated the tokens.
    if (current.accessToken !== staleToken && current.expiresAt > Date.now() + 5_000) {
      return current.accessToken;
    }
    const res = await send('/api/v1/auth/refresh', { method: 'POST', body: { refresh_token: current.refreshToken } }, null);
    if (!res.ok) {
      const err = await parseError(res);
      if (err.status === 400 || err.status === 401) clearSession();
      throw err;
    }
    return writeSession((await res.json()) as TokenPair).accessToken;
  };
  const locked: Promise<string> =
    typeof navigator !== 'undefined' && navigator.locks
      ? navigator.locks.request('homecore-token-refresh', run)
      : run();
  refreshing = locked.finally(() => {
    refreshing = null;
  });
  return refreshing;
}

export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  let session = opts.anonymous ? null : readSession();
  // Refresh a little early to avoid a guaranteed 401 round trip.
  if (session && session.expiresAt - Date.now() < 10_000) {
    try {
      await refreshAccessToken(session.accessToken);
      session = readSession();
    } catch (e) {
      if (e instanceof ApiError && e.unauthorized) {
        onAuthLost?.();
      }
      throw e;
    }
  }
  let res = await send(path, opts, session?.accessToken ?? null);

  if (res.status === 401 && !opts.anonymous && session) {
    try {
      const token = await refreshAccessToken(session.accessToken);
      res = await send(path, opts, token);
    } catch (e) {
      onAuthLost?.();
      throw e instanceof ApiError ? e : new ApiError(401, 'unauthorized', 'Session expired');
    }
    if (res.status === 401) {
      clearSession();
      onAuthLost?.();
    }
  }

  if (!res.ok) throw await parseError(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}
