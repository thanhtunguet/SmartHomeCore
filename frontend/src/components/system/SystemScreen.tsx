import React from 'react';
import { useApp } from '../../context/AppContext';
import {
  Cpu,
  Server,
  HardDrive,
  Network,
  RefreshCw,
  Activity,
  AlertCircle,
  ShieldCheck,
  CheckCircle2,
  Terminal,
  Zap,
  Info
} from 'lucide-react';

export const SystemScreen: React.FC = () => {
  const { systemMetrics: m, refreshSystemMetrics, serverHealth, connectionIssue } = useApp();
  const [refreshing, setRefreshing] = React.useState(false);

  const refresh = async () => {
    setRefreshing(true);
    try {
      await refreshSystemMetrics();
    } catch {
      /* surfaced through the connection banner */
    } finally {
      setRefreshing(false);
    }
  };

  const formatBytes = (bytes?: number) => {
    if (bytes === undefined) return '—';
    const mb = bytes / (1024 * 1024);
    if (mb >= 1024) {
      return `${(mb / 1024).toFixed(2)} GB`;
    }
    return `${mb.toFixed(1)} MB`;
  };

  const formatUptime = (seconds?: number) => {
    if (seconds === undefined) return '—';
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const mins = Math.floor((seconds % 3600) / 60);
    return `${days}d ${hours}h ${mins}m`;
  };

  const percent = (used?: number, total?: number) =>
    used !== undefined && total ? Math.round((used / total) * 100) : undefined;

  if (!m) {
    return (
      <div className="max-w-7xl mx-auto text-xs text-neutral-500 dark:text-neutral-400">
        {connectionIssue ? 'System metrics are unavailable right now.' : 'Loading system metrics…'}
      </div>
    );
  }

  const memPercent = percent(m.memoryUsedBytes, m.memoryTotalBytes);
  const diskPercent = percent(m.diskUsedBytes, m.diskTotalBytes);
  const cpu = m.cpuPercent;

  const gauge = (value: number | undefined) => (
    <div className="w-full h-2 bg-neutral-100 dark:bg-neutral-800 rounded-full overflow-hidden">
      <div className="h-full bg-emerald-500 transition-all duration-500" style={{ width: `${Math.min(100, value ?? 0)}%` }}></div>
    </div>
  );

  return (
    <div className="space-y-6 max-w-7xl mx-auto">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold tracking-tight text-neutral-900 dark:text-neutral-100">
            Host Hardware & Process Monitoring
          </h1>
          <p className="text-xs text-neutral-500 dark:text-neutral-400">
            Telemetry of the machine running the Controller, collected {new Date(m.collectedAt).toLocaleTimeString()} and refreshed every 10 s.
          </p>
        </div>

        <button
          onClick={refresh}
          disabled={refreshing}
          className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-white dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 hover:bg-neutral-50 dark:hover:bg-neutral-700/60 rounded-lg shadow-xs transition-colors self-start sm:self-auto disabled:opacity-50"
        >
          <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
          <span>Refresh Metrics</span>
        </button>
      </div>

      {/* Android Hardware Host Notice (if running on Android TV Box or repurposed Phone) */}
      {m.isAndroidHost && (
        <div className="p-3.5 bg-neutral-900 text-neutral-300 rounded-xl border border-neutral-800 flex items-start gap-3 text-xs">
          <Info size={16} className="text-emerald-400 shrink-0 mt-0.5" />
          <div className="space-y-1">
            <span className="font-semibold text-neutral-100 block">
              Low-Power Embedded Host (Android)
            </span>
            <p className="text-neutral-400 leading-relaxed text-[11px]">
              The Controller runs inside an Android app. Some metrics are restricted by the platform and may be missing
              {m.restricted.length > 0 ? ` (${m.restricted.join(', ')})` : ''}.
            </p>
          </div>
        </div>
      )}

      {/* Host Specifications Grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 shadow-xs space-y-1">
          <div className="text-xs text-neutral-500">Hostname / Node</div>
          <div className="font-semibold text-sm font-mono text-neutral-900 dark:text-neutral-100">
            {m.hostname ?? '—'}
          </div>
          <div className="text-[11px] text-neutral-400 truncate">
            {m.os}{m.cpuCores ? ` · ${m.cpuCores} cores` : ''}
          </div>
        </div>

        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 shadow-xs space-y-1">
          <div className="text-xs text-neutral-500">Platform</div>
          <div className="font-semibold text-sm font-mono text-neutral-900 dark:text-neutral-100">
            {m.platform}
          </div>
          <div className="text-[11px] text-neutral-400 truncate">
            Controller v{serverHealth?.version ?? '?'}{m.kernel ? ` · kernel ${m.kernel}` : ''}
          </div>
        </div>

        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 shadow-xs space-y-1">
          <div className="text-xs text-neutral-500">Host Uptime</div>
          <div className="font-semibold text-sm font-mono text-neutral-900 dark:text-neutral-100 tabular-nums">
            {formatUptime(m.uptimeSeconds)}
          </div>
          <div className="text-[11px] text-neutral-400 flex items-center gap-1">
            <CheckCircle2 size={12} className="text-emerald-500" />
            <span>{m.loadAverage ? `Load ${m.loadAverage.map(v => v.toFixed(2)).join(' ')}` : 'Reported by the Controller'}</span>
          </div>
        </div>

        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 shadow-xs space-y-1">
          <div className="text-xs text-neutral-500">Go Process Memory (RSS)</div>
          <div className="font-semibold text-sm font-mono text-emerald-600 dark:text-emerald-400 tabular-nums">
            {formatBytes(m.process.rssBytes)}
          </div>
          <div className="text-[11px] text-neutral-400">
            {m.process.goroutines ?? '—'} active goroutines
            {m.process.pid !== undefined ? ` · pid ${m.process.pid}` : ''}
          </div>
        </div>
      </div>

      {/* Resource Utilization Gauges (CPU, Memory, Disk) */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-3">
          <div className="flex justify-between items-center text-xs">
            <span className="font-medium text-neutral-700 dark:text-neutral-300 flex items-center gap-1.5">
              <Cpu size={15} className="text-neutral-400" />
              <span>CPU Utilization</span>
            </span>
            <span className="font-mono text-sm font-semibold text-neutral-900 dark:text-neutral-100 tabular-nums">
              {cpu !== undefined ? `${cpu.toFixed(1)}%` : '—'}
            </span>
          </div>
          {gauge(cpu)}
        </div>

        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-3">
          <div className="flex justify-between items-center text-xs">
            <span className="font-medium text-neutral-700 dark:text-neutral-300 flex items-center gap-1.5">
              <Activity size={15} className="text-neutral-400" />
              <span>Host Memory (RAM)</span>
            </span>
            <span className="font-mono text-sm font-semibold text-neutral-900 dark:text-neutral-100 tabular-nums">
              {memPercent !== undefined ? `${memPercent}%` : '—'}
            </span>
          </div>
          {gauge(memPercent)}
          <div className="flex justify-between text-[11px] text-neutral-400 font-mono">
            <span>Used: {formatBytes(m.memoryUsedBytes)}</span>
            <span>Total: {formatBytes(m.memoryTotalBytes)}</span>
          </div>
        </div>

        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-3">
          <div className="flex justify-between items-center text-xs">
            <span className="font-medium text-neutral-700 dark:text-neutral-300 flex items-center gap-1.5">
              <HardDrive size={15} className="text-neutral-400" />
              <span title={m.diskPath || undefined} tabIndex={m.diskPath ? 0 : undefined}>Disk</span>
            </span>
            <span className="font-mono text-sm font-semibold text-neutral-900 dark:text-neutral-100 tabular-nums">
              {diskPercent !== undefined ? `${diskPercent}%` : '—'}
            </span>
          </div>
          {gauge(diskPercent)}
          <div className="flex justify-between text-[11px] text-neutral-400 font-mono">
            <span>Used: {formatBytes(m.diskUsedBytes)}</span>
            <span>Total: {formatBytes(m.diskTotalBytes)}</span>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {/* Process Telemetry */}
        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-3">
          <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
            <Server size={16} className="text-neutral-500" />
            <span>HomeCore Process Internals</span>
          </h2>

          <div className="space-y-2 text-xs">
            <div className="flex justify-between items-center py-1 border-b border-neutral-100 dark:border-neutral-800">
              <span className="text-neutral-500">Controller Version</span>
              <span className="font-mono text-neutral-800 dark:text-neutral-200">{serverHealth?.version ?? '—'}</span>
            </div>
            <div className="flex justify-between items-center py-1 border-b border-neutral-100 dark:border-neutral-800">
              <span className="text-neutral-500">Heap Allocation</span>
              <span className="font-mono text-neutral-800 dark:text-neutral-200">{formatBytes(m.process.heapAllocBytes)}</span>
            </div>
            <div className="flex justify-between items-center py-1 border-b border-neutral-100 dark:border-neutral-800">
              <span className="text-neutral-500">Concurrent Goroutines</span>
              <span className="font-mono text-neutral-800 dark:text-neutral-200">{m.process.goroutines ?? '—'}</span>
            </div>
            <div className="flex justify-between items-center py-1">
              <span className="text-neutral-500">Server Clock</span>
              <span className="font-mono text-neutral-800 dark:text-neutral-200">
                {serverHealth ? new Date(serverHealth.time).toLocaleTimeString() : '—'}
              </span>
            </div>
          </div>
        </div>

        {/* Local addresses */}
        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-5 shadow-xs space-y-3">
          <h2 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 flex items-center gap-2">
            <Network size={16} className="text-neutral-500" />
            <span>Local Addresses</span>
          </h2>

          {m.localAddresses.length === 0 ? (
            <p className="text-xs text-neutral-400">No addresses reported.</p>
          ) : (
            <div className="space-y-2">
              {m.localAddresses.map((address, index) => (
                <div
                  key={`${address.interfaceName ?? 'unknown'}-${address.ipAddress}-${index}`}
                  className="flex items-start justify-between gap-3 p-3 bg-neutral-50 dark:bg-neutral-800/40 border border-neutral-200 dark:border-neutral-800 rounded-lg text-xs font-mono text-neutral-800 dark:text-neutral-200"
                >
                  <span className="min-w-0 break-all text-neutral-500">{address.interfaceName ?? '—'}</span>
                  <span className="min-w-0 break-all text-right">{address.ipAddress}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
