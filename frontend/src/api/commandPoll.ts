/**
 * Following a wake/shutdown command to its end.
 *
 * Both operations are idempotent and answer `202` with a command that may already be terminal on
 * the first read (waking an online device, shutting down an offline one) or that is an earlier,
 * still active command; neither is an error, so the first read is handled like any other.
 */
import type { ApiCommand } from './schema';

export const TERMINAL: ReadonlySet<ApiCommand['status']> = new Set(['completed', 'failed', 'timed_out']);

export const isTerminal = (c: Pick<ApiCommand, 'status'>) => TERMINAL.has(c.status);

export interface PollOptions {
  /** Fetch the command again (`getCommand` of the API). */
  fetchCommand: (id: string) => Promise<ApiCommand>;
  /** Cadence between reads. */
  intervalMs?: number;
  /** Grace beyond the command's `deadline_at` before the client stops waiting. */
  graceMs?: number;
  /** Used when the command carries no `deadline_at`. */
  defaultWaitMs?: number;
  /** Consecutive failed reads tolerated before the error is thrown. */
  maxFailures?: number;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
}

const defaultSleep = (ms: number) => new Promise<void>(r => setTimeout(r, ms));

/**
 * Poll until `first` (or a later read) is terminal. `onUpdate` sees every read, including `first`.
 * Returns null when superseded (`isCurrent()` false) and the last known command if the client
 * gives up waiting. A command that is terminal on the first read costs no further request.
 */
export async function pollCommand(
  first: ApiCommand,
  isCurrent: () => boolean,
  onUpdate: (c: ApiCommand) => void,
  opts: PollOptions,
): Promise<ApiCommand | null> {
  const { fetchCommand, intervalMs = 1500, graceMs = 30_000, defaultWaitMs = 180_000, maxFailures = 5 } = opts;
  const now = opts.now ?? Date.now;
  const sleep = opts.sleep ?? defaultSleep;

  let cmd = first;
  const waitMs = cmd.deadline_at ? Math.max(0, Date.parse(cmd.deadline_at) - now()) : defaultWaitMs;
  const giveUpAt = now() + waitMs + graceMs;
  let failures = 0;
  onUpdate(cmd);
  while (isCurrent() && !isTerminal(cmd)) {
    if (now() > giveUpAt) return cmd;
    await sleep(intervalMs);
    if (!isCurrent()) return null;
    try {
      cmd = await fetchCommand(cmd.id);
      failures = 0;
      onUpdate(cmd);
    } catch (e) {
      if (++failures >= maxFailures) throw e;
    }
  }
  return isCurrent() ? cmd : null;
}
