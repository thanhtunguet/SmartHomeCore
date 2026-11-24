import React, { useEffect, useState } from 'react';
import { useApp, DDNSDraft } from '../../context/AppContext';
import { CustomSelect, SelectOption } from '../common/CustomSelect';
import { ConfirmDialog } from '../common/ConfirmDialog';
import { DDNSConfig } from '../../types';
import { cloudflareLookup, recordToDdnsFields } from '../../api/mappers';
import { useCloudflareLookup } from './useCloudflareLookup';
import {
  Globe,
  RefreshCw,
  Cloud,
  CheckCircle2,
  AlertTriangle,
  KeyRound,
  Sliders,
  Check,
  Plus,
  Trash2,
  AlertCircle,
  Download,
  Loader2
} from 'lucide-react';

const inputCls =
  'w-full font-mono px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs focus:border-emerald-500 focus:outline-hidden';
const cardCls = 'bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-4';

const INTERVALS = [
  { value: '60', label: 'Every 1 minute' },
  { value: '300', label: 'Every 5 minutes (Default)' },
  { value: '600', label: 'Every 10 minutes' },
  { value: '900', label: 'Every 15 minutes' },
  { value: '1800', label: 'Every 30 minutes' },
  { value: '3600', label: 'Every 1 hour' }
];

const emptyDraft = (): DDNSDraft => ({
  name: '',
  proxied: false,
  ttl: 1,
  interval_seconds: 300,
  enabled: true,
  api_token: '',
  target: null
});

const fromConfig = (c: DDNSConfig): DDNSDraft => ({
  name: c.name,
  proxied: c.proxied,
  ttl: c.ttl,
  interval_seconds: c.intervalSeconds,
  enabled: c.enabled,
  api_token: '',
  target: null // the saved record stays unless the user picks another one
});

