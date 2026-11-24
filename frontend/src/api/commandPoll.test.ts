import { describe, expect, it, vi } from 'vitest';
import { isTerminal, pollCommand, TERMINAL } from './commandPoll';
import type { ApiCommand } from './schema';

const cmd = (status: ApiCommand['status'], extra: Partial<ApiCommand> = {}): ApiCommand => ({
  id: 'c1',
  device_id: 'd1',
  type: 'wake',
  status,
  created_at: '2026-11-14T10:00:00Z',
  updated_at: '2026-11-14T10:00:00Z',
  ...extra,
});

const noSleep = () => Promise.resolve();

describe('terminal statuses', () => {
  it('are completed, failed and timed_out only', () => {
    expect([...TERMINAL].sort()).toEqual(['completed', 'failed', 'timed_out']);
    expect(isTerminal(cmd('acknowledged'))).toBe(false);
  });
});

describe('pollCommand with an idempotent API', () => {
  it('a command that is already completed on the first read costs no further request', async () => {
    // waking an online device: 202 with a command that is `completed` immediately
    const fetchCommand = vi.fn();
    const seen: string[] = [];
    const final = await pollCommand(cmd('completed'), () => true, c => seen.push(c.status), { fetchCommand, sleep: noSleep });
    expect(final?.status).toBe('completed');
    expect(seen).toEqual(['completed']);
    expect(fetchCommand).not.toHaveBeenCalled();
  });

  it('first-read failed and timed_out are returned as is (a real outcome, not a client error)', async () => {
    const fetchCommand = vi.fn();
    for (const status of ['failed', 'timed_out'] as const) {
      const final = await pollCommand(cmd(status, { error: 'x' }), () => true, () => undefined, { fetchCommand, sleep: noSleep });
      expect(final?.status).toBe(status);
    }
    expect(fetchCommand).not.toHaveBeenCalled();
  });

  it('follows an already-active command (returned by a repeated request) until it is terminal', async () => {
    const reads = [cmd('sent'), cmd('acknowledged'), cmd('completed')];
    const fetchCommand = vi.fn(async () => reads.shift()!);
    const seen: string[] = [];
    const final = await pollCommand(cmd('pending'), () => true, c => seen.push(c.status), { fetchCommand, sleep: noSleep });
    expect(seen).toEqual(['pending', 'sent', 'acknowledged', 'completed']);
    expect(final?.status).toBe('completed');
    expect(fetchCommand).toHaveBeenCalledTimes(3);
    expect(fetchCommand).toHaveBeenCalledWith('c1');
  });

  it('returns null when superseded', async () => {
    let current = true;
    const fetchCommand = vi.fn(async () => cmd('sent'));
    const final = await pollCommand(cmd('pending'), () => current, () => (current = false), { fetchCommand, sleep: noSleep });
    expect(final).toBeNull();
  });

  it('tolerates isolated read failures but throws after maxFailures in a row', async () => {
    const ok = vi.fn()
      .mockRejectedValueOnce(new Error('blip'))
      .mockResolvedValueOnce(cmd('completed'));
    expect((await pollCommand(cmd('sent'), () => true, () => undefined, { fetchCommand: ok, sleep: noSleep }))?.status).toBe('completed');

    const bad = vi.fn().mockRejectedValue(new Error('down'));
    await expect(pollCommand(cmd('sent'), () => true, () => undefined, { fetchCommand: bad, sleep: noSleep, maxFailures: 3 })).rejects.toThrow('down');
    expect(bad).toHaveBeenCalledTimes(3);
  });

  it('gives up after deadline_at plus the grace period and returns the last command', async () => {
    let t = Date.parse('2026-11-14T10:00:00Z');
    const fetchCommand = vi.fn(async () => cmd('sent'));
    const sleep = async (ms: number) => void (t += ms);
    const first = cmd('sent', { deadline_at: '2026-11-14T10:01:00Z' });
    const final = await pollCommand(first, () => true, () => undefined, { fetchCommand, sleep, now: () => t, intervalMs: 10_000, graceMs: 20_000 });
    expect(final?.status).toBe('sent');
    expect(t).toBeLessThanOrEqual(Date.parse('2026-11-14T10:01:30Z'));
    expect(fetchCommand.mock.calls.length).toBeGreaterThan(3);
  });
});
