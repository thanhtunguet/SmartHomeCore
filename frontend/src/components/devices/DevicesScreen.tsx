import React, { useState } from 'react';
import { useApp } from '../../context/AppContext';
import { Device, DeviceStatus } from '../../types';
import { PowerStateBadge } from '../common/PowerStateBadge';
import { DeviceIcon } from '../common/DeviceIcon';
import {
  Search,
  Plus,
  LayoutGrid,
  List,
  Zap,
  Power,
  RefreshCw,
  MoreVertical,
  ExternalLink,
  Edit2,
  Trash2,
  Info,
  Clock,
  Network,
  HardDrive
} from 'lucide-react';

export const DevicesScreen: React.FC = () => {
  const {
    devices,
    setSelectedDevice,
    setEditingDevice,
    setDeviceModalOpen,
    probeDevice,
    startWakeFlow,
    requestShutdownConfirmation,
    deleteDevice,
    isPowerLocked
  } = useApp();

  const [viewMode, setViewMode] = useState<'card' | 'table'>('card');
  const [searchQuery, setSearchQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<'all' | 'online' | 'offline' | 'unknown'>('all');
  const [probingId, setProbingId] = useState<string | null>(null);

  const filteredDevices = devices.filter(device => {
    const matchesSearch =
      device.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
      device.ip.toLowerCase().includes(searchQuery.toLowerCase()) ||
      device.mac.toLowerCase().includes(searchQuery.toLowerCase()) ||
      device.description.toLowerCase().includes(searchQuery.toLowerCase());

    const matchesStatus = statusFilter === 'all' || device.status === statusFilter;

    return matchesSearch && matchesStatus;
  });

  const handleProbe = async (id: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setProbingId(id);
    await probeDevice(id);
    setProbingId(null);
  };

  return (
    <div className="space-y-6 max-w-7xl mx-auto">
      {/* Header Toolbar */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold tracking-tight text-neutral-900 dark:text-neutral-100">
            Device Inventory
          </h1>
          <p className="text-xs text-neutral-500 dark:text-neutral-400">
            Registered computers, servers, and network equipment monitored via TCP sockets.
          </p>
        </div>

        <div className="flex items-center gap-2">
          {/* Card / Table View Switcher */}
          <div className="flex items-center p-0.5 rounded-lg border border-neutral-200 dark:border-neutral-800 bg-neutral-100 dark:bg-neutral-900">
            <button
              onClick={() => setViewMode('card')}
              className={`p-1.5 rounded-md text-xs transition-colors ${
                viewMode === 'card'
                  ? 'bg-white dark:bg-neutral-800 text-neutral-900 dark:text-neutral-100 shadow-xs'
                  : 'text-neutral-500 hover:text-neutral-700 dark:hover:text-neutral-300'
              }`}
              title="Card View"
            >
              <LayoutGrid size={15} />
            </button>
            <button
              onClick={() => setViewMode('table')}
              className={`p-1.5 rounded-md text-xs transition-colors ${
                viewMode === 'table'
                  ? 'bg-white dark:bg-neutral-800 text-neutral-900 dark:text-neutral-100 shadow-xs'
                  : 'text-neutral-500 hover:text-neutral-700 dark:hover:text-neutral-300'
              }`}
              title="Table View"
            >
              <List size={15} />
            </button>
          </div>

          {/* Add Device Button */}
          <button
            onClick={() => {
              setEditingDevice(null);
              setDeviceModalOpen(true);
            }}
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-emerald-600 hover:bg-emerald-500 rounded-lg shadow-sm transition-colors whitespace-nowrap"
          >
            <Plus size={14} />
            <span>Add Device</span>
          </button>
        </div>
      </div>

      {/* Filter and Search Bar */}
      <div className="flex flex-col sm:flex-row gap-3 items-stretch sm:items-center justify-between">
        {/* Search */}
        <div className="relative flex-1 max-w-md">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-neutral-400" />
          <input
            type="text"
            placeholder="Search by name, IP, MAC address..."
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            className="w-full pl-9 pr-3 py-1.5 bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-lg text-xs text-neutral-900 dark:text-neutral-100 placeholder-neutral-400 focus:border-emerald-500 focus:outline-hidden"
          />
        </div>

        {/* Status Segmented Filter */}
        <div className="flex items-center gap-1 p-1 bg-neutral-100 dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-lg text-xs">
          {(['all', 'online', 'offline', 'unknown'] as const).map(status => {
            const count =
              status === 'all'
                ? devices.length
                : devices.filter(d => d.status === status).length;

            return (
              <button
                key={status}
                onClick={() => setStatusFilter(status)}
                className={`px-3 py-1 font-medium rounded-md transition-colors capitalize ${
                  statusFilter === status
                    ? 'bg-white dark:bg-neutral-800 text-neutral-900 dark:text-neutral-100 shadow-xs'
                    : 'text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200'
                }`}
              >
                {status} <span className="font-mono text-[11px] tabular-nums text-neutral-400">({count})</span>
              </button>
            );
          })}
        </div>
      </div>

      {/* View Mode: Card View */}
      {viewMode === 'card' && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {filteredDevices.map(device => {
            const isProbing = probingId === device.id;

            return (
              <div
                key={device.id}
                onClick={() => setSelectedDevice(device)}
                className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-4 sm:p-5 shadow-xs hover:border-neutral-300 dark:hover:border-neutral-700 transition-all cursor-pointer flex flex-col justify-between space-y-4"
              >
                <div>
                  {/* Top line: Icon, Name, and Status */}
                  <div className="flex items-start justify-between gap-3">
                    <div className="flex items-start gap-3 min-w-0">
                      <div className="p-2.5 rounded-lg bg-neutral-100 dark:bg-neutral-800 text-neutral-700 dark:text-neutral-300 shrink-0">
                        <DeviceIcon type={device.type} size={18} />
                      </div>
                      <div className="min-w-0 truncate">
                        <h3 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100 truncate hover:text-emerald-600 dark:hover:text-emerald-400 transition-colors">
                          {device.name}
                        </h3>
                        <p className="text-xs text-neutral-500 dark:text-neutral-400 truncate mt-0.5">
                          {device.description || `${device.type} · ${device.os}`}
                        </p>
                      </div>
                    </div>

                    <PowerStateBadge status={device.status} transition={device.powerTransition} />
                  </div>

                  {/* Network details strip */}
                  <div className="mt-4 pt-3 border-t border-neutral-100 dark:border-neutral-800/80 space-y-1.5 text-xs">
                    <div className="flex items-center justify-between">
                      <span className="text-neutral-500 dark:text-neutral-400">IP Address</span>
                      <span className="font-mono font-medium text-neutral-800 dark:text-neutral-200">{device.ip}</span>
                    </div>
                    <div className="flex items-center justify-between">
                      <span className="text-neutral-500 dark:text-neutral-400">Monitoring Port</span>
                      <span className="font-mono text-neutral-700 dark:text-neutral-300">
                        {device.monitoring.port} ({device.monitoring.protocolPreset})
                      </span>
                    </div>
                    <div className="flex items-center justify-between">
                      <span className="text-neutral-500 dark:text-neutral-400">MAC Address</span>
                      <span className="font-mono text-[11px] text-neutral-500">{device.mac}</span>
                    </div>
                  </div>
                </div>

                {/* Bottom Action Footer */}
                <div
                  className="pt-3 border-t border-neutral-100 dark:border-neutral-800 flex items-center justify-between text-xs"
                  onClick={e => e.stopPropagation()}
                >
                  <span className="text-[11px] text-neutral-400">
                    Seen: {device.lastSeen}
                  </span>

                  <div className="flex items-center gap-1.5">
                    {/* Wake Action */}
                    {device.wol.enabled ? (
                      <button
                        onClick={() => startWakeFlow(device)}
                        disabled={device.powerTransition !== 'none' || isPowerLocked(device.id)}
                        title="Dispatch Wake-on-LAN Magic Packet"
                        className={`disabled:opacity-50 disabled:cursor-not-allowed px-2.5 py-1 text-xs font-medium rounded-lg flex items-center gap-1 transition-colors ${
                          device.status !== 'online'
                            ? 'text-emerald-700 dark:text-emerald-300 bg-emerald-500/10 hover:bg-emerald-500/20'
                            : 'text-neutral-500 hover:text-neutral-800 dark:hover:text-neutral-200 hover:bg-neutral-100 dark:hover:bg-neutral-800'
                        }`}
                      >
                        <Zap size={13} className={device.status !== 'online' ? 'text-emerald-500' : 'text-neutral-400'} />
                        <span>Wake</span>
                      </button>
                    ) : (
                      <span
                        title="WOL not configured for this device"
                        className="text-[11px] text-neutral-400 px-1.5 py-0.5 cursor-not-allowed"
                      >
                        WOL off
                      </span>
                    )}

                    {/* Remote Shutdown Action: disabled (not hidden) when it cannot apply */}
                    {(() => {
                      const canPowerOff = device.shutdown.enabled && device.status === 'online';
                      const hint = !device.shutdown.enabled
                        ? 'HomeCore Shutdown Agent not enabled'
                        : device.status !== 'online'
                        ? 'Device is offline'
                        : 'Send Remote Shutdown Command via Agent';
                      return (
                        <button
                          onClick={() => requestShutdownConfirmation(device)}
                          disabled={!canPowerOff || device.powerTransition !== 'none' || isPowerLocked(device.id)}
                          title={hint}
                          className={`disabled:opacity-50 disabled:cursor-not-allowed px-2.5 py-1 text-xs font-medium rounded-lg flex items-center gap-1 transition-colors ${
                            canPowerOff
                              ? 'text-red-700 dark:text-red-300 bg-red-500/10 hover:bg-red-500/20'
                              : 'text-neutral-500 bg-neutral-100 dark:bg-neutral-800'
                          }`}
                        >
                          <Power size={13} className={canPowerOff ? 'text-red-500' : 'text-neutral-400'} />
                          <span>Power off</span>
                        </button>
                      );
                    })()}

                    {/* Single Check Probe */}
                    <button
                      onClick={e => handleProbe(device.id, e)}
                      disabled={isProbing}
                      title="Probe TCP port now"
                      className="p-1.5 text-neutral-400 hover:text-neutral-700 dark:hover:text-neutral-200 rounded-md hover:bg-neutral-100 dark:hover:bg-neutral-800 transition-colors"
                    >
                      <RefreshCw size={13} className={isProbing ? 'animate-spin text-emerald-500' : ''} />
                    </button>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* View Mode: Table View */}
      {viewMode === 'table' && (
        <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl overflow-hidden shadow-xs">
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead className="bg-neutral-50 dark:bg-neutral-800/50 border-b border-neutral-200 dark:border-neutral-800 text-neutral-500 font-medium uppercase tracking-wider text-[11px]">
                <tr>
                  <th className="py-3 px-4">Device</th>
                  <th className="py-3 px-4">Status</th>
                  <th className="py-3 px-4">IP Address</th>
                  <th className="py-3 px-4">MAC Address</th>
                  <th className="py-3 px-4">TCP Port</th>
                  <th className="py-3 px-4">Last Seen</th>
                  <th className="py-3 px-4 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-neutral-100 dark:divide-neutral-800">
                {filteredDevices.map(device => {
                  const isProbing = probingId === device.id;

                  return (
                    <tr
                      key={device.id}
                      onClick={() => setSelectedDevice(device)}
                      className="hover:bg-neutral-50/80 dark:hover:bg-neutral-800/40 cursor-pointer transition-colors"
                    >
                      <td className="py-3 px-4">
                        <div className="flex items-center gap-2.5">
                          <DeviceIcon type={device.type} size={16} className="text-neutral-400 shrink-0" />
                          <div>
                            <span className="font-semibold text-neutral-900 dark:text-neutral-100 block">
                              {device.name}
                            </span>
                            <span className="text-[11px] text-neutral-400 block">
                              {device.os} · {device.type}
                            </span>
                          </div>
                        </div>
                      </td>

                      <td className="py-3 px-4">
                        <PowerStateBadge status={device.status} transition={device.powerTransition} />
                      </td>

                      <td className="py-3 px-4 font-mono text-neutral-800 dark:text-neutral-200">
                        {device.ip}
                      </td>

                      <td className="py-3 px-4 font-mono text-[11px] text-neutral-500">
                        {device.mac}
                      </td>

                      <td className="py-3 px-4 font-mono text-neutral-700 dark:text-neutral-300">
                        {device.monitoring.port} ({device.monitoring.protocolPreset})
                      </td>

                      <td className="py-3 px-4 text-neutral-500">
                        {device.lastSeen}
                      </td>

                      <td className="py-3 px-4 text-right" onClick={e => e.stopPropagation()}>
                        <div className="flex items-center justify-end gap-1">
                          {device.wol.enabled && (
                            <button
                              onClick={() => startWakeFlow(device)}
                              disabled={device.powerTransition !== 'none' || isPowerLocked(device.id)}
                              title="Wake-on-LAN"
                              className="disabled:opacity-50 disabled:cursor-not-allowed p-1 text-emerald-600 dark:text-emerald-400 hover:bg-emerald-500/10 rounded"
                            >
                              <Zap size={14} />
                            </button>
                          )}

                          {device.shutdown.enabled && device.status === 'online' && (
                            <button
                              onClick={() => requestShutdownConfirmation(device)}
                              disabled={device.powerTransition !== 'none' || isPowerLocked(device.id)}
                              title="Remote Shutdown"
                              className="disabled:opacity-50 disabled:cursor-not-allowed p-1 text-red-600 dark:text-red-400 hover:bg-red-500/10 rounded"
                            >
                              <Power size={14} />
                            </button>
                          )}

                          <button
                            onClick={e => handleProbe(device.id, e)}
                            disabled={isProbing}
                            title="Check Status Now"
                            className="p-1 text-neutral-400 hover:text-neutral-700 dark:hover:text-neutral-200 rounded"
                          >
                            <RefreshCw size={14} className={isProbing ? 'animate-spin text-emerald-500' : ''} />
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Empty State */}
      {filteredDevices.length === 0 && (
        <div className="text-center py-12 bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl p-6">
          <HardDrive size={32} className="mx-auto text-neutral-400 mb-2" />
          <h3 className="font-semibold text-sm text-neutral-800 dark:text-neutral-200">
            No devices found
          </h3>
          <p className="text-xs text-neutral-500 dark:text-neutral-400 mt-1 max-w-sm mx-auto">
            No devices matched your search or status filter. Try clearing filters or register a new device on your network.
          </p>
          <button
            onClick={() => {
              setSearchQuery('');
              setStatusFilter('all');
            }}
            className="mt-3 text-xs text-emerald-600 dark:text-emerald-400 hover:underline font-medium"
          >
            Clear filters
          </button>
        </div>
      )}
    </div>
  );
};
