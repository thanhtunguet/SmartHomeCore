import { describe, expect, it } from 'vitest';
import { activityToEvent, categoryOf, filterActivity, humanizeAction, mergeActivity, outcomeOf } from './activity';
import type { ActivityEvent } from '../types';
import type { ActivityItem, ApiCommand } from './schema';

const item = (id: number, over: Partial<ActivityItem> = {}): ActivityItem => ({
  id,
  at: '2026-11-14T10:00:00Z',
  category: 'device',
  action: 'device.update',
  result: 'ok',
  message: `message ${id}`,
  ...over,
});

const command = (id: string, over: Partial<ApiCommand> = {}): ApiCommand => ({
  id,
  device_id: 'dev-1',
  type: 'shutdown',
  status: 'pending',
  created_at: '2026-11-14T10:00:00Z',
  updated_at: '2026-11-14T10:00:00Z',
  ...over,
});

const local = (id: string, timestamp: string): ActivityEvent => ({
  id,
  source: 'local',
  timestamp,
  category: 'device',
  title: 'TCP Health Probe Passed',
  description: 'probe',
  outcome: 'success',
});

describe('categoryOf', () => {
  it.each([
    ['auth', 'auth.login', 'auth'],
    ['device', 'device.update', 'device'],
    ['ddns', 'ddns.sync', 'ddns'],
    ['user', 'user.create', 'user'],
    ['system', 'controller_key.create', 'system'],
    ['command', 'device.wake', 'wol'],
    ['command', 'device.shutdown', 'shutdown'],
    ['command', 'device.reboot', 'system'], // a future command action
    ['brand_new', 'x.y', 'system'], // unknown category tolerated
    [undefined, undefined, 'system'],
  ])('%s / %s -> %s', (cat, action, expected) => {
    expect(categoryOf(cat, action)).toBe(expected);
  });
});

describe('outcomeOf', () => {
  it.each([
    ['ok', 'success'],
    ['failed', 'error'],
    ['clock_skew', 'error'], // an error code
    ['something_new', 'error'],
    ['', 'info'],
    [undefined, 'info'],
  ])('%s -> %s', (result, expected) => {
    expect(outcomeOf(result)).toBe(expected);
  });
});

describe('humanizeAction', () => {
  it('knows the Controller actions and tolerates new ones', () => {
    expect(humanizeAction('auth.login')).toBe('Sign-in');
    expect(humanizeAction('device.shutdown')).toBe('Shutdown Requested');
    expect(humanizeAction('backup.run_now')).toBe('Backup run now');
    expect(humanizeAction('')).toBe('Event');
    expect(humanizeAction(undefined)).toBe('Event');
  });
});

describe('activityToEvent', () => {
  it('maps a full row', () => {
    const e = activityToEvent(
      item(42, { actor: 'alice', device_id: 'dev-1', command_id: 'cmd-1', category: 'command', action: 'device.wake', message: 'alice woke NAS' }),
      'NAS',
    );
    expect(e).toMatchObject({
      id: 'act-42',
      source: 'server',
      timestamp: '2026-11-14T10:00:00Z',
      actor: 'alice',
      category: 'wol',
      deviceId: 'dev-1',
      deviceName: 'NAS',
      commandId: 'cmd-1',
      title: 'Wake-on-LAN Requested',
      description: 'alice woke NAS',
      outcome: 'success',
    });
    expect(e.details?.rawOutput).toContain('command_id=cmd-1');
    expect(e.details?.rawOutput).toContain('actor=alice');
  });

  it('never assumes optional fields: actor, device_id, command_id', () => {
    const e = activityToEvent(item(1, { category: 'auth', action: 'auth.login', result: 'failed' }));
    expect(e.actor).toBeUndefined();
    expect(e.deviceId).toBeUndefined();
    expect(e.deviceName).toBeUndefined();
    expect(e.commandId).toBeUndefined();
    expect(e.outcome).toBe('error');
    expect(e.details?.rawOutput).not.toContain('actor=');
    expect(e.details?.rawOutput).not.toContain('device_id=');
  });

  it('survives unknown category/action/result values and an empty message', () => {
    const e = activityToEvent({ ...item(7), category: 'future' as never, action: 'thing.happened', result: 'weird', message: '' });
    expect(e).toMatchObject({ category: 'system', title: 'Thing happened', description: 'Thing happened', outcome: 'error' });
  });

  it('an empty-string actor counts as absent', () => {
    expect(activityToEvent(item(1, { actor: '' })).actor).toBeUndefined();
  });
});

