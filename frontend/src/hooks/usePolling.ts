import { useEffect, useRef } from 'react';

/**
 * Run `fn` immediately and then every `intervalMs` while `enabled` and the tab is visible
 * (the API has no push channel by design: no WebSocket/SSE, see ADR scope). Overlapping runs are
 * skipped. Returns nothing; call the underlying refresh function directly for an on-demand fetch.
 */
export function usePolling(fn: () => Promise<unknown>, intervalMs: number, enabled = true): void {
  const fnRef = useRef(fn);
  fnRef.current = fn;

  useEffect(() => {
    if (!enabled) return;
    let running = false;
    let stopped = false;
    const tick = async () => {
      if (running || stopped || document.hidden) return;
      running = true;
      try {
        await fnRef.current();
      } catch {
        /* the caller records errors in its own state */
      } finally {
        running = false;
      }
    };
    void tick();
    const timer = setInterval(tick, intervalMs);
    const onVisible = () => {
      if (!document.hidden) void tick();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      stopped = true;
      clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [intervalMs, enabled]);
}
