import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createCooldownGuard, POWER_COOLDOWN_MS } from './cooldown';

describe('createCooldownGuard (1 wake/shutdown request per device per second)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-14T10:00:00Z'));
  });
  afterEach(() => vi.useRealTimers());

  it('the window is one second', () => {
    expect(POWER_COOLDOWN_MS).toBe(1000);
  });

  it('lets the first call through and drops calls inside the window', () => {
    const g = createCooldownGuard();
    expect(g.tryAcquire('dev-a')).toBe(true);
    expect(g.tryAcquire('dev-a')).toBe(false);
    vi.advanceTimersByTime(999);
    expect(g.tryAcquire('dev-a')).toBe(false);
    expect(g.isLocked('dev-a')).toBe(true);
  });

  it('frees the device exactly after 1000 ms', () => {
    const g = createCooldownGuard();
    g.tryAcquire('dev-a');
    vi.advanceTimersByTime(1000);
    expect(g.isLocked('dev-a')).toBe(false);
    expect(g.tryAcquire('dev-a')).toBe(true);
  });

  it('does not extend the window when a call is dropped', () => {
    const g = createCooldownGuard();
    g.tryAcquire('dev-a');
    vi.advanceTimersByTime(600);
    expect(g.tryAcquire('dev-a')).toBe(false); // dropped: must not restart the second
    vi.advanceTimersByTime(400);
    expect(g.tryAcquire('dev-a')).toBe(true);
  });

  it('does not freeze other devices', () => {
    const g = createCooldownGuard();
    expect(g.tryAcquire('dev-a')).toBe(true);
    expect(g.isLocked('dev-b')).toBe(false);
    expect(g.tryAcquire('dev-b')).toBe(true);
    expect(g.tryAcquire('dev-a')).toBe(false);
  });

  it('reports the time left', () => {
    const g = createCooldownGuard();
    expect(g.remainingMs('dev-a')).toBe(0);
    g.tryAcquire('dev-a');
    vi.advanceTimersByTime(250);
    expect(g.remainingMs('dev-a')).toBe(750);
  });

  it('hold() locks without a check and never shortens an existing lock (429 with Retry-After)', () => {
    const g = createCooldownGuard();
    g.hold('dev-a', 3000);
    expect(g.isLocked('dev-a')).toBe(true);
    expect(g.tryAcquire('dev-a')).toBe(false);
    g.hold('dev-a', 500);
    expect(g.remainingMs('dev-a')).toBe(3000);
    vi.advanceTimersByTime(3000);
    expect(g.tryAcquire('dev-a')).toBe(true);
  });

  it('takes the clock from an injected now()', () => {
    let t = 5_000;
    const g = createCooldownGuard(1000, () => t);
    expect(g.tryAcquire('x')).toBe(true);
    t += 999;
    expect(g.tryAcquire('x')).toBe(false);
    t += 1;
    expect(g.tryAcquire('x')).toBe(true);
  });
});
