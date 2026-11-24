import React, { useCallback, useEffect, useState } from 'react';
import { Bell, Pause, Pencil, Play, Plus, RefreshCw, Send, Trash2, X } from 'lucide-react';
import {
  createAlertChannel,
  deleteAlertChannel,
  listAlertChannels,
  testAlertChannel,
  updateAlertChannel,
} from '../../api/endpoints';
import type { AlertChannel } from '../../api/schema';
import {
  ALERT_EVENTS,
  MAX_CHANNEL_NAME_LENGTH,
  TELEGRAM_TARGET_KEY,
  deliveryState,
  draftFromChannel,
  emptyDraft,
  eventLabel,
  toggleEvent,
  validateDraft,
} from '../../api/alerts';
import type { ChannelDraft, DeliveryTone } from '../../api/alerts';
import { relativeTime } from '../../api/serviceKeys';
import { errMsg } from '../../context/AppContext';
import { ConfirmDialog } from '../common/ConfirmDialog';

const inputCls =
  'w-full px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs';
const cardCls =
  'bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-4';
const headCls = 'border-b border-neutral-200 dark:border-neutral-800 pb-3';
const ghostBtn =
  'flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-lg transition-colors disabled:opacity-50';
const rowBtn =
  'flex items-center gap-1 px-2.5 py-1 text-[11px] font-medium text-neutral-600 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800 rounded-lg transition-colors disabled:opacity-50';

const toneCls: Record<DeliveryTone, string> = {
  ok: 'text-emerald-700 dark:text-emerald-300 bg-emerald-500/10 border-emerald-500/20',
  warn: 'text-amber-700 dark:text-amber-300 bg-amber-500/10 border-amber-500/20',
  idle: 'text-neutral-500 dark:text-neutral-400 bg-neutral-500/10 border-neutral-500/20',
  off: 'text-neutral-500 dark:text-neutral-400 bg-neutral-500/10 border-neutral-500/20',
};

/** Which channel the form edits: `null` = the form is closed, `'new'` = a new channel. */
type Editing = null | 'new' | AlertChannel;

/**
 * Alert channels (ADR-021): where HomeCore sends its alerts. Today the only adapter is Telegram (a
 * bot token and the id of the user that receives the messages). The bot token is write-only: the
 * Controller never returns it, so editing shows an empty token field that means "keep the stored one".
 * The token lives only in this component's form state until it is sent.
 */
