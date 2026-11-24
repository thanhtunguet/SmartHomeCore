/**
 * Per-key cooldown: the client side of the API's "at most one wake/shutdown request per device
 * per second" rule (wakeDevice / shutdownDevice in api/openapi.yaml). The Controller enforces it
 * (`429 rate_limited`); dropping excess calls here saves the round trip and keeps buttons honest.
 *
 * Pure and framework free: time comes from `now` (default `Date.now`, so fake timers drive it).
 * One guard instance is shared by wake and shutdown, because the server counts both together.
 */
export const POWER_COOLDOWN_MS = 1000;

export interface CooldownGuard {
  /** Allow the call and start the cooldown, or return false (nothing recorded) while one runs. */
  tryAcquire(key: string): boolean;
  /** Start (or extend) a cooldown without a check, e.g. after the server answered 429. */
  hold(key: string, ms?: number): void;
  isLocked(key: string): boolean;
  /** Milliseconds until `key` may be used again (0 when free). */
  remainingMs(key: string): number;
}

export function createCooldownGuard(windowMs = POWER_COOLDOWN_MS, now: () => number = () => Date.now()): CooldownGuard {
  const until = new Map<string, number>();

  const remainingMs = (key: string) => Math.max(0, (until.get(key) ?? 0) - now());

  return {
    tryAcquire(key) {
      const t = now();
      if (t < (until.get(key) ?? 0)) return false;
      // Forget expired keys so the map cannot grow without bound.
      for (const [k, u] of until) if (u <= t) until.delete(k);
      until.set(key, t + windowMs);
      return true;
    },
    hold(key, ms = windowMs) {
      until.set(key, Math.max(until.get(key) ?? 0, now() + ms));
    },
    isLocked: key => remainingMs(key) > 0,
    remainingMs,
  };
}
