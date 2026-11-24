import React, { useCallback, useEffect, useRef, useState } from 'react';
import { AlertTriangle, Bot, Check, Copy, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { createServiceAccountKey, listServiceAccountKeys, revokeServiceAccountKey } from '../../api/endpoints';
import type { ServiceKey, ServiceKeyCreated, ServiceKeyScope } from '../../api/schema';
import {
  DEFAULT_TTL_DAYS,
  MAX_NAME_LENGTH,
  SCOPES,
  TTL_PRESETS,
  canRevoke,
  keyState,
  relativeTime,
  scopeLabel,
  ttlSecondsFromDays,
  usageExamples,
  validateKeyName,
} from '../../api/serviceKeys';
import type { KeyTone } from '../../api/serviceKeys';
import { errMsg } from '../../context/AppContext';
import { ConfirmDialog } from '../common/ConfirmDialog';

const inputCls =
  'w-full px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs';
const cardCls =
  'bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-4';
const headCls = 'border-b border-neutral-200 dark:border-neutral-800 pb-3';
const ghostBtn =
  'flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-lg transition-colors disabled:opacity-50';

const toneCls: Record<KeyTone, string> = {
  ok: 'text-emerald-700 dark:text-emerald-300 bg-emerald-500/10 border-emerald-500/20',
  warn: 'text-amber-700 dark:text-amber-300 bg-amber-500/10 border-amber-500/20',
  dead: 'text-neutral-500 dark:text-neutral-400 bg-neutral-500/10 border-neutral-500/20',
};

/** Copy text; returns false when the clipboard is unavailable (the text stays selectable). */
async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

const CopyButton: React.FC<{ text: string; label?: string }> = ({ text, label = 'Copy' }) => {
  const [done, setDone] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <button
      type="button"
      onClick={async () => {
        if (await copyText(text)) {
          setDone(true);
          clearTimeout(timer.current);
          timer.current = setTimeout(() => setDone(false), 2000);
        }
      }}
      className="flex items-center gap-1 text-emerald-600 dark:text-emerald-400 hover:underline text-[11px] font-medium shrink-0"
    >
      {done ? <Check size={12} /> : <Copy size={12} />}
      <span>{done ? 'Copied' : label}</span>
    </button>
  );
};

/**
 * Service account keys (ADR-018): long-lived credentials for AI agents (MCP servers) and legacy
 * apps. A user creates, lists and revokes their own keys here; a key rotates itself through
 * `POST /auth/service-key/rotate`, so there is no rotate button. The secret is returned once by the
 * create call: it lives only in this component's state until the user dismisses it.
 */
