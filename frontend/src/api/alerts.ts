/**
 * Helpers for the alert channels screen (ADR-021): the events a channel can receive, client-side
 * checks of the Telegram fields (the Controller validates again), and the wording of a channel's
 * delivery state. Pure, so it is unit tested.
 */
import type { AlertChannel, AlertEvent } from './schema';

export const MAX_CHANNEL_NAME_LENGTH = 100;
export const TELEGRAM_TARGET_KEY = 'target_user_id';

export const ALERT_EVENTS: { value: AlertEvent; label: string; description: string }[] = [
  { value: 'ip_changed', label: 'Public IP changed', description: 'A DDNS record was moved to a new public address.' },
  { value: 'power_command', label: 'Power on / off', description: 'A wake or shutdown request was accepted (or failed).' },
  { value: 'status_changed', label: 'Unexpected status change', description: 'A device went online or offline without a wake or shutdown from HomeCore: power loss, crash, manual start.' },
];

export const ALL_EVENTS: AlertEvent[] = ALERT_EVENTS.map(e => e.value);

export const eventLabel = (e: AlertEvent): string => ALERT_EVENTS.find(x => x.value === e)?.label ?? e;

/** Same rule as the Controller: "<bot id>:<secret>" from BotFather. */
const BOT_TOKEN = /^[0-9]{3,}:[A-Za-z0-9_-]{20,}$/;
/** A user id is a number; a group or channel id is negative. */
const TARGET_ID = /^-?[0-9]{1,20}$/;

export interface ChannelDraft {
  name: string;
  targetUserId: string;
  /** The bot token. Empty while editing means "keep the stored one". */
  token: string;
  events: AlertEvent[];
  enabled: boolean;
}

export const emptyDraft = (): ChannelDraft => ({ name: '', targetUserId: '', token: '', events: [...ALL_EVENTS], enabled: true });

export const draftFromChannel = (c: AlertChannel): ChannelDraft => ({
  name: c.name,
  targetUserId: c.settings[TELEGRAM_TARGET_KEY] ?? '',
  token: '',
  events: c.events,
  enabled: c.enabled,
});

/** Trimmed fields, or the first problem as a message. `editing`: a blank token keeps the stored one. */
export function validateDraft(
  d: ChannelDraft,
  editing: boolean,
): { value: { name: string; targetUserId: string; token: string; events: AlertEvent[] } } | { error: string } {
  const name = d.name.trim();
  if (name === '') return { error: 'Give the channel a name, for example "My phone".' };
  if ([...name].length > MAX_CHANNEL_NAME_LENGTH) return { error: `The name can have at most ${MAX_CHANNEL_NAME_LENGTH} characters.` };
  if (/[\u0000-\u001f\u007f]/.test(name)) return { error: 'The name cannot contain control characters.' };
  const targetUserId = d.targetUserId.trim();
  if (!TARGET_ID.test(targetUserId)) return { error: 'Target user id must be the numeric Telegram id (a group id starts with -).' };
  const token = d.token.trim();
  if (token === '' ? !editing : !BOT_TOKEN.test(token)) {
    return { error: 'Bot token must look like 123456789:AAH… (from BotFather).' };
  }
  const events = ALL_EVENTS.filter(e => d.events.includes(e));
  if (events.length === 0) return { error: 'Choose at least one event to be alerted about.' };
  return { value: { name, targetUserId, token, events } };
}

/** Events in documentation order, for a stable display and request body. */
export const toggleEvent = (events: AlertEvent[], e: AlertEvent): AlertEvent[] =>
  ALL_EVENTS.filter(x => (x === e ? !events.includes(e) : events.includes(x)));

export type DeliveryTone = 'ok' | 'warn' | 'idle' | 'off';

/**
 * What to show for a channel's last delivery. A failure counts only while it is newer than the last
 * success (the Controller clears `last_error` on success, this also covers clock skew in tests).
 */
export function deliveryState(
  c: Pick<AlertChannel, 'enabled' | 'last_sent_at' | 'last_error' | 'last_error_at'>,
): { label: string; tone: DeliveryTone; detail?: string } {
  if (!c.enabled) return { label: 'Paused', tone: 'off' };
  const failedAt = c.last_error_at ? Date.parse(c.last_error_at) : NaN;
  const sentAt = c.last_sent_at ? Date.parse(c.last_sent_at) : NaN;
  if (c.last_error && (Number.isNaN(sentAt) || failedAt > sentAt)) return { label: 'Last delivery failed', tone: 'warn', detail: c.last_error };
  if (!Number.isNaN(sentAt)) return { label: 'Delivered', tone: 'ok' };
  return { label: 'Nothing sent yet', tone: 'idle' };
}
