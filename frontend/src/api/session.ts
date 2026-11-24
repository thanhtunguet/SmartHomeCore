/**
 * Token session storage.
 *
 * The API issues a JWT access token (short lived) plus a rotating refresh token in the JSON body;
 * there is no cookie flow. Both are kept in localStorage so that several tabs share one session
 * (refresh-token rotation with reuse detection would otherwise revoke the whole family when two
 * tabs refresh independently). The CSP served by the Controller (`default-src 'self'`, no inline
 * script) is the main mitigation against token theft via XSS.
 */
import type { TokenPair } from './schema';

const KEY = 'homecore_session_v1';

export interface Session {
  accessToken: string;
  refreshToken: string;
  /** Epoch ms at which the access token expires. */
  expiresAt: number;
}

type Listener = (s: Session | null) => void;
const listeners = new Set<Listener>();

export function readSession(): Session | null {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return null;
    const s = JSON.parse(raw) as Session;
    if (typeof s.accessToken === 'string' && typeof s.refreshToken === 'string') return s;
  } catch {
    /* corrupted or storage unavailable */
  }
  return null;
}

export function writeSession(pair: TokenPair): Session {
  const s: Session = {
    accessToken: pair.access_token,
    refreshToken: pair.refresh_token,
    expiresAt: Date.now() + pair.expires_in * 1000,
  };
  try {
    localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    /* storage unavailable: session lives only until reload */
  }
  listeners.forEach(l => l(s));
  return s;
}

export function clearSession(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
  listeners.forEach(l => l(null));
}

/** Subscribe to session changes in this tab and (via the storage event) in other tabs. */
export function onSessionChange(l: Listener): () => void {
  listeners.add(l);
  const storage = (e: StorageEvent) => {
    if (e.key === KEY || e.key === null) l(readSession());
  };
  window.addEventListener('storage', storage);
  return () => {
    listeners.delete(l);
    window.removeEventListener('storage', storage);
  };
}