export const ServiceKeysSection: React.FC = () => {
  const [keys, setKeys] = useState<ServiceKey[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());

  const [name, setName] = useState('');
  const [scope, setScope] = useState<ServiceKeyScope>('mcp');
  const [days, setDays] = useState<string>(String(DEFAULT_TTL_DAYS));
  const [formError, setFormError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<ServiceKeyCreated | null>(null);
  const [revoking, setRevoking] = useState<string | null>(null);
  const [confirmRevoke, setConfirmRevoke] = useState<ServiceKey | null>(null);
  const [rowError, setRowError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setKeys(await listServiceAccountKeys());
      setLoadError(null);
      setNow(Date.now());
    } catch (e) {
      setLoadError(errMsg(e));
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);
    const n = validateKeyName(name);
    if ('error' in n) return setFormError(n.error);
    const ttl = ttlSecondsFromDays(Number(days));
    if ('error' in ttl) return setFormError(ttl.error);
    setCreating(true);
    try {
      const key = await createServiceAccountKey({ name: n.name, scope, ttl_seconds: ttl.seconds });
      setCreated(key);
      setName('');
      await load();
    } catch (err) {
      setFormError(errMsg(err));
    } finally {
      setCreating(false);
    }
  };

  const revoke = async (k: ServiceKey) => {
    setConfirmRevoke(null);
    setRevoking(k.id);
    setRowError(null);
    try {
      await revokeServiceAccountKey(k.id);
    } catch (err) {
      setRowError(`Could not revoke "${k.name}": ${errMsg(err)}`);
    } finally {
      setRevoking(null);
      await load();
    }
  };

  const origin = typeof window === 'undefined' ? '' : window.location.origin;

  return (
    <section className={cardCls}>
      <div className={`${headCls} flex items-start justify-between gap-3`}>
        <div>
          <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
            <Bot size={16} className="text-neutral-500" />
            <span>Service Account Keys</span>
          </h2>
          <p className="text-[11px] text-neutral-500 dark:text-neutral-400 mt-0.5">
            Long-lived keys for AI agents (MCP servers) and legacy apps. They act as you, within their scope, and can be revoked one by one.
            A key replaces itself when its agent calls <code>POST /api/v1/auth/service-key/rotate</code>.
          </p>
        </div>
        <button type="button" onClick={() => void load()} className={ghostBtn} aria-label="Refresh keys">
          <RefreshCw size={13} />
        </button>
      </div>

      {/* The secret, once. */}
      {created && (
        <div role="status" className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-3.5 space-y-2.5">
          <div className="flex items-start gap-2 text-[11px] text-amber-800 dark:text-amber-200">
            <AlertTriangle size={14} className="shrink-0 mt-0.5" />
            <span>
              <strong>Copy this key now.</strong> It is shown only once and cannot be recovered; treat it like a password. If you lose it, create a new key and revoke this one.
            </span>
          </div>
          <div className="flex items-start justify-between gap-3">
            <code data-testid="new-key" className="font-mono text-[11px] break-all text-neutral-900 dark:text-neutral-100 select-all">{created.key}</code>
            <CopyButton text={created.key} />
          </div>
          <div className="text-[11px] text-neutral-600 dark:text-neutral-300">
            “{created.name}” · {scopeLabel(created.scope)} · expires {new Date(created.expires_at).toLocaleDateString()} ({relativeTime(created.expires_at)})
          </div>
          <div className="space-y-1.5">
            {usageExamples(origin, created.scope).map(ex => (
              <div key={ex.label}>
                <div className="text-[10px] uppercase tracking-wide text-neutral-500">{ex.label}</div>
                <div className="flex items-start justify-between gap-3">
                  <code className="font-mono text-[11px] break-all text-neutral-700 dark:text-neutral-300 select-all">{ex.command}</code>
                  <CopyButton text={ex.command} />
                </div>
              </div>
            ))}
          </div>
          <button type="button" onClick={() => setCreated(null)} className={ghostBtn}>
            I have saved the key
          </button>
        </div>
      )}

      {/* Keys */}
      {loadError ? (
        <p className="text-[11px] text-red-600 dark:text-red-400">{loadError}</p>
      ) : keys === null ? (
        <p className="text-[11px] text-neutral-400">Loading…</p>
      ) : keys.length === 0 ? (
        <p className="text-[11px] text-neutral-500">No keys yet. Create one below.</p>
      ) : (
        <ul className="divide-y divide-neutral-100 dark:divide-neutral-800">
          {keys.map(k => {
            const st = keyState(k, now);
            return (
              <li key={k.id} className="py-2.5 flex flex-wrap items-center gap-x-3 gap-y-1.5">
                <div className="min-w-0 flex-1 basis-48">
                  <div className="font-medium text-neutral-900 dark:text-neutral-100 truncate">{k.name}</div>
                  <div className="text-[11px] text-neutral-500 dark:text-neutral-400">
                    {scopeLabel(k.scope)} · expires{' '}
                    <span title={new Date(k.expires_at).toLocaleString()}>{relativeTime(k.expires_at, now)}</span>
                    {' · '}
                    {k.last_used_at ? (
                      <span title={new Date(k.last_used_at).toLocaleString()}>used {relativeTime(k.last_used_at, now)}</span>
                    ) : (
                      'never used'
                    )}
                  </div>
                </div>
                <span className={`px-2 py-0.5 rounded-full border text-[10px] font-medium ${toneCls[st.tone]}`}>{st.label}</span>
                {canRevoke(k, now) && (
                  <button
                    type="button"
                    disabled={revoking === k.id}
                    onClick={() => setConfirmRevoke(k)}
                    className="flex items-center gap-1 px-2.5 py-1 text-[11px] font-medium text-red-600 dark:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors disabled:opacity-50"
                    aria-label={`Revoke ${k.name}`}
                  >
                    <Trash2 size={12} />
                    <span>{revoking === k.id ? 'Revoking…' : 'Revoke'}</span>
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {rowError && <p className="text-[11px] text-red-600 dark:text-red-400">{rowError}</p>}

      {/* Create */}
      <form onSubmit={submit} className="border-t border-neutral-200 dark:border-neutral-800 pt-4 space-y-3">
        <h3 className="font-medium text-neutral-800 dark:text-neutral-200">Create a key</h3>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <label className="block font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
            <span>Name</span>
            <input
              type="text"
              value={name}
              maxLength={MAX_NAME_LENGTH * 2}
              onChange={e => setName(e.target.value)}
              placeholder="e.g. claude mcp"
              className={inputCls}
            />
          </label>
          <div className="space-y-1">
            <label htmlFor="key-ttl" className="block font-medium text-neutral-700 dark:text-neutral-300">
              Lifetime (days)
            </label>
            <div className="flex flex-wrap items-center gap-2">
              <input
                id="key-ttl"
                type="number"
                min={1}
                step={1}
                value={days}
                onChange={e => setDays(e.target.value)}
                className={inputCls.replace('w-full', 'w-28')}
              />
              {TTL_PRESETS.map(p => (
                <button
                  key={p.days}
                  type="button"
                  onClick={() => setDays(String(p.days))}
                  className={`px-2 py-1 rounded-lg text-[11px] border transition-colors ${
                    Number(days) === p.days
                      ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                      : 'border-neutral-200 dark:border-neutral-700 text-neutral-600 dark:text-neutral-400 hover:bg-neutral-100 dark:hover:bg-neutral-800'
                  }`}
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>
        </div>

        <fieldset className="space-y-1.5">
          <legend className="font-medium text-neutral-700 dark:text-neutral-300 mb-1">Scope</legend>
          {SCOPES.map(s => (
            <label
              key={s.value}
              className={`flex items-start gap-2.5 p-2.5 rounded-lg border cursor-pointer transition-colors ${
                scope === s.value
                  ? 'border-emerald-500/40 bg-emerald-500/5'
                  : 'border-neutral-200 dark:border-neutral-700 hover:bg-neutral-50 dark:hover:bg-neutral-800/50'
              }`}
            >
              <input type="radio" name="key-scope" value={s.value} checked={scope === s.value} onChange={() => setScope(s.value)} className="mt-0.5" />
              <span>
                <span className="block font-medium text-neutral-800 dark:text-neutral-200">{s.label}</span>
                <span className="block text-[11px] text-neutral-500 dark:text-neutral-400">{s.description}</span>
              </span>
            </label>
          ))}
        </fieldset>

        {formError && (
          <p role="alert" className="text-[11px] text-red-600 dark:text-red-400">
            {formError}
          </p>
        )}
        <button
          type="submit"
          disabled={creating}
          className="flex items-center gap-1.5 px-4 py-2 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 disabled:opacity-60 rounded-lg shadow-sm transition-colors"
        >
          <Plus size={14} />
          <span>{creating ? 'Creating…' : 'Create key'}</span>
        </button>
      </form>
      <ConfirmDialog
        open={confirmRevoke !== null}
        title="Revoke service key?"
        message={`Revoke "${confirmRevoke?.name ?? ''}"? Anything still using this key stops working immediately.`}
        confirmLabel="Revoke key"
        onConfirm={() => confirmRevoke && void revoke(confirmRevoke)}
        onCancel={() => setConfirmRevoke(null)}
      />
    </section>
  );
};
