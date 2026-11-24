import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { ArrowLeftRight, Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import { createPortForward, deletePortForward, listPortForwards, updatePortForward } from '../../api/endpoints';
import type { PortForward } from '../../api/schema';
import {
  MAX_RULE_NAME_LENGTH,
  PROTOCOLS,
  draftFromRule,
  emptyDraft,
  interfaceOptions,
  protocolLabel,
  statusState,
  validateDraft,
} from '../../api/portForward';
import type { RuleDraft, StatusTone } from '../../api/portForward';
import { errMsg, useApp } from '../../context/AppContext';
import { usePolling } from '../../hooks/usePolling';
import { ConfirmDialog } from '../common/ConfirmDialog';
import { CustomSelect } from '../common/CustomSelect';

const inputCls =
  'w-full px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs focus:border-emerald-500 focus:outline-hidden';
const cardCls =
  'bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-4';
const headCls = 'border-b border-neutral-200 dark:border-neutral-800 pb-3';
const ghostBtn =
  'flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-lg transition-colors disabled:opacity-50';
const rowBtn =
  'flex items-center gap-1 px-2.5 py-1 text-[11px] font-medium text-neutral-600 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800 rounded-lg transition-colors disabled:opacity-50';

const toneCls: Record<StatusTone, string> = {
  ok: 'text-emerald-700 dark:text-emerald-300 bg-emerald-500/10 border-emerald-500/20',
  warn: 'text-amber-700 dark:text-amber-300 bg-amber-500/10 border-amber-500/20',
  off: 'text-neutral-500 dark:text-neutral-400 bg-neutral-500/10 border-neutral-500/20',
};

/** Which rule the form edits: `null` = closed, `'new'` = a new rule. */
type Editing = null | 'new' | PortForward;

const endpoint = (host: string, port: number) => (host.includes(':') ? `[${host}]:${port}` : `${host}:${port}`);

/** On/off switch (role="switch"). The stored state changes only after the Controller accepted it. */
const Switch: React.FC<{ checked: boolean; disabled?: boolean; label: string; onChange: () => void }> = ({ checked, disabled, label, onChange }) => (
  <button
    type="button"
    role="switch"
    aria-checked={checked}
    aria-label={label}
    title={label}
    disabled={disabled}
    onClick={onChange}
    className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 ${
      checked ? 'bg-emerald-600' : 'bg-neutral-300 dark:bg-neutral-700'
    }`}
  >
    <span className={`inline-block h-4 w-4 rounded-full bg-white shadow-sm transition-transform ${checked ? 'translate-x-4.5' : 'translate-x-0.5'}`} />
  </button>
);

/**
 * Port forwarding (ADR-022): the Controller listens on a local interface and relays TCP and/or UDP
 * traffic to a target. Each rule has a stored on/off switch; the Controller restores enabled rules
 * after a restart, and `status` shows what is happening now (a rule whose port is busy shows the error
 * and is retried by the Controller).
 */
export const PortForwardScreen: React.FC = () => {
  const { systemMetrics } = useApp();
  const [rules, setRules] = useState<PortForward[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [editing, setEditing] = useState<Editing>(null);
  const [draft, setDraft] = useState<RuleDraft>(emptyDraft);
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const [busy, setBusy] = useState<string | null>(null);
  const [rowMsg, setRowMsg] = useState<{ id: string; text: string } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<PortForward | null>(null);

  const load = useCallback(async () => {
    try {
      setRules(await listPortForwards());
      setLoadError(null);
    } catch (e) {
      setLoadError(errMsg(e));
    }
  }, []);
  // Status changes without a click (a retry succeeds, the port frees up), so poll.
  usePolling(load, 10_000);

  const options = useMemo(
    () => interfaceOptions(systemMetrics?.localAddresses ?? [], draft.listenAddress.trim() || undefined),
    [systemMetrics, draft.listenAddress],
  );
  // Without a list of host addresses (a platform that hides them) the field is typed by hand.
  const [manualInterface, setManualInterface] = useState(false);
  const showSelect = !manualInterface;

  const open = (target: Exclude<Editing, null>) => {
    setEditing(target);
    setDraft(target === 'new' ? emptyDraft() : draftFromRule(target));
    setManualInterface((systemMetrics?.localAddresses.length ?? 0) === 0);
    setFormError(null);
  };
  const close = () => {
    setEditing(null);
    setDraft(emptyDraft());
    setFormError(null);
  };

  useEffect(() => {
    if (editing === null || saving) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [editing, saving]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (editing === null) return;
    const v = validateDraft(draft);
    if ('error' in v) return setFormError(v.error);
    setSaving(true);
    setFormError(null);
    const body = {
      name: v.value.name,
      protocol: v.value.protocol,
      listen_address: v.value.listenAddress,
      listen_port: v.value.listenPort,
      target_host: v.value.targetHost,
      target_port: v.value.targetPort,
      enabled: v.value.enabled,
    };
    try {
      if (editing === 'new') await createPortForward(body);
      else await updatePortForward(editing.id, body);
      close();
      await load();
    } catch (err) {
      setFormError(errMsg(err));
    } finally {
      setSaving(false);
    }
  };

  const toggle = async (r: PortForward) => {
    setBusy(r.id);
    setRowMsg(null);
    try {
      await updatePortForward(r.id, { enabled: !r.enabled });
    } catch (err) {
      setRowMsg({ id: r.id, text: `Could not ${r.enabled ? 'turn off' : 'turn on'} "${r.name}": ${errMsg(err)}` });
    } finally {
      setBusy(null);
      await load();
    }
  };

  const remove = async (r: PortForward) => {
    setConfirmDelete(null);
    setBusy(r.id);
    setRowMsg(null);
    try {
      await deletePortForward(r.id);
    } catch (err) {
      setRowMsg({ id: r.id, text: `Could not delete "${r.name}": ${errMsg(err)}` });
    } finally {
      setBusy(null);
      await load();
    }
  };

  const isEdit = editing !== null && editing !== 'new';
  const label = 'block font-medium text-neutral-700 dark:text-neutral-300 space-y-1';

  return (
    <div className="space-y-6 text-xs max-w-4xl">
      <div>
        <h1 className="text-lg font-semibold text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
          <ArrowLeftRight size={18} className="text-neutral-500" />
          <span>Tunnel</span>
        </h1>
        <p className="text-neutral-500 dark:text-neutral-400 mt-1">
          This Controller listens on a local interface and relays the traffic to another host. Rules that are switched on come back by themselves after a restart.
          Ports below 1024 may need extra privileges on the Controller host.
        </p>
      </div>

      <section className={cardCls}>
        <div className={`${headCls} flex items-start justify-between gap-3`}>
          <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100">Rules</h2>
          <button type="button" onClick={() => void load()} className={ghostBtn} aria-label="Refresh tunnel rules">
            <RefreshCw size={13} />
          </button>
        </div>

        {loadError ? (
          <p className="text-[11px] text-red-600 dark:text-red-400">{loadError}</p>
        ) : rules === null ? (
          <p className="text-[11px] text-neutral-400">Loading…</p>
        ) : rules.length === 0 ? (
          <p className="text-[11px] text-neutral-500">No tunnel rules yet. Add one to start relaying traffic.</p>
        ) : (
          <ul className="divide-y divide-neutral-100 dark:divide-neutral-800">
            {rules.map(r => {
              const st = statusState(r);
              const working = busy === r.id;
              return (
                <li key={r.id} className="py-2.5 space-y-1.5">
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
                    <Switch checked={r.enabled} disabled={working} label={`Enable ${r.name}`} onChange={() => void toggle(r)} />
                    <div className="min-w-0 flex-1 basis-48">
                      <div className="font-medium text-neutral-900 dark:text-neutral-100 truncate">{r.name}</div>
                      <div className="text-[11px] text-neutral-500 dark:text-neutral-400 font-mono break-all">
                        {protocolLabel(r.protocol)} {endpoint(r.listen_address, r.listen_port)} → {endpoint(r.target_host, r.target_port)}
                      </div>
                    </div>
                    <span className={`px-2 py-0.5 rounded-full border text-[10px] font-medium ${toneCls[st.tone]}`}>{st.label}</span>
                    <div className="flex flex-wrap items-center gap-1">
                      <button type="button" disabled={working} onClick={() => open(r)} className={rowBtn} aria-label={`Edit ${r.name}`}>
                        <Pencil size={12} />
                        <span>Edit</span>
                      </button>
                      <button
                        type="button"
                        disabled={working}
                        onClick={() => setConfirmDelete(r)}
                        className="flex items-center gap-1 px-2.5 py-1 text-[11px] font-medium text-red-600 dark:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors disabled:opacity-50"
                        aria-label={`Delete ${r.name}`}
                      >
                        <Trash2 size={12} />
                        <span>Delete</span>
                      </button>
                    </div>
                  </div>
                  {st.detail && <p className="text-[11px] text-amber-700 dark:text-amber-300 break-words">{st.detail}</p>}
                  {rowMsg?.id === r.id && (
                    <p role="status" className="text-[11px] text-red-600 dark:text-red-400 break-words">
                      {rowMsg.text}
                    </p>
                  )}
                </li>
              );
            })}
          </ul>
        )}

        <button type="button" onClick={() => open('new')} className="flex items-center gap-1.5 px-4 py-2 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg shadow-sm transition-colors">
          <Plus size={14} />
          <span>Add rule</span>
        </button>
      </section>

      {editing !== null && (
        <div
          className="fixed inset-0 z-[60] flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs animate-in fade-in duration-150"
          onClick={() => !saving && close()}
        >
          <form
            onSubmit={submit}
            role="dialog"
            aria-modal="true"
            aria-labelledby="pf-dialog-title"
            onClick={e => e.stopPropagation()}
            className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl max-w-xl w-full shadow-2xl flex flex-col max-h-[90vh] text-xs"
          >
            <div className="flex items-center justify-between px-5 py-3.5 border-b border-neutral-200 dark:border-neutral-800">
              <h3 id="pf-dialog-title" className="font-semibold text-sm text-neutral-900 dark:text-neutral-100">
                {isEdit ? `Edit “${editing.name}”` : 'New tunnel rule'}
              </h3>
              <button type="button" onClick={close} disabled={saving} className={ghostBtn} aria-label="Close the form">
                <X size={13} />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-5 space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <label className={label}>
                <span>Name</span>
                <input
                  type="text"
                  value={draft.name}
                  maxLength={MAX_RULE_NAME_LENGTH * 2}
                  onChange={e => setDraft({ ...draft, name: e.target.value })}
                  placeholder="e.g. NAS web"
                  className={inputCls}
                />
              </label>
              <div className={label}>
                <span>Connection type</span>
                <CustomSelect
                  value={draft.protocol}
                  onChange={v => setDraft({ ...draft, protocol: v as RuleDraft['protocol'] })}
                  options={PROTOCOLS.map(p => ({ value: p.value, label: p.label, sublabel: p.description }))}
                  compactTrigger
                />
              </div>

              <div className={label}>
                <span>Local interface</span>
                {showSelect ? (
                  <CustomSelect value={draft.listenAddress} onChange={v => setDraft({ ...draft, listenAddress: v })} options={options} />
                ) : (
                  <input
                    type="text"
                    value={draft.listenAddress}
                    onChange={e => setDraft({ ...draft, listenAddress: e.target.value })}
                    placeholder="0.0.0.0"
                    autoComplete="off"
                    spellCheck={false}
                    aria-label="Local interface IP address"
                    className={`${inputCls} font-mono`}
                  />
                )}
                <span className="block text-[11px] font-normal text-neutral-500">0.0.0.0 listens on every interface.</span>
              </div>
              <label className={label}>
                <span>Listen port</span>
                <input
                  type="text"
                  inputMode="numeric"
                  value={draft.listenPort}
                  onChange={e => setDraft({ ...draft, listenPort: e.target.value })}
                  placeholder="e.g. 8080"
                  autoComplete="off"
                  className={`${inputCls} font-mono`}
                />
              </label>

              <label className={label}>
                <span>Target host</span>
                <input
                  type="text"
                  value={draft.targetHost}
                  onChange={e => setDraft({ ...draft, targetHost: e.target.value })}
                  placeholder="e.g. 192.168.1.20 or nas.local"
                  autoComplete="off"
                  spellCheck={false}
                  className={`${inputCls} font-mono`}
                />
              </label>
              <label className={label}>
                <span>Target port</span>
                <input
                  type="text"
                  inputMode="numeric"
                  value={draft.targetPort}
                  onChange={e => setDraft({ ...draft, targetPort: e.target.value })}
                  placeholder="e.g. 80"
                  autoComplete="off"
                  className={`${inputCls} font-mono`}
                />
              </label>
            </div>

            <label className="flex items-center gap-2 font-medium text-neutral-700 dark:text-neutral-300">
              <input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />
              <span>Enabled (start relaying as soon as it is saved)</span>
            </label>

            {formError && (
              <p role="alert" className="text-[11px] text-red-600 dark:text-red-400 break-words">
                {formError}
              </p>
            )}
            </div>
            <div className="flex justify-end gap-2 px-5 py-3.5 border-t border-neutral-200 dark:border-neutral-800">
              <button type="button" onClick={close} disabled={saving} className={ghostBtn}>
                Cancel
              </button>
              <button
                type="submit"
                disabled={saving}
                className="px-4 py-2 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 disabled:opacity-60 rounded-lg shadow-sm transition-colors"
              >
                {saving ? 'Saving…' : isEdit ? 'Save changes' : 'Add rule'}
              </button>
            </div>
          </form>
        </div>
      )}

      <ConfirmDialog
        open={confirmDelete !== null}
        title="Delete tunnel rule?"
        message={`Delete "${confirmDelete?.name ?? ''}"? The port stops being forwarded and open connections are dropped.`}
        confirmLabel="Delete rule"
        onConfirm={() => confirmDelete && void remove(confirmDelete)}
        onCancel={() => setConfirmDelete(null)}
      />
    </div>
  );
};
