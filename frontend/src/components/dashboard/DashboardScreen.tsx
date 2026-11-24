import React from 'react';
import { useApp } from '../../context/AppContext';
import { StatusBadge } from '../common/StatusBadge';
import { DeviceIcon } from '../common/DeviceIcon';
import {
  Server,
  HardDrive,
  Globe,
  Activity,
  Zap,
  Power,
  RefreshCw,
  ArrowRight,
  ShieldCheck,
  CheckCircle2,
  AlertTriangle,
  Clock,
  Cpu,
  Wifi
} from 'lucide-react';

export const DashboardScreen: React.FC = () => {
  const {
    devices,
    onlineCount,
    totalDevices,
    ddnsConfig,
    activities,
    systemMetrics,
    setActiveTab,
    setSelectedDevice,
    probeDevice,
    startWakeFlow,
    requestShutdownConfirmation,
    triggerDDNSSync,
    isPowerLocked
  } = useApp();

  const offlineCount = devices.filter(d => d.status === 'offline').length;
  const unknownCount = devices.filter(d => d.status === 'unknown').length;

  // Format uptime
  const formatUptime = (seconds?: number) => {
    if (seconds === undefined) return '—';
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const mins = Math.floor((seconds % 3600) / 60);
    return `${days}d ${hours}h ${mins}m`;
  };

  const formatBytes = (bytes?: number) => {
    if (bytes === undefined) return '—';
    const gb = bytes / (1024 * 1024 * 1024);
    return `${gb.toFixed(1)} GB`;
  };

  return (
    <div className="space-y-6 max-w-7xl mx-auto">
      {/* 1. System Overview: Is HomeCore Running Normally? */}
      <section className="grid grid-cols-2 lg:grid-cols-4 gap-3 sm:gap-4">
        {/* Core Status & Uptime */}
        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 shadow-xs">
          <div className="flex items-center justify-between text-neutral-500 dark:text-neutral-400 text-xs mb-1">
            <span>HomeCore Server</span>
            <span className="flex items-center gap-1 text-emerald-600 dark:text-emerald-400 font-medium">
              <CheckCircle2 size={13} />
              <span>Healthy</span>
            </span>
          </div>
          <div className="text-xl sm:text-2xl font-semibold tracking-tight text-neutral-900 dark:text-neutral-100 font-mono tabular-nums">
            {formatUptime(systemMetrics?.uptimeSeconds)}
          </div>
          <div className="text-[11px] text-neutral-500 dark:text-neutral-400 mt-1 truncate">
            Uptime · {systemMetrics?.platform ?? 'host unknown'}
          </div>
        </div>

        {/* Local Network IP */}
        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 shadow-xs">
          <div className="flex items-center justify-between text-neutral-500 dark:text-neutral-400 text-xs mb-1">
            <span>Controller Address</span>
            <Wifi size={13} className="text-neutral-400" />
          </div>
          <div className="text-xl sm:text-2xl font-semibold tracking-tight text-neutral-900 dark:text-neutral-100 font-mono">
            {systemMetrics?.localIps[0] ?? '—'}
          </div>
          <div className="text-[11px] text-neutral-500 dark:text-neutral-400 mt-1 truncate">
            {systemMetrics?.hostname ?? 'host'} · {window.location.host}
          </div>
        </div>

        {/* Public IP & DDNS Status */}
        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 shadow-xs">
          <div className="flex items-center justify-between text-neutral-500 dark:text-neutral-400 text-xs mb-1">
            <span>Public IPv4 (WAN)</span>
            <Globe size={13} className="text-emerald-500" />
          </div>
          <div className="text-xl sm:text-2xl font-semibold tracking-tight text-neutral-900 dark:text-neutral-100 font-mono truncate">
            {ddnsConfig?.detectedIp ?? ddnsConfig?.lastIp ?? '—'}
          </div>
          <div className={`text-[11px] mt-1 flex items-center gap-1 truncate ${ddnsConfig?.status === 'error' ? 'text-red-500' : 'text-emerald-600 dark:text-emerald-400'}`}>
            <span>{ddnsConfig ? { synced: 'Cloudflare Synced', error: 'Last sync failed', updating: 'Syncing…', never_synced: 'Not synced yet', disabled: 'Sync disabled' }[ddnsConfig.status] : 'No DDNS record'}</span>
            {ddnsConfig && <span>·</span>}
            {ddnsConfig && <span className="font-mono">{ddnsConfig.recordName}</span>}
          </div>
        </div>

        {/* Host Resource Load */}
        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 shadow-xs">
          <div className="flex items-center justify-between text-neutral-500 dark:text-neutral-400 text-xs mb-1">
            <span>Host Resource Load</span>
            <Cpu size={13} className="text-neutral-400" />
          </div>
          <div className="text-xl sm:text-2xl font-semibold tracking-tight text-neutral-900 dark:text-neutral-100 font-mono tabular-nums">
            {systemMetrics?.cpuPercent !== undefined ? `${systemMetrics.cpuPercent.toFixed(0)}%` : '—'}
          </div>
          <div className="text-[11px] text-neutral-500 dark:text-neutral-400 mt-1 truncate font-mono">
            RAM {formatBytes(systemMetrics?.memoryUsedBytes)} / {formatBytes(systemMetrics?.memoryTotalBytes)}
          </div>
        </div>
      </section>

      {/* 2. Device Summary Strip: Which devices are online? */}
      <section className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 sm:p-5 shadow-xs">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-4 border-b border-neutral-200 dark:border-neutral-800">
          <div>
            <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
              <HardDrive size={16} className="text-neutral-500" />
              <span>Registered Devices</span>
            </h2>
            <p className="text-xs text-neutral-500 dark:text-neutral-400 mt-0.5">
              Automated TCP health checks run by the Controller
            </p>
          </div>

          <div className="flex items-center gap-4 text-xs font-mono">
            <div className="flex items-center gap-1.5">
              <span className="w-2 h-2 rounded-full bg-emerald-500"></span>
              <span className="text-neutral-700 dark:text-neutral-300 font-medium tabular-nums">{onlineCount} Online</span>
            </div>
            <div className="flex items-center gap-1.5">
              <span className="w-2 h-2 rounded-full bg-neutral-400 dark:bg-neutral-600"></span>
              <span className="text-neutral-600 dark:text-neutral-400 tabular-nums">{offlineCount} Offline</span>
            </div>
            {unknownCount > 0 && (
              <div className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-amber-500"></span>
                <span className="text-amber-600 dark:text-amber-400 tabular-nums">{unknownCount} Unknown</span>
              </div>
            )}
            <button
              onClick={() => setActiveTab('devices')}
              className="text-xs text-emerald-600 dark:text-emerald-400 hover:underline flex items-center gap-1 font-sans font-medium"
            >
              <span>Manage all</span>
              <ArrowRight size={12} />
            </button>
          </div>
        </div>

        {/* Device Quick Access Grid */}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3 pt-4">
          {devices.slice(0, 6).map(device => (
            <div
              key={device.id}
              onClick={() => setSelectedDevice(device)}
              className="group p-3.5 rounded-lg border border-neutral-200 dark:border-neutral-800/80 bg-neutral-50/50 dark:bg-neutral-900/40 hover:bg-white dark:hover:bg-neutral-800/60 hover:border-neutral-300 dark:hover:border-neutral-700 transition-all cursor-pointer flex flex-col justify-between space-y-3"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="flex items-start gap-2.5 min-w-0">
                  <div className="p-2 rounded-lg bg-neutral-200/70 dark:bg-neutral-800 text-neutral-700 dark:text-neutral-300 shrink-0">
                    <DeviceIcon type={device.type} size={16} />
                  </div>
                  <div className="min-w-0 truncate">
                    <div className="font-medium text-xs text-neutral-900 dark:text-neutral-100 truncate group-hover:text-emerald-600 dark:group-hover:text-emerald-400 transition-colors">
                      {device.name}
                    </div>
                    <div className="font-mono text-[11px] text-neutral-500 dark:text-neutral-400 truncate">
                      {device.ip} · Port {device.monitoring.port} ({device.monitoring.protocolPreset})
                    </div>
                  </div>
                </div>

                <StatusBadge status={device.status} size="sm" />
              </div>

              {/* Bottom Quick Actions Strip */}
              <div
                className="flex items-center justify-between pt-2 border-t border-neutral-200/60 dark:border-neutral-800/60 text-xs text-neutral-500 dark:text-neutral-400"
                onClick={e => e.stopPropagation()}
              >
                <span className="text-[11px]">Seen: {device.lastSeen}</span>

                <div className="flex items-center gap-1">
                  {/* Wake Action */}
                  {device.wol.enabled && device.status !== 'online' && (
                    <button
                      onClick={() => startWakeFlow(device)}
                      disabled={device.powerTransition !== 'none' || isPowerLocked(device.id)}
                      title="Send Wake-on-LAN Packet"
                      className="disabled:opacity-50 disabled:cursor-not-allowed px-2 py-1 text-[11px] font-medium text-emerald-700 dark:text-emerald-300 bg-emerald-500/10 hover:bg-emerald-500/20 rounded flex items-center gap-1 transition-colors"
                    >
                      <Zap size={11} className="text-emerald-500" />
                      <span>Wake</span>
                    </button>
                  )}

                  {/* Shutdown Action */}
                  {device.shutdown.enabled && device.status === 'online' && (
                    <button
                      onClick={() => requestShutdownConfirmation(device)}
                      disabled={device.powerTransition !== 'none' || isPowerLocked(device.id)}
                      title="Initiate Remote Shutdown"
                      className="disabled:opacity-50 disabled:cursor-not-allowed px-2 py-1 text-[11px] font-medium text-red-700 dark:text-red-300 bg-red-500/10 hover:bg-red-500/20 rounded flex items-center gap-1 transition-colors"
                    >
                      <Power size={11} className="text-red-500" />
                      <span>Shutdown</span>
                    </button>
                  )}

                  {/* Probe Now */}
                  <button
                    onClick={() => probeDevice(device.id)}
                    title="Check TCP Status Now"
                    className="p-1 text-neutral-400 hover:text-neutral-700 dark:hover:text-neutral-200 rounded hover:bg-neutral-200/60 dark:hover:bg-neutral-800 transition-colors"
                  >
                    <RefreshCw size={12} />
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* 3 & 4. Two-Column Split: DDNS Status & Recent Activity Feed */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 sm:gap-6">
        {/* 3. DDNS Status Card */}
        <section className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 sm:p-5 shadow-xs flex flex-col justify-between">
          <div>
            <div className="flex items-center justify-between pb-3 border-b border-neutral-200 dark:border-neutral-800">
              <div className="flex items-center gap-2">
                <Globe size={16} className="text-neutral-500" />
                <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100">
                  Cloudflare DDNS Alias
                </h2>
              </div>
              {ddnsConfig && (
                <button
                  onClick={() => triggerDDNSSync(ddnsConfig.id)}
                  disabled={ddnsConfig.status === 'updating'}
                  className="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-lg transition-colors disabled:opacity-50"
                >
                  <RefreshCw size={12} className={ddnsConfig.status === 'updating' ? 'animate-spin text-emerald-500' : ''} />
                  <span>{ddnsConfig.status === 'updating' ? 'Syncing...' : 'Sync Now'}</span>
                </button>
              )}
            </div>

            {ddnsConfig ? (
              <>
                <div className="py-3 space-y-2.5 text-xs">
                  <div className="flex justify-between items-center">
                    <span className="text-neutral-500 dark:text-neutral-400">Target Record Alias</span>
                    <span className="font-mono font-bold text-neutral-900 dark:text-neutral-100 truncate max-w-[240px]">
                      {ddnsConfig.recordName}
                    </span>
                  </div>
                  <div className="flex justify-between items-center">
                    <span className="text-neutral-500 dark:text-neutral-400">Record Type</span>
                    <span className="font-mono text-neutral-700 dark:text-neutral-300">{ddnsConfig.recordType}</span>
                  </div>
                  <div className="flex justify-between items-center">
                    <span className="text-neutral-500 dark:text-neutral-400">Last Pushed IP</span>
                    <span className="font-mono font-medium text-neutral-800 dark:text-neutral-200">{ddnsConfig.lastIp ?? '—'}</span>
                  </div>
                  <div className="flex justify-between items-center">
                    <span className="text-neutral-500 dark:text-neutral-400">Sync Interval</span>
                    <span className="font-medium text-neutral-800 dark:text-neutral-200">Every {Math.round(ddnsConfig.intervalSeconds / 60)} min</span>
                  </div>
                  <div className="flex justify-between items-center">
                    <span className="text-neutral-500 dark:text-neutral-400">Proxy Routing</span>
                    <span className={`text-[11px] font-medium ${ddnsConfig.proxied ? 'text-amber-600 dark:text-amber-400' : 'text-neutral-500'}`}>
                      {ddnsConfig.proxied ? 'Cloudflare Proxied (CDN)' : 'DNS Only (Direct)'}
                    </span>
                  </div>
                </div>

                <div className="p-2.5 rounded-lg bg-neutral-50 dark:bg-neutral-800/40 border border-neutral-200 dark:border-neutral-800/80 text-xs flex items-center justify-between gap-2">
                  <div className="flex items-center gap-1.5 min-w-0">
                    <span className={`w-2 h-2 rounded-full shrink-0 ${ddnsConfig.status === 'synced' ? 'bg-emerald-500' : ddnsConfig.status === 'error' ? 'bg-red-500' : 'bg-amber-500'}`}></span>
                    <span className="font-medium text-neutral-700 dark:text-neutral-300 truncate">
                      {ddnsConfig.status === 'error' ? ddnsConfig.lastError ?? 'Last sync failed' : ddnsConfig.status === 'synced' ? 'Record up to date' : ddnsConfig.status === 'disabled' ? 'Sync disabled' : 'Waiting for first sync'}
                    </span>
                  </div>
                  <span className="text-[11px] text-neutral-400 shrink-0">
                    {ddnsConfig.lastSyncedAt ? `Synced ${new Date(ddnsConfig.lastSyncedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}` : 'Never synced'}
                  </span>
                </div>
              </>
            ) : (
              <p className="py-4 text-xs text-neutral-500 dark:text-neutral-400">
                No Cloudflare DDNS record is configured yet.
              </p>
            )}
          </div>

          <div className="pt-3 mt-3 border-t border-neutral-200 dark:border-neutral-800 flex justify-end">
            <button
              onClick={() => setActiveTab('ddns')}
              className="text-xs text-emerald-600 dark:text-emerald-400 hover:underline flex items-center gap-1 font-medium"
            >
              <span>Configure DDNS Zone & Alias</span>
              <ArrowRight size={12} />
            </button>
          </div>
        </section>

        {/* 4. Recent Failures & Important Activity Feed */}
        <section className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 sm:p-5 shadow-xs flex flex-col justify-between">
          <div>
            <div className="flex items-center justify-between pb-3 border-b border-neutral-200 dark:border-neutral-800">
              <div className="flex items-center gap-2">
                <Activity size={16} className="text-neutral-500" />
                <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100">
                  Recent Events & Activity
                </h2>
              </div>
              <button
                onClick={() => setActiveTab('activity')}
                className="text-xs text-emerald-600 dark:text-emerald-400 hover:underline flex items-center gap-1 font-medium"
              >
                <span>View all</span>
                <ArrowRight size={12} />
              </button>
            </div>

            <div className="divide-y divide-neutral-100 dark:divide-neutral-800/60">
              {activities.slice(0, 4).map(act => (
                <div key={act.id} className="py-2.5 text-xs space-y-0.5">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-medium text-neutral-900 dark:text-neutral-100 truncate">
                      {act.title}
                    </span>
                    <span className="font-mono text-[11px] text-neutral-400 shrink-0">
                      {new Date(act.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                    </span>
                  </div>
                  <p className="text-neutral-500 dark:text-neutral-400 text-[11px] line-clamp-1">
                    {act.description}
                  </p>
                </div>
              ))}
            </div>
          </div>

          <div className="pt-3 border-t border-neutral-200 dark:border-neutral-800 text-[11px] text-neutral-400 flex items-center justify-between">
            <span>Controller activity log + command outcomes</span>
            <span className="font-mono">{activities.length} events</span>
          </div>
        </section>
      </div>
    </div>
  );
};