export const DDNSScreen: React.FC = () => {
  const { ddnsConfigs, ddnsConfig, setActiveDdnsId, triggerDDNSSync, saveDdns, deleteDdns } = useApp();

  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState<DDNSDraft>(emptyDraft());
  // Editing an existing record: true while the zone/record dropdowns replace the read-only summary.
  const [changing, setChanging] = useState(false);
  // Zone chosen in the dropdown; the record (and so `draft.target`) follows from it.
  const [zoneId, setZoneId] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const lookup = useCloudflareLookup();

  const isNew = creating || !ddnsConfig;
  // Zone and record can only be chosen from Cloudflare's own lists (no manual entry).
  const picking = isNew || changing;

  // (Re)load the form when the selected record changes or "new" is toggled.
  useEffect(() => {
    setDraft(creating || !ddnsConfig ? emptyDraft() : fromConfig(ddnsConfig));
    setChanging(false);
    setZoneId('');
    setError(null);
    lookup.reset();
  }, [creating, ddnsConfig?.id]); // not on every poll: that would wipe edits in progress

  const set = <K extends keyof DDNSDraft>(k: K, v: DDNSDraft[K]) => setDraft(d => ({ ...d, [k]: v }));

  // Token typed in the form, else the stored one of the record being edited.
  const credentials = cloudflareLookup(draft.api_token ?? '', isNew ? null : ddnsConfig);

  const clearChoice = () => {
    setZoneId('');
    setDraft(d => ({ ...d, target: null }));
    lookup.reset();
  };

  const handleToken = (token: string) => {
    set('api_token', token);
    clearChoice(); // lists fetched with another token are stale
  };

  const handlePickZone = (id: string) => {
    setZoneId(id);
    setDraft(d => ({ ...d, target: null }));
    if (credentials) void lookup.loadRecords(id, credentials);
  };

  const handlePickRecord = (recordId: string) => {
    const r = lookup.records.find(x => x.id === recordId);
    if (r) setDraft(d => ({ ...d, ...recordToDdnsFields(zoneId, r) }));
  };

  const startChanging = () => {
    setChanging(true);
    clearChoice();
  };

  const stopChanging = () => {
    setChanging(false);
    clearChoice();
  };

  const zoneOptions: SelectOption[] = lookup.zones.map(z => ({ value: z.id, label: z.name, sublabel: z.id }));
  const recordOptions: SelectOption[] = lookup.records.map(r => ({
    value: r.id,
    label: r.name,
    sublabel: `${r.type} · ${r.content}${r.proxied ? ' · proxied' : ''}`,
    badge: r.type
  }));

  // Save needs a record whenever the form is picking one (new record, or "Change zone/record").
  const canSave = !busy && (!picking || draft.target !== null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy || !canSave) return;
    setBusy(true);
    setError(null);
    const err = await saveDdns(isNew ? null : ddnsConfig!.id, {
      ...draft,
      name: draft.name.trim(),
      // Update: the record is only sent when it was changed (target is null otherwise).
      target: picking ? draft.target : null
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    setCreating(false);
    setChanging(false);
    setZoneId('');
    lookup.reset();
    set('api_token', '');
    setSaved(true);
    setTimeout(() => setSaved(false), 2500);
  };

  const handleDelete = async () => {
    setConfirmDelete(false);
    if (!ddnsConfig) return;
    const err = await deleteDdns(ddnsConfig.id);
    if (err) setError(err);
  };

  const statusLine = (c: DDNSConfig) => {
    switch (c.status) {
      case 'updating':
        return (
          <span className="inline-flex items-center gap-1.5 text-xs font-medium text-amber-500">
            <RefreshCw size={13} className="animate-spin" />
            <span>Updating Cloudflare...</span>
          </span>
        );
      case 'synced':
        return (
          <span className="inline-flex items-center gap-1.5 text-xs font-medium text-emerald-600 dark:text-emerald-400">
            <CheckCircle2 size={15} />
            <span>Record up to date</span>
          </span>
        );
      case 'error':
        return (
          <span className="inline-flex items-center gap-1.5 text-xs font-medium text-red-600 dark:text-red-400">
            <AlertTriangle size={15} />
            <span>Last sync failed</span>
          </span>
        );
      case 'disabled':
        return <span className="text-xs font-medium text-neutral-500">Automatic sync disabled</span>;
      default:
        return <span className="text-xs font-medium text-amber-600 dark:text-amber-400">Waiting for first sync</span>;
    }
  };

  const fmt = (iso?: string) => (iso ? new Date(iso).toLocaleString() : '—');

  return (
    <div className="space-y-6 max-w-5xl mx-auto">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold tracking-tight text-neutral-900 dark:text-neutral-100">Cloudflare DDNS</h1>
          <p className="text-xs text-neutral-500 dark:text-neutral-400">
            The Controller keeps each configured Cloudflare DNS record pointed at this network's public IP.
          </p>
        </div>

        {ddnsConfig && !creating && (
          <button
            onClick={() => triggerDDNSSync(ddnsConfig.id)}
            disabled={ddnsConfig.status === 'updating'}
            className="flex items-center gap-1.5 px-3.5 py-1.5 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg shadow-sm transition-colors disabled:opacity-50 self-start sm:self-auto"
          >
            <RefreshCw size={14} className={ddnsConfig.status === 'updating' ? 'animate-spin' : ''} />
            <span>{ddnsConfig.status === 'updating' ? 'Synchronizing...' : 'Sync DNS Now'}</span>
          </button>
        )}
      </div>

      {/* Record switcher */}
      {(ddnsConfigs.length > 0) && (
        <div className="flex flex-wrap items-center gap-2">
          {ddnsConfigs.map(c => (
            <button
              key={c.id}
              type="button"
              onClick={() => {
                setCreating(false);
                setActiveDdnsId(c.id);
              }}
              className={`px-3 py-1.5 text-xs font-mono rounded-lg border transition-colors ${
                !creating && ddnsConfig?.id === c.id
                  ? 'border-emerald-500 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 font-semibold'
                  : 'border-neutral-200 dark:border-neutral-700 text-neutral-600 dark:text-neutral-400 hover:bg-neutral-50 dark:hover:bg-neutral-800'
              }`}
            >
              {c.recordName}
            </button>
          ))}
          <button
            type="button"
            onClick={() => setCreating(true)}
            className={`flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg border transition-colors ${
              creating ? 'border-emerald-500 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' : 'border-dashed border-neutral-300 dark:border-neutral-700 text-neutral-500 hover:bg-neutral-50 dark:hover:bg-neutral-800'
            }`}
          >
            <Plus size={13} />
            <span>Add record</span>
          </button>
        </div>
      )}

      {/* Status card */}
      {ddnsConfig && !creating && (
        <div className={cardCls}>
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-4 border-b border-neutral-200 dark:border-neutral-800">
            <div className="flex items-center gap-3">
              <div className={`p-2.5 rounded-xl ${ddnsConfig.status === 'synced' ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : ddnsConfig.status === 'error' ? 'bg-red-500/10 text-red-500' : 'bg-amber-500/10 text-amber-500'}`}>
                <Globe size={22} />
              </div>
              <div>
                <div className="flex items-center gap-2">
                  <span className="font-mono text-base font-bold text-neutral-900 dark:text-neutral-100">{ddnsConfig.recordName}</span>
                  {ddnsConfig.proxied ? (
                    <span className="inline-flex items-center gap-1 text-[11px] font-medium text-amber-600 dark:text-amber-400 bg-amber-500/10 px-2 py-0.5 rounded-md">
                      <Cloud size={12} className="text-amber-500" />
                      <span>Proxied</span>
                    </span>
                  ) : (
                    <span className="text-[11px] text-neutral-500 bg-neutral-100 dark:bg-neutral-800 px-2 py-0.5 rounded-md">DNS Only</span>
                  )}
                </div>
                <p className="text-xs text-neutral-500 dark:text-neutral-400 mt-0.5">
                  {ddnsConfig.name} · {ddnsConfig.recordType} record · zone <span className="font-mono">{ddnsConfig.zoneId}</span>
                </p>
              </div>
            </div>
            {statusLine(ddnsConfig)}
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 text-xs">
            <div className="p-3.5 rounded-lg bg-neutral-50 dark:bg-neutral-800/40 border border-neutral-200 dark:border-neutral-800 space-y-1">
              <span className="text-neutral-500 block text-[11px]">Last IP pushed to Cloudflare</span>
              <span className="font-mono text-sm font-semibold text-neutral-900 dark:text-neutral-100 block">{ddnsConfig.lastIp ?? '—'}</span>
              <span className="text-[10px] text-neutral-400 block">Synced: {fmt(ddnsConfig.lastSyncedAt)}</span>
              {ddnsConfig.detectedIp && (
                <span className={`text-[10px] block font-mono ${ddnsConfig.lastIp && ddnsConfig.detectedIp !== ddnsConfig.lastIp ? 'text-amber-600 dark:text-amber-400' : 'text-neutral-400'}`}>
                  WAN now: {ddnsConfig.detectedIp}
                  {ddnsConfig.lastIp && ddnsConfig.detectedIp !== ddnsConfig.lastIp ? ' (changed, sync needed)' : ''}
                  {ddnsConfig.dnsValue && ddnsConfig.dnsValue !== ddnsConfig.detectedIp ? ` · DNS: ${ddnsConfig.dnsValue}` : ''}
                </span>
              )}
            </div>
            <div className="p-3.5 rounded-lg bg-neutral-50 dark:bg-neutral-800/40 border border-neutral-200 dark:border-neutral-800 space-y-1">
              <span className="text-neutral-500 block text-[11px]">Sync cadence</span>
              <span className="text-sm font-semibold text-neutral-800 dark:text-neutral-200 block">
                {ddnsConfig.enabled ? `Every ${Math.round(ddnsConfig.intervalSeconds / 60)} minutes` : 'Disabled'}
              </span>
              <span className="text-[10px] text-neutral-400 block">TTL: {ddnsConfig.ttl === 1 ? 'Auto' : `${ddnsConfig.ttl}s`}</span>
            </div>
            <div className="p-3.5 rounded-lg bg-neutral-50 dark:bg-neutral-800/40 border border-neutral-200 dark:border-neutral-800 space-y-1">
              <span className="text-neutral-500 block text-[11px]">Last error</span>
              <span className={`text-sm font-semibold block break-words ${ddnsConfig.lastError ? 'text-red-600 dark:text-red-400' : 'text-neutral-800 dark:text-neutral-200'}`}>
                {ddnsConfig.lastError ?? 'None'}
              </span>
              <span className="text-[10px] text-neutral-400 block">{ddnsConfig.lastError ? fmt(ddnsConfig.lastErrorAt) : ''}</span>
            </div>
          </div>
        </div>
      )}

      {/* Form */}
      <form onSubmit={handleSubmit} className={cardCls}>
        <div className="flex items-center justify-between pb-3 border-b border-neutral-200 dark:border-neutral-800">
          <div>
            <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
              <Sliders size={16} className="text-neutral-500" />
              <span>{isNew ? 'Add a Cloudflare record' : 'Cloudflare Configuration'}</span>
            </h2>
            <p className="text-xs text-neutral-500 dark:text-neutral-400 mt-0.5">
              Enter the API token, load the zones, then pick the zone and the A/AAAA record to keep up to date. The record must already exist in Cloudflare.
            </p>
          </div>
          {saved && (
            <span className="text-xs text-emerald-500 flex items-center gap-1 font-medium">
              <Check size={14} />
              <span>Saved</span>
            </span>
          )}
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
          <label className="block font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
            <span>Name</span>
            <input className={inputCls} value={draft.name} onChange={e => set('name', e.target.value)} maxLength={100} placeholder="Home server" required />
          </label>
          <div className="space-y-1">
            <div className="flex justify-between items-center">
              <span className="font-medium text-neutral-700 dark:text-neutral-300 flex items-center gap-1.5">
                <KeyRound size={12} />
                <span>API token</span>
              </span>
              {!isNew && ddnsConfig && (
                <span className="text-[11px] text-neutral-400">{ddnsConfig.hasApiToken ? 'A token is stored' : 'No token stored'}</span>
              )}
            </div>
            <input
              type="password"
              className={inputCls}
              value={draft.api_token ?? ''}
              onChange={e => handleToken(e.target.value)}
              placeholder={isNew ? 'Zone:DNS:Edit token' : 'Leave blank to keep the stored token'}
              autoComplete="off"
              required={isNew}
            />
            <p className="text-[11px] text-neutral-400">
              Write-only: the Controller never returns it.
              {!isNew && changing && ddnsConfig?.hasApiToken && !draft.api_token ? ' Lookups reuse the stored token.' : ''}
            </p>
          </div>

          {/* Zone and record: read-only summary of the saved ones, or the Cloudflare dropdowns. */}
          {!picking && ddnsConfig ? (
            <div className="md:col-span-2 p-3 rounded-lg bg-neutral-50 dark:bg-neutral-800/40 border border-neutral-200 dark:border-neutral-800 flex flex-col sm:flex-row sm:items-center justify-between gap-2">
              <div className="space-y-0.5 min-w-0">
                <span className="block text-[11px] text-neutral-500">Zone and record (set at creation)</span>
                <span className="block font-mono text-xs text-neutral-900 dark:text-neutral-100 break-all">
                  {ddnsConfig.recordName} · {ddnsConfig.recordType}
                </span>
                <span className="block font-mono text-[11px] text-neutral-400 break-all">zone {ddnsConfig.zoneId}</span>
              </div>
              <button
                type="button"
                onClick={startChanging}
                className="shrink-0 px-2.5 py-1 text-[11px] font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-lg transition-colors"
              >
                Change zone/record
              </button>
            </div>
          ) : (
            <div className="md:col-span-2 space-y-3">
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium text-neutral-700 dark:text-neutral-300">Cloudflare zone and record</span>
                <div className="flex items-center gap-2">
                  {changing && (
                    <button type="button" onClick={stopChanging} className="text-[11px] font-medium text-neutral-500 hover:underline">
                      Keep the current record
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={() => credentials && void lookup.loadZones(credentials)}
                    disabled={!credentials || lookup.loadingZones}
                    title={credentials ? 'List the zones this token can see' : 'Enter the API token first'}
                    className="shrink-0 flex items-center gap-1.5 px-2.5 py-1 text-[11px] font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-lg transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    {lookup.loadingZones ? <Loader2 size={12} className="animate-spin" /> : <Download size={12} />}
                    <span>{lookup.zones.length > 0 ? 'Reload zones' : 'Load zones'}</span>
                  </button>
                </div>
              </div>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div className="space-y-1">
                  <span className="block font-medium text-neutral-700 dark:text-neutral-300">Zone</span>
                  <CustomSelect
                    value={zoneId}
                    onChange={handlePickZone}
                    options={zoneOptions}
                    placeholder={credentials ? 'Click "Load zones" first' : 'Enter the API token first'}
                    disabled={zoneOptions.length === 0 || lookup.loadingZones}
                  />
                </div>
                <div className="space-y-1">
                  <span className="flex items-center gap-1.5 font-medium text-neutral-700 dark:text-neutral-300">
                    <span>DNS record (A / AAAA)</span>
                    {lookup.loadingRecords && <Loader2 size={12} className="animate-spin text-neutral-400" />}
                  </span>
                  <CustomSelect
                    value={draft.target?.record_id ?? ''}
                    onChange={handlePickRecord}
                    options={recordOptions}
                    placeholder={lookup.loadingRecords ? 'Loading records...' : zoneId ? 'No records loaded' : 'Choose a zone first'}
                    disabled={recordOptions.length === 0 || lookup.loadingRecords}
                  />
                </div>
              </div>
              {draft.target && (
                <p className="text-[11px] text-neutral-500 font-mono">
                  {draft.target.record_name} · {draft.target.record_type}
                </p>
              )}
              {lookup.error && (
                <div role="status" className="flex items-start gap-2 p-2.5 rounded-lg text-[11px] bg-amber-500/10 text-amber-800 dark:text-amber-300 border border-amber-500/20">
                  <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                  <span className="flex-1">Cloudflare lookup failed: {lookup.error}</span>
                  <button type="button" onClick={lookup.retry} className="shrink-0 font-medium underline">
                    Retry
                  </button>
                </div>
              )}
            </div>
          )}
          <div className="space-y-1">
            <span className="block font-medium text-neutral-700 dark:text-neutral-300">Background sync interval</span>
            <CustomSelect
              value={String(draft.interval_seconds)}
              onChange={v => set('interval_seconds', Number(v))}
              options={INTERVALS.some(o => o.value === String(draft.interval_seconds)) ? INTERVALS : [{ value: String(draft.interval_seconds), label: `Every ${draft.interval_seconds}s` }, ...INTERVALS]}
            />
          </div>
          <label className="block font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
            <span>TTL (seconds, 1 = automatic)</span>
            <input type="number" min={1} max={86400} className={inputCls} value={draft.ttl ?? 1} onChange={e => set('ttl', Number(e.target.value))} />
          </label>
          <div className="space-y-1">
            <span className="block font-medium text-neutral-700 dark:text-neutral-300">Cloudflare proxy mode</span>
            <div className="grid grid-cols-2 gap-2">
              <button
                type="button"
                onClick={() => set('proxied', true)}
                className={`p-2.5 rounded-lg border text-xs font-medium flex items-center justify-center gap-1.5 transition-colors ${
                  draft.proxied ? 'border-amber-500 bg-amber-500/10 text-amber-700 dark:text-amber-300 font-semibold' : 'border-neutral-200 dark:border-neutral-700 text-neutral-600 dark:text-neutral-400 hover:bg-neutral-50 dark:hover:bg-neutral-800'
                }`}
              >
                <Cloud size={14} className={draft.proxied ? 'text-amber-500' : 'text-neutral-400'} />
                <span>Proxied (CDN)</span>
              </button>
              <button
                type="button"
                onClick={() => set('proxied', false)}
                className={`p-2.5 rounded-lg border text-xs font-medium flex items-center justify-center gap-1.5 transition-colors ${
                  !draft.proxied ? 'border-emerald-500 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 font-semibold' : 'border-neutral-200 dark:border-neutral-700 text-neutral-600 dark:text-neutral-400 hover:bg-neutral-50 dark:hover:bg-neutral-800'
                }`}
              >
                <Globe size={14} className={!draft.proxied ? 'text-emerald-500' : 'text-neutral-400'} />
                <span>DNS Only</span>
              </button>
            </div>
          </div>
        </div>

        <label className="flex items-center gap-2 cursor-pointer text-xs">
          <input type="checkbox" checked={draft.enabled ?? true} onChange={e => set('enabled', e.target.checked)} className="rounded text-emerald-600 focus:ring-emerald-500" />
          <span className="font-medium text-neutral-800 dark:text-neutral-200">Keep this record updated automatically</span>
        </label>

        {error && (
          <div role="alert" className="flex items-start gap-2 p-2.5 rounded-lg text-xs bg-red-500/10 text-red-700 dark:text-red-300 border border-red-500/20">
            <AlertCircle size={14} className="shrink-0 mt-0.5" />
            <span>{error}</span>
          </div>
        )}

        <div className="pt-3 border-t border-neutral-100 dark:border-neutral-800 flex items-center justify-between gap-2.5">
          {!isNew ? (
            <button type="button" onClick={() => setConfirmDelete(true)} className="flex items-center gap-1.5 px-3 py-2 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors">
              <Trash2 size={14} />
              <span>Remove</span>
            </button>
          ) : (
            <span />
          )}
          <div className="flex gap-2">
            {creating && (
              <button type="button" onClick={() => setCreating(false)} className="px-4 py-2 text-xs font-medium text-neutral-700 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800 rounded-lg transition-colors">
                Cancel
              </button>
            )}
            <button type="submit" disabled={!canSave} title={!canSave && !busy ? 'Pick a zone and a record first' : undefined} className="px-4 py-2 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg shadow-sm transition-colors disabled:opacity-50">
              {busy ? 'Saving…' : isNew ? 'Add record' : 'Save changes'}
            </button>
          </div>
        </div>
      </form>
      <ConfirmDialog
        open={confirmDelete}
        title="Stop DDNS updates?"
        message={`Stop updating ${ddnsConfig?.recordName ?? 'this record'}? The DNS record itself is not deleted.`}
        confirmLabel="Stop updating"
        onConfirm={() => void handleDelete()}
        onCancel={() => setConfirmDelete(false)}
      />
    </div>
  );
};
