import { useCallback, useEffect, useRef, useState } from 'react';
import { createCooldownGuard, POWER_COOLDOWN_MS } from '../api/cooldown';

/**
 * React face of `createCooldownGuard`: `tryAcquire(deviceId)` for the request itself and
 * `isLocked(deviceId)` for disabling that device's wake/shutdown buttons while its second runs.
 * Locking one device re-renders subscribers once when it starts and once when it ends; other
 * devices are unaffected.
 */
export function usePowerCooldown() {
  const guard = useRef(createCooldownGuard()).current;
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>());
  const [locked, setLocked] = useState<ReadonlySet<string>>(() => new Set());

  const show = useCallback(
    (key: string) => {
      setLocked(prev => (prev.has(key) ? prev : new Set(prev).add(key)));
      clearTimeout(timers.current.get(key));
      timers.current.set(
        key,
        setTimeout(() => {
          timers.current.delete(key);
          setLocked(prev => {
            if (!prev.has(key)) return prev;
            const next = new Set(prev);
            next.delete(key);
            return next;
          });
        }, Math.max(1, guard.remainingMs(key))),
      );
    },
    [guard],
  );

  useEffect(() => {
    const t = timers.current;
    return () => t.forEach(clearTimeout);
  }, []);

  const tryAcquire = useCallback(
    (key: string) => {
      if (!guard.tryAcquire(key)) return false;
      show(key);
      return true;
    },
    [guard, show],
  );

  const hold = useCallback(
    (key: string, ms = POWER_COOLDOWN_MS) => {
      guard.hold(key, ms);
      show(key);
    },
    [guard, show],
  );

  const isLocked = useCallback((key: string) => locked.has(key), [locked]);

  return { tryAcquire, hold, isLocked };
}
