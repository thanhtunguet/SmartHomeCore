import { describe, expect, it } from 'vitest';
import {
  MAX_TTL_SECONDS,
  canRevoke,
  keyState,
  relativeTime,
  scopeLabel,
  ttlSecondsFromDays,
  usageExamples,
  validateKeyName,
} from './serviceKeys';

const NOW = Date.parse('2026-11-14T12:00:00Z');
const at = (ms: number) => new Date(NOW + ms).toISOString();
const DAY = 86_400_000;

describe('ttlSecondsFromDays', () => {
  it('converts whole days and defaults match the spec (1 year = 31 536 000 s)', () => {
    expect(ttlSecondsFromDays(365)).toEqual({ seconds: 31_536_000 });
    expect(ttlSecondsFromDays(1)).toEqual({ seconds: 86_400 });
  });

  it('accepts the 20 year maximum and refuses anything above', () => {
    expect(ttlSecondsFromDays(MAX_TTL_SECONDS / 86_400)).toEqual({ seconds: MAX_TTL_SECONDS });
    expect(ttlSecondsFromDays(MAX_TTL_SECONDS / 86_400 + 1)).toHaveProperty('error');
  });

  it.each([0, -3, 1.5, NaN, Infinity])('refuses %s', d => {
    expect(ttlSecondsFromDays(d)).toHaveProperty('error');
  });
});

describe('validateKeyName', () => {
  it('trims, and counts characters not UTF-16 units (the Controller counts characters)', () => {
    expect(validateKeyName('  claude mcp ')).toEqual({ name: 'claude mcp' });
    expect(validateKeyName('😀'.repeat(64))).toHaveProperty('name');
    expect(validateKeyName('😀'.repeat(65))).toHaveProperty('error');
  });

  it.each(['', '   ', 'a\nb', 'x'.repeat(65)])('refuses %j', n => {
    expect(validateKeyName(n)).toHaveProperty('error');
  });
});

describe('keyState and canRevoke', () => {
  it('maps the status of the list', () => {
    expect(keyState({ status: 'active', expires_at: at(DAY) }, NOW)).toEqual({ label: 'Active', tone: 'ok' });
    expect(keyState({ status: 'rotated', expires_at: at(DAY) }, NOW)).toEqual({ label: 'Rotated', tone: 'warn' });
    expect(keyState({ status: 'revoked', expires_at: at(DAY) }, NOW)).toEqual({ label: 'Revoked', tone: 'dead' });
    expect(keyState({ status: 'expired', expires_at: at(-DAY) }, NOW)).toEqual({ label: 'Expired', tone: 'dead' });
  });

  it('shows a key that expired while the page was open as expired', () => {
    expect(keyState({ status: 'active', expires_at: at(-1) }, NOW).label).toBe('Expired');
  });

  it('revoked beats expired when both are true', () => {
    expect(keyState({ status: 'revoked', expires_at: at(-DAY) }, NOW).label).toBe('Revoked');
  });

  it('only keys that can still authenticate can be revoked', () => {
    expect(canRevoke({ status: 'active', expires_at: at(DAY) }, NOW)).toBe(true);
    expect(canRevoke({ status: 'rotated', expires_at: at(DAY) }, NOW)).toBe(true);
    expect(canRevoke({ status: 'revoked', expires_at: at(DAY) }, NOW)).toBe(false);
    expect(canRevoke({ status: 'active', expires_at: at(-1) }, NOW)).toBe(false);
  });
});

describe('relativeTime', () => {
  it('reads both directions', () => {
    expect(relativeTime(at(2 * DAY + 5), NOW)).toBe('in 2 days');
    expect(relativeTime(at(-1 * DAY), NOW)).toBe('1 day ago');
    expect(relativeTime(at(400 * DAY), NOW)).toBe('in 1 year');
    expect(relativeTime(at(65 * DAY), NOW)).toBe('in 2 months');
    expect(relativeTime(at(3 * 3_600_000), NOW)).toBe('in 3 hours');
    expect(relativeTime(at(30_000), NOW)).toBe('in under a minute');
    expect(relativeTime(at(-30_000), NOW)).toBe('just now');
  });
});

describe('usageExamples', () => {
  it('uses the scope: an mcp key lists devices, a legacy key calls a legacy endpoint', () => {
    expect(usageExamples('https://home.example', 'mcp')[0].command).toContain('https://home.example/api/v1/devices');
    expect(usageExamples('https://home.example', 'legacy')[0].command).toContain('/api/is-online?device=');
  });

  it('shows the rotate call and never embeds a key', () => {
    const [, rotate] = usageExamples('http://x', 'mcp');
    expect(rotate.command).toContain('POST');
    expect(rotate.command).toContain('/api/v1/auth/service-key/rotate');
    for (const e of usageExamples('http://x', 'mcp')) expect(e.command).toContain('$HOMECORE_KEY');
  });
});

describe('scopeLabel', () => {
  it('names both scopes', () => {
    expect(scopeLabel('mcp')).toContain('mcp');
    expect(scopeLabel('legacy')).toContain('legacy');
  });
});
