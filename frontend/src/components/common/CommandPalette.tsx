import React, { useState, useEffect } from 'react';
import { useApp } from '../../context/AppContext';
import {
  Search,
  X,
  HardDrive,
  LayoutDashboard,
  Globe,
  ArrowLeftRight,
  Activity,
  Cpu,
  Settings,
  Zap,
  Power,
  RefreshCw
} from 'lucide-react';
import { StatusBadge } from '../common/StatusBadge';

interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
}

export const CommandPalette: React.FC<CommandPaletteProps> = ({
  isOpen,
  onClose
}) => {
  const {
    devices,
    setActiveTab,
    setSelectedDevice,
    startWakeFlow,
    requestShutdownConfirmation,
    triggerDDNSSync,
    isPowerLocked
  } = useApp();

  const [query, setQuery] = useState('');

  // Handle Escape key
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'k' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        if (isOpen) onClose();
      }
      if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  const filteredDevices = devices.filter(
    d =>
      d.name.toLowerCase().includes(query.toLowerCase()) ||
      d.ip.toLowerCase().includes(query.toLowerCase()) ||
      d.mac.toLowerCase().includes(query.toLowerCase())
  );

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-20 p-4 bg-black/60 backdrop-blur-xs animate-in fade-in duration-150">
      <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl max-w-xl w-full shadow-2xl overflow-hidden flex flex-col">
        {/* Search Input */}
        <div className="p-3 border-b border-neutral-200 dark:border-neutral-800 flex items-center gap-2.5">
          <Search size={16} className="text-neutral-400 shrink-0 ml-1" />
          <input
            type="text"
            autoFocus
            placeholder="Type a command or device name..."
            value={query}
            onChange={e => setQuery(e.target.value)}
            className="flex-1 bg-transparent border-none text-xs text-neutral-900 dark:text-neutral-100 placeholder-neutral-400 focus:outline-hidden"
          />
          <button
            onClick={onClose}
            className="p-1 text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-200 rounded"
          >
            <X size={15} />
          </button>
        </div>

        {/* Results List */}
        <div className="max-h-80 overflow-y-auto p-2 space-y-1 text-xs">
          {/* Section: Quick Navigation */}
          {!query && (
            <div className="px-2 py-1 text-[11px] font-semibold text-neutral-400 uppercase tracking-wider">
              Navigation
            </div>
          )}

          {!query && (
            <>
              <button
                onClick={() => {
                  setActiveTab('dashboard');
                  onClose();
                }}
                className="w-full flex items-center gap-2.5 px-3 py-2 rounded-lg text-neutral-700 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800 text-left"
              >
                <LayoutDashboard size={15} className="text-neutral-400" />
                <span>Go to Dashboard</span>
              </button>

              <button
                onClick={() => {
                  setActiveTab('devices');
                  onClose();
                }}
                className="w-full flex items-center gap-2.5 px-3 py-2 rounded-lg text-neutral-700 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800 text-left"
              >
                <HardDrive size={15} className="text-neutral-400" />
                <span>Go to Devices Inventory</span>
              </button>

              <button
                onClick={() => {
                  setActiveTab('ddns');
                  onClose();
                }}
                className="w-full flex items-center gap-2.5 px-3 py-2 rounded-lg text-neutral-700 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800 text-left"
              >
                <Globe size={15} className="text-neutral-400" />
                <span>Go to Cloudflare DDNS</span>
              </button>

              <button
                onClick={() => {
                  setActiveTab('portforward');
                  onClose();
                }}
                className="w-full flex items-center gap-2.5 px-3 py-2 rounded-lg text-neutral-700 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800 text-left"
              >
                <ArrowLeftRight size={15} className="text-neutral-400" />
                <span>Go to Tunnel</span>
              </button>

              <button
                onClick={() => {
                  triggerDDNSSync();
                  onClose();
                }}
                className="w-full flex items-center gap-2.5 px-3 py-2 rounded-lg text-emerald-700 dark:text-emerald-300 hover:bg-emerald-500/10 text-left"
              >
                <RefreshCw size={15} className="text-emerald-500" />
                <span>Quick Action: Synchronize Cloudflare DNS</span>
              </button>
            </>
          )}

          {/* Section: Devices */}
          <div className="px-2 py-1 text-[11px] font-semibold text-neutral-400 uppercase tracking-wider mt-2">
            Devices ({filteredDevices.length})
          </div>

          {filteredDevices.map(device => (
            <div
              key={device.id}
              className="flex items-center justify-between p-2 rounded-lg hover:bg-neutral-100 dark:hover:bg-neutral-800 transition-colors"
            >
              <button
                onClick={() => {
                  setSelectedDevice(device);
                  setActiveTab('devices');
                  onClose();
                }}
                className="flex items-center gap-2.5 text-left flex-1 min-w-0"
              >
                <StatusBadge status={device.status} showText={false} size="sm" />
                <div className="truncate">
                  <span className="font-medium text-neutral-900 dark:text-neutral-100 block truncate">
                    {device.name}
                  </span>
                  <span className="font-mono text-[11px] text-neutral-400 block">
                    {device.ip} · Port {device.monitoring.port} ({device.monitoring.protocolPreset})
                  </span>
                </div>
              </button>

              <div className="flex items-center gap-1 shrink-0 ml-2">
                {device.wol.enabled && (
                  <button
                    disabled={device.powerTransition !== 'none' || isPowerLocked(device.id)}
                    onClick={() => {
                      startWakeFlow(device);
                      onClose();
                    }}
                    title="Wake-on-LAN"
                    className="disabled:opacity-50 disabled:cursor-not-allowed p-1.5 text-emerald-600 dark:text-emerald-400 hover:bg-emerald-500/10 rounded"
                  >
                    <Zap size={14} />
                  </button>
                )}
                {device.shutdown.enabled && device.status === 'online' && (
                  <button
                    disabled={device.powerTransition !== 'none' || isPowerLocked(device.id)}
                    onClick={() => {
                      requestShutdownConfirmation(device);
                      onClose();
                    }}
                    title="Shutdown"
                    className="disabled:opacity-50 disabled:cursor-not-allowed p-1.5 text-red-600 dark:text-red-400 hover:bg-red-500/10 rounded"
                  >
                    <Power size={14} />
                  </button>
                )}
              </div>
            </div>
          ))}

          {filteredDevices.length === 0 && (
            <div className="p-4 text-center text-neutral-400 text-xs">
              No matching devices or commands found.
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
