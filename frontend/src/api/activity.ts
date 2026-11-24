/**
 * The Activity screen's data: the Controller's activity feed (`GET /activity`, built from the
 * audit log) is the base; wake/shutdown outcomes come from the command history (the feed only
 * records the request); browser-local events cover what the server cannot know (probes).
 * Everything here is pure so it can be tested without React or a Controller.
 */
import type { ActivityEvent } from '../types';
import { commandToActivity } from './mappers';
import { isTerminal } from './commandPoll';
import type { ActivityItem, ApiCommand } from './schema';

type Outcome = ActivityEvent['outcome'];
type Category = ActivityEvent['category'];

/** Known actions get a proper title; unknown ones are humanised (clients must tolerate new values). */
const TITLES: Record<string, string> = {
  'auth.login': 'Sign-in',
  'auth.logout': 'Sign-out',
  'auth.refresh_reuse': 'Refresh Token Reuse Detected',
  'user.create': 'User Created',
  'user.password_change': 'Password Changed',
  'device.create': 'Device Registered',
  'device.update': 'Device Updated',
  'device.delete': 'Device Removed',
  'device.agent_remove': 'Agent Binding Removed',
  'device.wake': 'Wake-on-LAN Requested',
  'device.shutdown': 'Shutdown Requested',
  'ddns.create': 'DDNS Record Added',
  'ddns.update': 'DDNS Settings Updated',
  'ddns.delete': 'DDNS Record Removed',
  'ddns.sync': 'DDNS Sync',
  'controller_key.create': 'Controller Key Created',
};

export function humanizeAction(action: string | undefined): string {
  if (!action) return 'Event';
  const known = TITLES[action];
  if (known) return known;
  const words = action.replace(/[._-]+/g, ' ').trim();
  return words ? words.charAt(0).toUpperCase() + words.slice(1) : 'Event';
}

/** Server category (+ action for `command`) -> the UI's category, which has separate wake/shutdown icons. */
export function categoryOf(category: string | undefined, action: string | undefined): Category {
  switch (category) {
    case 'command':
      return action === 'device.wake' ? 'wol' : action === 'device.shutdown' ? 'shutdown' : 'system';
    case 'auth':
    case 'device':
    case 'ddns':
    case 'user':
    case 'system':
      return category;
    default:
      return 'system'; // a category this UI does not know yet
  }
}

/** `result` is `ok`, `failed` or an error code. Absent/empty: nothing to judge. */
export function outcomeOf(result: string | undefined): Outcome {
  if (!result) return 'info';
  return result === 'ok' ? 'success' : 'error';
}

/** One feed entry as a UI event. Only `id`, `at`, `category`, `action`, `result`, `message` are guaranteed. */
export function activityToEvent(item: ActivityItem, deviceName?: string): ActivityEvent {
  const title = humanizeAction(item.action);
  return {
    id: `act-${item.id}`,
    source: 'server',
    timestamp: item.at,
    actor: item.actor || undefined,
    category: categoryOf(item.category, item.action),
    deviceId: item.device_id || undefined,
    deviceName,
    commandId: item.command_id || undefined,
    title,
    description: item.message || title,
    outcome: outcomeOf(item.result),
    details: {
      rawOutput: [
        `activity_id=${item.id}`,
        `action=${item.action}`,
        `result=${item.result}`,
        item.actor && `actor=${item.actor}`,
        item.device_id && `device_id=${item.device_id}`,
        item.command_id && `command_id=${item.command_id}`,
      ]
        .filter(Boolean)
        .join('\n'),
    },
  };
}

/** The feed line for a wake/shutdown request, completed with the command's current state. */
function absorbCommand(base: ActivityEvent, c: ApiCommand, deviceName?: string): ActivityEvent {
  const cmd = commandToActivity(c, deviceName);
  let description = base.description;
  if (c.status === 'completed') description = cmd.description;
  else if (c.status === 'failed') description = c.error ?? base.description;
  else if (c.status === 'timed_out') description = `${base.description} (timed out before the device changed state)`;
  return {
    ...base, // keeps the stable `act-` id, the request time, the actor and the device
    commandId: c.id,
    title: cmd.title,
    description,
    // a request that already failed stays failed even if the command row is still in flight
    outcome: base.outcome === 'error' && !isTerminal(c) ? 'error' : cmd.outcome,
    details: {
      ...cmd.details,
      rawOutput: [base.details?.rawOutput, cmd.details?.rawOutput].filter(Boolean).join('\n'),
    },
  };
}

export interface MergeInput {
  /** `GET /activity`, newest first. */
  server: readonly ActivityItem[];
  /** The feed has older entries than `server` holds (page cap reached). */
  serverTruncated?: boolean;
  /** `GET /commands`. */
  commands: readonly ApiCommand[];
  /** Browser-local events (e.g. probes); the caller keeps only what the feed does not cover. */
  local?: readonly ActivityEvent[];
  deviceNames?: ReadonlyMap<string, string>;
}

const ts = (e: { timestamp: string }) => {
  const t = Date.parse(e.timestamp);
  return Number.isNaN(t) ? 0 : t;
};

/**
 * Feed rows are the base. A row carrying a `command_id` absorbs that command's state instead of
 * producing a second line; commands without a row still appear as before (except those older than
 * a truncated feed window, whose row simply was not loaded). Newest first; equal timestamps keep
 * the order feed rows, commands, local events (the sort is stable).
 */
export function mergeActivity({ server, serverTruncated = false, commands, local = [], deviceNames }: MergeInput): ActivityEvent[] {
  const byCommand = new Map(commands.map(c => [c.id, c]));
  const absorbed = new Set<string>();
  const name = (id?: string) => (id ? deviceNames?.get(id) : undefined);

  const rows = server.map(item => {
    const base = activityToEvent(item, name(item.device_id));
    const c = item.command_id ? byCommand.get(item.command_id) : undefined;
    if (!c) return base;
    absorbed.add(c.id);
    return absorbCommand(base, c, name(c.device_id) ?? base.deviceName);
  });

  const oldest = server.reduce((min, i) => Math.min(min, Date.parse(i.at) || Infinity), Infinity);
  const rest = commands
    .filter(c => !absorbed.has(c.id))
    .filter(c => !(serverTruncated && Date.parse(c.created_at) < oldest))
    .map(c => commandToActivity(c, name(c.device_id)));

  return [...rows, ...rest, ...local].sort((a, b) => ts(b) - ts(a));
}

export interface ActivityFilter {
  category?: string;
  outcome?: string;
  deviceId?: string;
  query?: string;
}

/** `all`/undefined/'' mean "no filter". Search looks at title, description, device and actor. */
export function filterActivity(events: readonly ActivityEvent[], f: ActivityFilter): ActivityEvent[] {
  const on = (v?: string) => (v && v !== 'all' ? v : undefined);
  const category = on(f.category);
  const outcome = on(f.outcome);
  const deviceId = on(f.deviceId);
  const q = (f.query ?? '').trim().toLowerCase();
  return events.filter(
    e =>
      (!category || e.category === category) &&
      (!outcome || e.outcome === outcome) &&
      (!deviceId || e.deviceId === deviceId) &&
      (!q || [e.title, e.description, e.deviceName, e.actor].some(s => s?.toLowerCase().includes(q))),
  );
}
