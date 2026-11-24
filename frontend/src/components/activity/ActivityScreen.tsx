import React, { useState } from 'react';
import { useApp } from '../../context/AppContext';
import { ActivityCategory, ActivityOutcome, ActivityEvent } from '../../types';
import { filterActivity } from '../../api/activity';
import { CustomSelect } from '../common/CustomSelect';
import {
  Activity,
  Search,
  Filter,
  CheckCircle2,
  AlertTriangle,
  Info,
  XCircle,
  Zap,
  Power,
  Globe,
  Server,
  ShieldCheck,
  UserRound,
  ChevronDown,
  ChevronRight,
  Terminal,
  Copy,
  Check,
  Trash2,
  Download
} from 'lucide-react';

export const ActivityScreen: React.FC = () => {
  const { activities, activityTruncated, clearActivities, devices } = useApp();
  const localCount = activities.filter(a => a.source === 'local').length;

  const [categoryFilter, setCategoryFilter] = useState<string>('all');
  const [outcomeFilter, setOutcomeFilter] = useState<string>('all');
  const [deviceFilter, setDeviceFilter] = useState<string>('all');
  const [searchQuery, setSearchQuery] = useState('');
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);

  const filteredEvents = filterActivity(activities, {
    category: categoryFilter,
    outcome: outcomeFilter,
    deviceId: deviceFilter,
    query: searchQuery
  });

  const getCategoryIcon = (category: ActivityCategory) => {
    switch (category) {
      case 'wol':
        return <Zap size={14} className="text-amber-500" />;
      case 'shutdown':
        return <Power size={14} className="text-red-500" />;
      case 'ddns':
        return <Globe size={14} className="text-blue-500" />;
      case 'device':
        return <Server size={14} className="text-emerald-500" />;
      case 'auth':
        return <ShieldCheck size={14} className="text-purple-500" />;
      case 'user':
        return <UserRound size={14} className="text-indigo-500" />;
      case 'system':
      default:
        return <Activity size={14} className="text-neutral-400" />;
    }
  };

  const getOutcomeBadge = (outcome: ActivityOutcome) => {
    switch (outcome) {
      case 'success':
        return (
          <span className="inline-flex items-center gap-1 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
            <CheckCircle2 size={12} />
            <span>Success</span>
          </span>
        );
      case 'warning':
        return (
          <span className="inline-flex items-center gap-1 text-[11px] font-medium text-amber-600 dark:text-amber-400">
            <AlertTriangle size={12} />
            <span>Warning</span>
          </span>
        );
      case 'error':
        return (
          <span className="inline-flex items-center gap-1 text-[11px] font-medium text-red-600 dark:text-red-400">
            <XCircle size={12} />
            <span>Failed</span>
          </span>
        );
      case 'info':
      default:
        return (
          <span className="inline-flex items-center gap-1 text-[11px] font-medium text-neutral-500 dark:text-neutral-400">
            <Info size={12} />
            <span>Info</span>
          </span>
        );
    }
  };

  const handleCopyCurl = (event: ActivityEvent) => {
    const curl = `curl -X ${event.details?.method || 'POST'} "${window.location.origin}${event.details?.endpoint || '/api/v1'}" \\
  -H "Authorization: Bearer <access_token>" \\
  -H "Content-Type: application/json" ${
    event.details?.requestPayload ? `\\\n  -d '${JSON.stringify(event.details.requestPayload)}'` : ''
  }`;
    navigator.clipboard.writeText(curl);
    setCopiedId(event.id);
    setTimeout(() => setCopiedId(null), 2000);
  };

  const handleExportJson = () => {
    const dataStr = 'data:text/json;charset=utf-8,' + encodeURIComponent(JSON.stringify(activities, null, 2));
    const downloadAnchor = document.createElement('a');
    downloadAnchor.setAttribute('href', dataStr);
    downloadAnchor.setAttribute('download', `homecore_activity_log_${Date.now()}.json`);
    document.body.appendChild(downloadAnchor);
    downloadAnchor.click();
    downloadAnchor.remove();
  };

  return (
    <div className="space-y-6 max-w-7xl mx-auto">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0 flex-[1_1_30rem]">
          <h1 className="text-lg font-semibold tracking-tight text-neutral-900 dark:text-neutral-100">
            Activity & Command Timeline
          </h1>
          <p className="text-xs text-neutral-500 dark:text-neutral-400">
            Who did what, from the Controller's persistent activity log (sign-ins, device and DDNS changes, wake and shutdown
            requests). Wake and shutdown lines show the command's outcome. Probe results are browser-local and disappear on reload.
          </p>
          {activityTruncated && (
            <p className="text-[11px] text-amber-600 dark:text-amber-400 mt-1">
              Showing the most recent entries only; older ones exist on the Controller.
            </p>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <button
            onClick={handleExportJson}
            className="flex items-center gap-1.5 whitespace-nowrap px-3 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-white dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 hover:bg-neutral-50 dark:hover:bg-neutral-700 rounded-lg shadow-xs transition-colors"
          >
            <Download size={14} />
            <span>Export JSON</span>
          </button>

          <button
            onClick={clearActivities}
            disabled={localCount === 0}
            title="Removes the browser-local events (probe results) only. The Controller's activity log cannot be cleared."
            className="disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-1.5 whitespace-nowrap px-3 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 hover:text-red-600 bg-white dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 hover:bg-neutral-50 dark:hover:bg-neutral-700 rounded-lg shadow-xs transition-colors"
          >
            <Trash2 size={14} />
            <span>Clear local events{localCount > 0 ? ` (${localCount})` : ''}</span>
          </button>
        </div>
      </div>

      {/* Filter Bar */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
        {/* Search Input */}
        <div className="relative">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-neutral-400" />
          <input
            type="text"
            placeholder="Search event logs..."
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            className="w-full pl-9 pr-3 py-1.5 bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-lg text-xs text-neutral-900 dark:text-neutral-100 placeholder-neutral-400 focus:outline-hidden"
          />
        </div>

        {/* Category Filter */}
        <CustomSelect
          value={categoryFilter}
          onChange={setCategoryFilter}
          options={[
            { value: 'all', label: 'All Categories' },
            { value: 'device', label: 'Devices' },
            { value: 'wol', label: 'Wake-on-LAN' },
            { value: 'shutdown', label: 'Shutdown Signals' },
            { value: 'ddns', label: 'Cloudflare DDNS' },
            { value: 'user', label: 'Accounts' },
            { value: 'system', label: 'System' },
            { value: 'auth', label: 'Authentication' }
          ]}
        />

        {/* Outcome Filter */}
        <CustomSelect
          value={outcomeFilter}
          onChange={setOutcomeFilter}
          options={[
            { value: 'all', label: 'All Outcomes' },
            { value: 'success', label: 'Success' },
            { value: 'warning', label: 'Warning / Timeout' },
            { value: 'error', label: 'Failed' },
            { value: 'info', label: 'Informational' }
          ]}
        />

        {/* Device Filter */}
        <CustomSelect
          value={deviceFilter}
          onChange={setDeviceFilter}
          options={[
            { value: 'all', label: 'All Devices' },
            ...devices.map(d => ({
              value: d.id,
              label: d.name,
              sublabel: d.ip
            }))
          ]}
        />
      </div>

      {/* Event Timeline List */}
      <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl overflow-hidden shadow-xs divide-y divide-neutral-100 dark:divide-neutral-800">
        {filteredEvents.map(event => {
          const isExpanded = expandedId === event.id;

          return (
            <div key={event.id} className="transition-colors hover:bg-neutral-50/50 dark:hover:bg-neutral-800/20">
              {/* Event Summary Row */}
              <div
                onClick={() => setExpandedId(isExpanded ? null : event.id)}
                className="p-4 flex items-start justify-between gap-3 cursor-pointer select-none"
              >
                <div className="flex items-start gap-3 min-w-0">
                  <div className="p-2 rounded-lg bg-neutral-100 dark:bg-neutral-800 text-neutral-700 dark:text-neutral-300 shrink-0 mt-0.5">
                    {getCategoryIcon(event.category)}
                  </div>

                  <div className="min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-semibold text-xs text-neutral-900 dark:text-neutral-100">
                        {event.title}
                      </span>
                      {getOutcomeBadge(event.outcome)}
                      {event.deviceName && (
                        <span className="text-[11px] text-neutral-500 font-mono">
                          · {event.deviceName}
                        </span>
                      )}
                      {event.actor && (
                        <span className="text-[11px] text-neutral-400">by {event.actor}</span>
                      )}
                      {event.source === 'local' && (
                        <span className="text-[10px] px-1.5 rounded bg-neutral-100 dark:bg-neutral-800 text-neutral-500">this browser</span>
                      )}
                    </div>

                    <p className="text-xs text-neutral-500 dark:text-neutral-400 mt-0.5 leading-relaxed">
                      {event.description}
                    </p>
                  </div>
                </div>

                <div className="flex items-center gap-3 shrink-0">
                  <span className="font-mono text-[11px] text-neutral-400 tabular-nums">
                    {new Date(event.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
                  </span>
                  <div className="p-1 text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-200">
                    {isExpanded ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
                  </div>
                </div>
              </div>

              {/* Technical Diagnostics Drawer */}
              {isExpanded && (
                <div className="px-5 pb-4 pt-1 bg-neutral-50 dark:bg-neutral-950/40 border-t border-neutral-100 dark:border-neutral-800/80 space-y-3 text-xs animate-in fade-in duration-100">
                  <div className="flex items-center justify-between text-[11px] text-neutral-400 pb-1">
                    <div className="flex items-center gap-2">
                      <Terminal size={13} className="text-neutral-400" />
                      <span className="font-semibold text-neutral-500 uppercase tracking-wider">
                        {event.details?.endpoint ? 'REST Trace & Payload' : 'Details'}
                      </span>
                    </div>

                    {event.details?.endpoint && <button
                      onClick={() => handleCopyCurl(event)}
                      className="flex items-center gap-1 text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200 transition-colors"
                    >
                      {copiedId === event.id ? (
                        <>
                          <Check size={12} className="text-emerald-500" />
                          <span className="text-emerald-500">Copied cURL</span>
                        </>
                      ) : (
                        <>
                          <Copy size={12} />
                          <span>Copy as cURL</span>
                        </>
                      )}
                    </button>}
                  </div>

                  <div className="bg-neutral-950 border border-neutral-800 rounded-lg p-3 font-mono text-[11px] text-neutral-300 space-y-2 overflow-x-auto">
                    {(event.details?.endpoint || event.details?.responseCode) && <div className="flex items-center gap-2 text-neutral-400">
                      <span className="text-emerald-400 font-semibold">{event.details?.method || 'POST'}</span>
                      <span>{event.details?.endpoint}</span>
                      {event.details?.responseCode && (
                        <span className="text-neutral-500">HTTP {event.details.responseCode}</span>
                      )}
                      {event.details?.latencyMs && (
                        <span className="text-neutral-500">({event.details.latencyMs} ms)</span>
                      )}
                    </div>}

                    {event.details?.requestPayload && (
                      <div className="pt-1 text-neutral-400">
                        <span className="text-neutral-500 block mb-0.5">// Request Payload</span>
                        <pre className="text-neutral-300">{JSON.stringify(event.details.requestPayload, null, 2)}</pre>
                      </div>
                    )}

                    {event.details?.rawOutput && (
                      <div className="pt-1 text-neutral-400">
                        <span className="text-neutral-500 block mb-0.5">// Diagnostic Log</span>
                        <div className="text-emerald-300/90 whitespace-pre-wrap">{event.details.rawOutput}</div>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          );
        })}

        {filteredEvents.length === 0 && (
          <div className="p-8 text-center text-xs text-neutral-400">
            No events match the selected criteria.
          </div>
        )}
      </div>
    </div>
  );
};
