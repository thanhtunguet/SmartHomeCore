import React, { useEffect, useState } from 'react';
import { useApp } from '../../context/AppContext';
import { getOpenAPISettings, getSystemInfo, updateOpenAPISettings } from '../../api/endpoints';
import type { OpenAPISettings, SystemInfo } from '../../api/schema';
import { ServiceKeysSection } from './ServiceKeysSection';
import { AlertChannelsSection } from './AlertChannelsSection';
import { ConfirmDialog } from '../common/ConfirmDialog';
import {
  Shield,
  Server,
  KeyRound,
  ExternalLink,
  Check,
  RotateCcw,
  Sliders,
  Moon,
  Sun,
  LogOut,
  Info
} from 'lucide-react';

const inputCls =
  'w-full px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs font-mono';
const cardCls =
  'bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-4';
const headCls = 'border-b border-neutral-200 dark:border-neutral-800 pb-3';

export const SettingsScreen: React.FC = () => {
  const {
    settings,
    updateSettings,
    theme,
    toggleTheme,
    username,
    logout,
    serverHealth,
    systemMetrics,
    controllerKey,
    controllerKeyError,
    loadControllerKey
  } = useApp();

  const [defaultInterval, setDefaultInterval] = useState(settings.monitoring.defaultIntervalSec);
  const [defaultTimeout, setDefaultTimeout] = useState(settings.monitoring.defaultTimeoutMs);
  const [savedSuccess, setSavedSuccess] = useState(false);
  const [keyCopied, setKeyCopied] = useState(false);
  const [confirmReset, setConfirmReset] = useState(false);
  // Read-only server configuration; absent when the Controller predates GET /system/info.
  const [info, setInfo] = useState<SystemInfo | null>(null);
  const [openAPI, setOpenAPI] = useState<OpenAPISettings | null>(null);
  const [openAPILoading, setOpenAPILoading] = useState(false);
  const [openAPIError, setOpenAPIError] = useState<string | null>(null);
  useEffect(() => {
    getSystemInfo().then(setInfo).catch(() => setInfo(null));
    getOpenAPISettings().then(setOpenAPI).catch(() => setOpenAPIError('Could not load the OpenAPI setting.'));
  }, []);

  useEffect(() => {
    if (!controllerKey) void loadControllerKey();
  }, []);

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault();
    updateSettings({
      monitoring: { defaultIntervalSec: Number(defaultInterval), defaultTimeoutMs: Number(defaultTimeout) }
    });
    setSavedSuccess(true);
    setTimeout(() => setSavedSuccess(false), 3000);
  };

  const handleOpenAPIToggle = async () => {
    if (!openAPI || openAPILoading) return;
    setOpenAPILoading(true);
    setOpenAPIError(null);
    try {
      setOpenAPI(await updateOpenAPISettings(!openAPI.enabled));
    } catch {
      setOpenAPIError('Could not save the OpenAPI setting.');
    } finally {
      setOpenAPILoading(false);
    }
  };

  const handleResetLocal = () => {
    try {
      Object.keys(localStorage)
        .filter(k => k.startsWith('homecore_'))
        .forEach(k => localStorage.removeItem(k));
    } catch {
      /* ignore */
    }
    window.location.reload();
  };

  const copyKey = async () => {
    if (!controllerKey) return;
    try {
      await navigator.clipboard.writeText(controllerKey.public_key);
      setKeyCopied(true);
      setTimeout(() => setKeyCopied(false), 2000);
    } catch {
      /* clipboard unavailable: the key is selectable text */
    }
  };

  const row = (label: string, value: React.ReactNode) => (
    <div className="flex justify-between items-center gap-3 py-1 border-b border-neutral-100 dark:border-neutral-800 last:border-0">
      <span className="text-neutral-500">{label}</span>
      <span className="font-mono text-neutral-800 dark:text-neutral-200 truncate">{value}</span>
    </div>
  );

  return (
    <div className="space-y-6 max-w-5xl mx-auto text-xs">
      <div>
        <h1 className="text-lg font-semibold tracking-tight text-neutral-900 dark:text-neutral-100">Settings & Security</h1>
        <p className="text-xs text-neutral-500 dark:text-neutral-400">
          Your account, the Controller you are connected to, agent trust, service account keys, alerts, and browser preferences.
        </p>
      </div>

      <div className="p-3.5 rounded-xl border border-neutral-200 dark:border-neutral-800 bg-neutral-50/50 dark:bg-neutral-900/40 flex items-start gap-2.5 text-[11px] text-neutral-500 dark:text-neutral-400">
        <Info size={14} className="shrink-0 mt-0.5 text-neutral-400" />
        <span>
          Server settings are read-only here: network listeners, allowed origins, session lifetime and monitoring defaults are
          set in the Controller's YAML configuration file. The effective values are shown below when the Controller reports them.
        </span>
      </div>

      {/* Account */}
      <section className={cardCls}>
        <div className={headCls}>
          <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
            <Shield size={16} className="text-neutral-500" />
            <span>Account</span>
          </h2>
        </div>
        <div>
          {row('Signed in as', username)}
        </div>
        <button
          type="button"
          onClick={() => void logout()}
          className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-lg transition-colors"
        >
          <LogOut size={14} />
          <span>Sign out</span>
        </button>
      </section>

      {/* Controller */}
      <section className={cardCls}>
        <div className={headCls}>
          <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
            <Server size={16} className="text-neutral-500" />
            <span>Controller</span>
          </h2>
        </div>
        <div>
          {row('Version', serverHealth?.version ?? '—')}
          {row('Status', serverHealth?.status ?? 'unreachable')}
          {row('Platform', systemMetrics?.platform ?? '—')}
          {row('Hostname', systemMetrics?.hostname ?? '—')}
          {row('Server clock', serverHealth ? new Date(serverHealth.time).toLocaleString() : '—')}
          {info && row('Mode', info.mode)}
          {info?.listen && row('Listen', `${info.listen}${info.tls ? ' (TLS)' : ''}`)}
          {info?.access_token_ttl_seconds !== undefined && row('Access token', `${Math.round(info.access_token_ttl_seconds / 60)} min`)}
          {info?.refresh_token_ttl_seconds !== undefined && row('Refresh token', `${Math.round(info.refresh_token_ttl_seconds / 86400)} days`)}
          {info?.monitor_defaults?.interval_seconds !== undefined && row('Monitor default', `${info.monitor_defaults.interval_seconds} s · timeout ${info.monitor_defaults.timeout_ms ?? '—'} ms`)}
          {info?.command_deadlines?.wake_seconds !== undefined && row('Command deadlines', `wake ${info.command_deadlines.wake_seconds} s · shutdown ${info.command_deadlines.shutdown_seconds ?? '—'} s`)}
          {info?.retention_days !== undefined && row('History retention', `${info.retention_days} days`)}
        </div>
      </section>

      {/* Agent trust */}
      <section className={cardCls}>
        <div className={headCls}>
          <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
            <KeyRound size={16} className="text-neutral-500" />
            <span>Agent Trust: Controller Signing Key</span>
          </h2>
          <p className="text-[11px] text-neutral-500 dark:text-neutral-400 mt-0.5">
            Shutdown commands are signed with this Ed25519 key. Add the public key to each Agent's <code>trusted_controllers</code>.
          </p>
        </div>
        {controllerKey ? (
          <div className="space-y-1.5">
            {row('Key id', controllerKey.key_id)}
            {row('Algorithm', controllerKey.algorithm)}
            <div className="flex items-start justify-between gap-3 pt-1">
              <span className="font-mono text-[11px] break-all text-neutral-700 dark:text-neutral-300 select-all">{controllerKey.public_key}</span>
              <button type="button" onClick={copyKey} className="text-emerald-600 dark:text-emerald-400 hover:underline text-[11px] font-medium shrink-0">
                {keyCopied ? 'Copied' : 'Copy'}
              </button>
            </div>
          </div>
        ) : (
          <p className="text-[11px] text-neutral-400">{controllerKeyError ?? 'Loading…'}</p>
        )}
      </section>

      {/* Service account keys (AI agents, legacy apps) */}
      <ServiceKeysSection />

      {/* Alert channels (Telegram) */}
      <AlertChannelsSection />

      {/* OpenAPI and Swagger UI */}
      <section className={cardCls}>
        <div className={headCls}>
          <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
            <ExternalLink size={16} className="text-neutral-500" />
            <span>OpenAPI</span>
          </h2>
          <p className="text-[11px] text-neutral-500 dark:text-neutral-400 mt-0.5">
            Control public access to the OpenAPI document and offline Swagger UI. Enabled endpoints are limited to 2 requests per second per client IP.
          </p>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p className="font-medium text-neutral-800 dark:text-neutral-200">
              {openAPI ? (openAPI.enabled ? 'Enabled' : 'Disabled') : 'Loading…'}
            </p>
            {openAPI?.enabled && (
              <div className="flex flex-wrap gap-3 mt-1">
                <a className="text-emerald-700 dark:text-emerald-300 hover:underline" href="/api/docs/" target="_blank" rel="noreferrer">Swagger UI</a>
                <a className="text-neutral-600 dark:text-neutral-400 hover:underline" href="/api/swagger.json" target="_blank" rel="noreferrer">swagger.json</a>
              </div>
            )}
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={openAPI?.enabled ?? false}
            aria-label="Enable OpenAPI and Swagger UI"
            disabled={!openAPI || openAPILoading}
            onClick={() => void handleOpenAPIToggle()}
            className={`relative h-6 w-11 rounded-full transition-colors disabled:opacity-50 ${openAPI?.enabled ? 'bg-emerald-600' : 'bg-neutral-300 dark:bg-neutral-700'}`}
          >
            <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-transform ${openAPI?.enabled ? 'translate-x-5' : 'translate-x-0.5'}`} />
          </button>
        </div>
        {openAPIError && <p role="alert" className="text-[11px] text-red-600 dark:text-red-400">{openAPIError}</p>}
      </section>

      {/* Browser preferences */}
      <form onSubmit={handleSave} className={cardCls}>
        <div className={`${headCls} flex items-center justify-between`}>
          <div>
            <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
              <Sliders size={16} className="text-neutral-500" />
              <span>Browser Preferences</span>
            </h2>
            <p className="text-[11px] text-neutral-500 dark:text-neutral-400 mt-0.5">Stored in this browser only.</p>
          </div>
          {savedSuccess && (
            <span className="text-xs text-emerald-500 flex items-center gap-1 font-medium">
              <Check size={14} />
              <span>Saved</span>
            </span>
          )}
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <label className="block font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
            <span>Default probe interval for new devices (s)</span>
            <input type="number" min={5} max={3600} value={defaultInterval} onChange={e => setDefaultInterval(Number(e.target.value))} className={inputCls} />
          </label>
          <label className="block font-medium text-neutral-700 dark:text-neutral-300 space-y-1">
            <span>Default probe timeout for new devices (ms)</span>
            <input type="number" min={100} max={10000} step={100} value={defaultTimeout} onChange={e => setDefaultTimeout(Number(e.target.value))} className={inputCls} />
          </label>
        </div>

        <div className="flex flex-wrap items-center gap-2 pt-1">
          <button
            type="submit"
            className="px-4 py-2 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg shadow-sm transition-colors"
          >
            Save preferences
          </button>
          <button
            type="button"
            onClick={toggleTheme}
            className="flex items-center gap-1.5 px-3 py-2 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-lg transition-colors"
          >
            {theme === 'dark' ? <Sun size={14} /> : <Moon size={14} />}
            <span>{theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}</span>
          </button>
          <button
            type="button"
            onClick={() => setConfirmReset(true)}
            className="ml-auto flex items-center gap-1.5 px-3 py-2 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors"
          >
            <RotateCcw size={14} />
            <span>Clear browser data</span>
          </button>
        </div>
      </form>
      <ConfirmDialog
        open={confirmReset}
        title="Clear browser data?"
        message="Clear all HomeCore data stored in this browser (preferences) and sign out?"
        confirmLabel="Clear and sign out"
        onConfirm={handleResetLocal}
        onCancel={() => setConfirmReset(false)}
      />
    </div>
  );
};