describe('mergeActivity', () => {
  const names = new Map([['dev-1', 'NAS']]);

  it('uses feed rows as the base, newest first, with stable ids and device names', () => {
    const out = mergeActivity({
      server: [item(3, { at: '2026-11-14T10:03:00Z', device_id: 'dev-1' }), item(2, { at: '2026-11-14T10:02:00Z' }), item(1, { at: '2026-11-14T10:01:00Z' })],
      commands: [],
      deviceNames: names,
    });
    expect(out.map(e => e.id)).toEqual(['act-3', 'act-2', 'act-1']);
    expect(out[0].deviceName).toBe('NAS');
    expect(out[1].deviceName).toBeUndefined();
  });

  it('a feed row with a command_id absorbs the command: one line, final status and error text', () => {
    const out = mergeActivity({
      server: [item(9, { category: 'command', action: 'device.shutdown', command_id: 'c1', device_id: 'dev-1', actor: 'alice', message: 'alice shut down NAS', at: '2026-11-14T10:00:00Z' })],
      commands: [command('c1', { status: 'failed', error: 'agent rejected: clock_skew', error_code: 'clock_skew', completed_at: '2026-11-14T10:00:05Z' })],
      deviceNames: names,
    });
    expect(out).toHaveLength(1); // no second line for the command
    expect(out[0]).toMatchObject({
      id: 'act-9', // the stable feed id, not cmd-c1
      commandId: 'c1',
      title: 'Remote Shutdown Failed',
      description: 'agent rejected: clock_skew',
      outcome: 'error',
      actor: 'alice',
      timestamp: '2026-11-14T10:00:00Z', // request time: the line does not jump when the command ends
      category: 'shutdown',
    });
    expect(out[0].details?.rawOutput).toContain('error_code=clock_skew');
    expect(out[0].details?.rawOutput).toContain('activity_id=9');
  });

  it.each([
    ['completed', 'success', 'Device confirmed offline by the monitor.'],
    ['timed_out', 'warning', 'message 1 (timed out before the device changed state)'],
    ['sent', 'info', 'message 1'],
  ] as const)('absorbs a %s command', (status, outcome, description) => {
    const out = mergeActivity({
      server: [item(1, { category: 'command', action: 'device.shutdown', command_id: 'c1' })],
      commands: [command('c1', { status })],
    });
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ outcome, description });
  });

  it('a request that failed at the feed stays failed while the command row is still in flight', () => {
    const out = mergeActivity({
      server: [item(1, { category: 'command', action: 'device.wake', command_id: 'c1', result: 'failed' })],
      commands: [command('c1', { type: 'wake', status: 'pending' })],
    });
    expect(out[0].outcome).toBe('error');
  });

  it('commands without a feed row still appear, as before', () => {
    const out = mergeActivity({
      server: [item(1, { at: '2026-11-14T09:00:00Z' })],
      commands: [command('c-orphan', { type: 'wake', status: 'completed', completed_at: '2026-11-14T10:00:09Z' })],
      deviceNames: names,
    });
    expect(out.map(e => e.id)).toEqual(['cmd-c-orphan', 'act-1']);
    expect(out[0]).toMatchObject({ source: 'command', title: 'Wake-on-LAN Completed', deviceName: 'NAS' });
  });

  it('a command_id that is not in the command list (window) leaves the feed row as is', () => {
    const out = mergeActivity({ server: [item(1, { command_id: 'gone', category: 'command', action: 'device.wake' })], commands: [] });
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ id: 'act-1', title: 'Wake-on-LAN Requested', outcome: 'success' });
  });

  it('with a truncated feed, commands older than its oldest row are dropped (their rows were not loaded)', () => {
    const input = {
      server: [item(5, { at: '2026-11-14T10:00:00Z' })],
      commands: [command('old', { created_at: '2026-11-14T08:00:00Z', updated_at: '2026-11-14T08:00:00Z' }), command('new', { created_at: '2026-11-14T10:30:00Z', updated_at: '2026-11-14T10:30:00Z' })],
    };
    expect(mergeActivity({ ...input, serverTruncated: true }).map(e => e.id)).toEqual(['cmd-new', 'act-5']);
    expect(mergeActivity({ ...input, serverTruncated: false }).map(e => e.id)).toEqual(['cmd-new', 'act-5', 'cmd-old']);
  });

  it('sorts everything newest first; ties keep feed rows before commands before local events', () => {
    const at = '2026-11-14T10:00:00Z';
    const out = mergeActivity({
      server: [item(2, { at }), item(1, { at })],
      commands: [command('c1', { created_at: at, updated_at: at })],
      local: [local('local-a', at), local('local-new', '2026-11-14T10:10:00Z')],
    });
    expect(out.map(e => e.id)).toEqual(['local-new', 'act-2', 'act-1', 'cmd-c1', 'local-a']);
  });

  it('tolerates an unparsable timestamp (sorted last, no crash)', () => {
    const out = mergeActivity({ server: [item(1, { at: 'garbage' }), item(2)], commands: [] });
    expect(out.map(e => e.id)).toEqual(['act-2', 'act-1']);
  });

  it('is empty for empty inputs', () => {
    expect(mergeActivity({ server: [], commands: [] })).toEqual([]);
  });
});