export const AlertChannelsSection: React.FC = () => {
  const [channels, setChannels] = useState<AlertChannel[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());

  const [editing, setEditing] = useState<Editing>(null);
  const [draft, setDraft] = useState<ChannelDraft>(emptyDraft);
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const [busy, setBusy] = useState<string | null>(null);
  const [rowMsg, setRowMsg] = useState<{ id: string; ok: boolean; text: string } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<AlertChannel | null>(null);

  const load = useCallback(async () => {
    try {
      setChannels(await listAlertChannels());
      setLoadError(null);
      setNow(Date.now());
    } catch (e) {
      setLoadError(errMsg(e));
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  const open = (target: Exclude<Editing, null>) => {
    setEditing(target);
    setDraft(target === 'new' ? emptyDraft() : draftFromChannel(target));
    setFormError(null);
  };
  const close = () => {
    setEditing(null);
    setDraft(emptyDraft()); // drops the typed token
    setFormError(null);
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (editing === null) return;
    const isEdit = editing !== 'new';
    const v = validateDraft(draft, isEdit);
    if ('error' in v) return setFormError(v.error);
    setSaving(true);
    setFormError(null);
    try {
      const settings = { [TELEGRAM_TARGET_KEY]: v.value.targetUserId };
      if (isEdit) {
        await updateAlertChannel(editing.id, {
          name: v.value.name,
          enabled: draft.enabled,
          events: v.value.events,
          settings,
          ...(v.value.token ? { secret: v.value.token } : {}),
        });
      } else {
        await createAlertChannel({
          name: v.value.name,
          type: 'telegram',
          enabled: draft.enabled,
          events: v.value.events,
          settings,
          secret: v.value.token,
        });
      }
      close();
      await load();
    } catch (err) {
      setFormError(errMsg(err));
    } finally {
      setSaving(false);
    }
  };

  const toggleEnabled = async (c: AlertChannel) => {
    setBusy(c.id);
    setRowMsg(null);
    try {
      await updateAlertChannel(c.id, { enabled: !c.enabled });
    } catch (err) {
      setRowMsg({ id: c.id, ok: false, text: `Could not ${c.enabled ? 'pause' : 'resume'} "${c.name}": ${errMsg(err)}` });
    } finally {
      setBusy(null);
      await load();
    }
  };

  const sendTest = async (c: AlertChannel) => {
    setBusy(c.id);
    setRowMsg(null);
    try {
      const r = await testAlertChannel(c.id);
      setRowMsg(
        r.result === 'sent'
          ? { id: c.id, ok: true, text: 'Test message sent. Check Telegram.' }
          : { id: c.id, ok: false, text: `Test failed: ${r.error ?? 'unknown error'}` },
      );
    } catch (err) {
      setRowMsg({ id: c.id, ok: false, text: `Test failed: ${errMsg(err)}` });
    } finally {
      setBusy(null);
      await load();
    }
  };

  const remove = async (c: AlertChannel) => {
    setConfirmDelete(null);
    setBusy(c.id);
    setRowMsg(null);
    try {
      await deleteAlertChannel(c.id);
    } catch (err) {
      setRowMsg({ id: c.id, ok: false, text: `Could not delete "${c.name}": ${errMsg(err)}` });
    } finally {
      setBusy(null);
      await load();
    }
  };

  const isEdit = editing !== null && editing !== 'new';

  return (
    <section className={cardCls}>
      <div className={`${headCls} flex items-start justify-between gap-3`}>
        <div>
          <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
            <Bell size={16} className="text-neutral-500" />
            <span>Alerts</span>
          </h2>
          <p className="text-[11px] text-neutral-500 dark:text-neutral-400 mt-0.5">
            Get a message when the public IP changes, when a PC is woken or shut down, and when a PC goes online or offline
            without HomeCore doing it. Telegram is supported today; more adapters will follow.
          </p>
        </div>
        <button type="button" onClick={() => void load()} className={ghostBtn} aria-label="Refresh alert channels">
          <RefreshCw size={13} />
        </button>
      </div>

      {loadError ? (
        <p className="text-[11px] text-red-600 dark:text-red-400">{loadError}</p>
      ) : channels === null ? (
        <p className="text-[11px] text-neutral-400">Loading…</p>
      ) : channels.length === 0 ? (
        <p className="text-[11px] text-neutral-500">No alert channels yet. Add one to start receiving alerts.</p>
      ) : (
        <ul className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {channels.map(c => {
            const st = deliveryState(c);
            const working = busy === c.id;
            return (
              <li key={c.id} className="py-2.5 space-y-1.5">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
                  <div className="min-w-0 flex-1 basis-48">
                    <div className="font-medium text-neutral-900 dark:text-neutral-100 truncate">{c.name}</div>
                    <div className="text-[11px] text-neutral-500 dark:text-neutral-400">
                      Telegram → {c.settings[TELEGRAM_TARGET_KEY] ?? '—'} · {c.events.map(eventLabel).join(', ')}
                      {c.last_sent_at && (
                        <>
                          {' · last sent '}
                          <span title={new Date(c.last_sent_at).toLocaleString()}>{relativeTime(c.last_sent_at, now)}</span>
                        </>
                      )}
                    </div>
                  </div>
                  <span className={`px-2 py-0.5 rounded-full border text-[10px] font-medium ${toneCls[st.tone]}`}>{st.label}</span>
                  <div className="flex flex-wrap items-center gap-1">
                    <button type="button" disabled={working} onClick={() => void sendTest(c)} className={rowBtn} aria-label={`Send a test message to ${c.name}`}>
                      <Send size={12} />
                      <span>{working ? 'Working…' : 'Test'}</span>
                    </button>
                    <button type="button" disabled={working} onClick={() => void toggleEnabled(c)} className={rowBtn} aria-label={`${c.enabled ? 'Pause' : 'Resume'} ${c.name}`}>
                      {c.enabled ? <Pause size={12} /> : <Play size={12} />}
                      <span>{c.enabled ? 'Pause' : 'Resume'}</span>
                    </button>
                    <button type="button" disabled={working} onClick={() => open(c)} className={rowBtn} aria-label={`Edit ${c.name}`}>
                      <Pencil size={12} />
                      <span>Edit</span>
                    </button>
                    <button
                      type="button"
                      disabled={working}
                      onClick={() => setConfirmDelete(c)}
                      className="flex items-center gap-1 px-2.5 py-1 text-[11px] font-medium text-red-600 dark:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors disabled:opacity-50"
                      aria-label={`Delete ${c.name}`}
                    >
                      <Trash2 size={12} />
                      <span>Delete</span>
                    </button>
                  </div>
                </div>
                {st.detail && (
                  <p className="text-[11px] text-amber-700 dark:text-amber-300 break-words">
                    {st.detail}
                    {c.last_error_at && <span className="text-neutral-500"> ({relativeTime(c.last_error_at, now)})</span>}
                  </p>
                )}
                {rowMsg?.id === c.id && (
                  <p role="status" className={`text-[11px] break-words ${rowMsg.ok ? 'text-emerald-700 dark:text-emerald-300' : 'text-red-600 dark:text-red-400'}`}>
                    {rowMsg.text}
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {editing === null ? (
        <button type="button" onClick={() => open('new')} className="flex items-center gap-1.5 px-4 py-2 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg shadow-sm transition-colors">
          <Plus size={14} />
          <span>Add Telegram channel</span>
        </button>
      ) : (
        <form onSubmit={submit} className="border-t border-neutral-200 dark:border-neutral-800 pt-4 space-y-3" aria-label={isEdit ? 'Edit alert channel' : 'New alert channel'}>
          <div className="flex items-center justify-between">
            <h3 className="font-medium text-neutral-800 dark:text-neutral-200">{isEdit ? `Edit “${editing.name}”` : 'New Telegram channel'}</h3>
            <button type="button" onClick={close} className={ghostBtn} aria-label="Close the form">
              <X size={13} />
            </button>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <label className="block font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
              <span>Name</span>
              <input
                type="text"
                value={draft.name}
                maxLength={MAX_CHANNEL_NAME_LENGTH * 2}
                onChange={e => setDraft({ ...draft, name: e.target.value })}
                placeholder="e.g. My phone"
                className={inputCls}
              />
            </label>
            <label className="block font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
              <span>Target user id</span>
              <input
                type="text"
                inputMode="numeric"
                value={draft.targetUserId}
                onChange={e => setDraft({ ...draft, targetUserId: e.target.value })}
                placeholder="e.g. 123456789"
                autoComplete="off"
                className={`${inputCls} font-mono`}
              />
              <span className="block text-[11px] font-normal text-neutral-500">
                The numeric Telegram id that receives the messages (ask @userinfobot). It must have started a chat with the bot first.
              </span>
            </label>
          </div>

          <label className="block font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
            <span>Bot token</span>
            <input
              type="password"
              value={draft.token}
              onChange={e => setDraft({ ...draft, token: e.target.value })}
              placeholder={isEdit ? 'Leave blank to keep the saved token' : '123456789:AAH…'}
              autoComplete="new-password"
              spellCheck={false}
              className={`${inputCls} font-mono`}
            />
            <span className="block text-[11px] font-normal text-neutral-500">
              From @BotFather. It is stored encrypted and never shown again.
              {isEdit && editing.has_secret && ' A token is saved for this channel.'}
            </span>
          </label>

          <fieldset className="space-y-1.5">
            <legend className="font-medium text-neutral-700 dark:text-neutral-300 mb-1">Send an alert when</legend>
            {ALERT_EVENTS.map(ev => (
              <label key={ev.value} className="flex items-start gap-2.5 p-2.5 rounded-lg border border-neutral-200 dark:border-neutral-700 cursor-pointer hover:bg-neutral-50 dark:hover:bg-neutral-800/50">
                <input
                  type="checkbox"
                  checked={draft.events.includes(ev.value)}
                  onChange={() => setDraft({ ...draft, events: toggleEvent(draft.events, ev.value) })}
                  className="mt-0.5"
                />
                <span>
                  <span className="block font-medium text-neutral-800 dark:text-neutral-200">{ev.label}</span>
                  <span className="block text-[11px] text-neutral-500 dark:text-neutral-400">{ev.description}</span>
                </span>
              </label>
            ))}
          </fieldset>

          <label className="flex items-center gap-2 font-medium text-neutral-700 dark:text-neutral-300">
            <input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />
            <span>Enabled</span>
          </label>

          {formError && (
            <p role="alert" className="text-[11px] text-red-600 dark:text-red-400 break-words">
              {formError}
            </p>
          )}
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={saving}
              className="px-4 py-2 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 disabled:opacity-60 rounded-lg shadow-sm transition-colors"
            >
              {saving ? 'Saving…' : isEdit ? 'Save changes' : 'Add channel'}
            </button>
            <button type="button" onClick={close} className={ghostBtn}>
              Cancel
            </button>
          </div>
        </form>
      )}

      <ConfirmDialog
        open={confirmDelete !== null}
        title="Delete alert channel?"
        message={`Delete "${confirmDelete?.name ?? ''}"? It stops receiving alerts, and its saved bot token is removed.`}
        confirmLabel="Delete channel"
        onConfirm={() => confirmDelete && void remove(confirmDelete)}
        onCancel={() => setConfirmDelete(null)}
      />
    </section>
  );
};
