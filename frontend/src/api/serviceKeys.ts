/**
 * Helpers for the service account keys screen (ADR-018): the lifetime presets and their bounds,
 * the scope descriptions, and the wording of a key's state. Pure, so it is unit tested.
 */
import type { ServiceKey, ServiceKeyScope } from './schema';

const DAY = 86_400;

/** Bounds of `ttl_seconds` in the spec (also enforced by the Controller): 60 s to 20 years. */
export const MIN_TTL_SECONDS = 60;
export const MAX_TTL_SECONDS = 630_720_000;
export const DEFAULT_TTL_DAYS = 365;
export const MAX_NAME_LENGTH = 64;

export const TTL_PRESETS: { label: string; days: number }[] = [
  { label: '30 days', days: 30 },
  { label: '90 days', days: 90 },
  { label: '1 year', days: 365 },
  { label: '5 years', days: 5 * 365 },
];

export const SCOPES: { value: ServiceKeyScope; label: string; description: string }[] = [
  {
    value: 'mcp',
    label: 'AI agent (mcp)',
    description: 'Control and monitor devices: list, wake, shutdown, probe, commands, activity. Cannot change configuration, DDNS or keys.',
  },
  {
    value: 'legacy',
    label: 'Legacy app (legacy)',
    description: 'Only /api/turn-on, /api/turn-off and /api/is-online, for apps that predate multi-device support.',
  },
];

export const scopeLabel = (scope: ServiceKeyScope): string => SCOPES.find(s => s.value === scope)?.label ?? scope;

/** Days (a whole number the user typed) to `ttl_seconds`, or an error message. */
export function ttlSecondsFromDays(days: number): { seconds: number } | { error: string } {
  if (!Number.isInteger(days) || days < 1) return { error: 'Lifetime must be a whole number of days, at least 1.' };
  const seconds = days * DAY;
  if (seconds > MAX_TTL_SECONDS) return { error: `Lifetime must be at most ${MAX_TTL_SECONDS / DAY} days (20 years).` };
  return { seconds };
}

/** The name as the Controller will store it (trimmed), or an error message. */
export function validateKeyName(name: string): { name: string } | { error: string } {
  const n = name.trim();
  if (n === '') return { error: 'Give the key a name, for example the agent that will use it.' };
  if ([...n].length > MAX_NAME_LENGTH) return { error: `The name can have at most ${MAX_NAME_LENGTH} characters.` };
  if (/[\u0000-\u001f\u007f]/.test(n)) return { error: 'The name cannot contain control characters.' };
  return { name: n };
}

export type KeyTone = 'ok' | 'warn' | 'dead';

/**
 * What to show for a key. The list reports `status` as of the request; a key that expired while the
 * page was open is shown as expired too (`now` is injectable for tests).
 */
export function keyState(k: Pick<ServiceKey, 'status' | 'expires_at'>, now = Date.now()): { label: string; tone: KeyTone } {
  if (k.status === 'revoked') return { label: 'Revoked', tone: 'dead' };
  if (k.status === 'expired' || Date.parse(k.expires_at) <= now) return { label: 'Expired', tone: 'dead' };
  if (k.status === 'rotated') return { label: 'Rotated', tone: 'warn' };
  return { label: 'Active', tone: 'ok' };
}

/** A key can be revoked while it still authenticates or may still rotate; the rest is history. */
export const canRevoke = (k: Pick<ServiceKey, 'status' | 'expires_at'>, now = Date.now()): boolean =>
  keyState(k, now).tone !== 'dead';

/** "in 11 months", "in 3 days", "2 days ago" for the expiry column. */
export function relativeTime(iso: string, now = Date.now()): string {
  const diff = Date.parse(iso) - now;
  const abs = Math.abs(diff);
  const units: [number, string][] = [
    [365 * DAY * 1000, 'year'],
    [30 * DAY * 1000, 'month'],
    [DAY * 1000, 'day'],
    [3_600_000, 'hour'],
    [60_000, 'minute'],
  ];
  for (const [ms, name] of units) {
    if (abs >= ms) {
      const n = Math.floor(abs / ms);
      const text = `${n} ${name}${n === 1 ? '' : 's'}`;
      return diff >= 0 ? `in ${text}` : `${text} ago`;
    }
  }
  return diff >= 0 ? 'in under a minute' : 'just now';
}

/**
 * Example calls shown next to a new key. `origin` is where the browser reached the Controller, the
 * same place an agent will call. The key is a placeholder here: it is only ever shown by the
 * component that received it, never put into a string that is logged or stored.
 */
export function usageExamples(origin: string, scope: ServiceKeyScope): { label: string; command: string }[] {
  const out = [{ label: 'Use the key', command: scope === 'legacy'
    ? `curl -H "Authorization: Bearer $HOMECORE_KEY" "${origin}/api/is-online?device=NAME"`
    : `curl -H "Authorization: Bearer $HOMECORE_KEY" ${origin}/api/v1/devices` }];
  out.push({ label: 'Rotate the key (the agent does this itself)', command: `curl -X POST -H "Authorization: Bearer $HOMECORE_KEY" ${origin}/api/v1/auth/service-key/rotate` });
  return out;
}