describe('filterActivity', () => {
  const events = mergeActivity({
    server: [
      item(3, { category: 'auth', action: 'auth.login', actor: 'alice', message: 'alice signed in' }),
      item(2, { category: 'command', action: 'device.wake', device_id: 'dev-1', result: 'failed', message: 'wake failed' }),
      item(1, { category: 'ddns', action: 'ddns.sync', actor: 'bob', message: 'synced home.example.com' }),
    ],
    commands: [],
    deviceNames: new Map([['dev-1', 'NAS']]),
  });
  const ids = (f: Parameters<typeof filterActivity>[1]) => filterActivity(events, f).map(e => e.id);

  it('no filter, "all" and blanks keep everything', () => {
    expect(ids({})).toHaveLength(3);
    expect(ids({ category: 'all', outcome: 'all', deviceId: 'all', query: '  ' })).toHaveLength(3);
  });

  it('filters by UI category (wake is wol), outcome and device', () => {
    expect(ids({ category: 'wol' })).toEqual(['act-2']);
    expect(ids({ category: 'auth' })).toEqual(['act-3']);
    expect(ids({ outcome: 'error' })).toEqual(['act-2']);
    expect(ids({ deviceId: 'dev-1' })).toEqual(['act-2']);
  });

  it('searches title, description, device name and actor, case-insensitively', () => {
    expect(ids({ query: 'ALICE' })).toEqual(['act-3']);
    expect(ids({ query: 'nas' })).toEqual(['act-2']);
    expect(ids({ query: 'home.example' })).toEqual(['act-1']);
    expect(ids({ query: 'sign-in' })).toEqual(['act-3']);
    expect(ids({ query: 'nothing like this' })).toEqual([]);
  });

  it('combines filters', () => {
    expect(ids({ category: 'ddns', query: 'bob' })).toEqual(['act-1']);
    expect(ids({ category: 'auth', outcome: 'error' })).toEqual([]);
  });
});
