import React from 'react';
import { useApp } from '../../context/AppContext';
import {
  Power,
  AlertTriangle,
  CheckCircle2,
  Loader2,
  X,
  ShieldAlert,
  Server,
  Terminal
} from 'lucide-react';

export const ShutdownFlowModal: React.FC = () => {
  const {
    shutdownFlow,
    confirmAndExecuteShutdown,
    cancelShutdownFlow,
    devices,
    isPowerLocked
  } = useApp();

  if (!shutdownFlow.active) return null;

  const targetDevice = devices.find(d => d.id === shutdownFlow.deviceId);
  const isConfirming = shutdownFlow.step === 'confirming';
  const isFinished = shutdownFlow.step === 'offline_verified' || shutdownFlow.step === 'failed';

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs animate-in fade-in duration-150">
      <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl max-w-lg w-full shadow-2xl overflow-hidden">
        {/* Header */}
        <div className={`px-5 py-4 border-b border-neutral-200 dark:border-neutral-800 flex items-center justify-between ${
          isConfirming ? 'bg-red-500/5' : ''
        }`}>
          <div className="flex items-center gap-2.5">
            <div className={`p-2 rounded-lg ${
              isConfirming ? 'bg-red-500/10 text-red-600 dark:text-red-400' : 'bg-amber-500/10 text-amber-500'
            }`}>
              <Power size={18} />
            </div>
            <div>
              <h3 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100">
                {isConfirming ? 'Confirm Remote Host Shutdown' : 'Shutdown Command Progress'}
              </h3>
              <p className="text-xs text-neutral-500 dark:text-neutral-400">
                Target: {shutdownFlow.deviceName} ({targetDevice?.ip})
              </p>
            </div>
          </div>
          <button
            onClick={cancelShutdownFlow}
            className="p-1 text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-200 rounded-md"
            aria-label="Close dialog"
          >
            <X size={16} />
          </button>
        </div>

        {/* Phase 1: Confirmation Form */}
        {isConfirming ? (
          <div className="p-5 space-y-4">
            <div className="flex items-start gap-3 p-3.5 rounded-lg bg-red-500/10 border border-red-500/20 text-red-900 dark:text-red-200 text-xs">
              <ShieldAlert size={18} className="shrink-0 text-red-500 mt-0.5" />
              <div>
                <span className="font-semibold block mb-0.5">Warning: Unsaved Data Will Be Lost</span>
                <span>
                  This operation initiates a secure ACPI poweroff command via the HomeCore Agent. All open sessions, unsaved files, and background containers on this host will terminate immediately.
                </span>
              </div>
            </div>

            <div className="bg-neutral-50 dark:bg-neutral-800/50 rounded-lg p-3.5 border border-neutral-200 dark:border-neutral-800 space-y-2 text-xs">
              <div className="flex justify-between">
                <span className="text-neutral-500 dark:text-neutral-400">Target Host</span>
                <span className="font-medium text-neutral-800 dark:text-neutral-200">{targetDevice?.name}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-neutral-500 dark:text-neutral-400">IP Address</span>
                <span className="font-mono text-neutral-800 dark:text-neutral-200">{targetDevice?.ip}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-neutral-500 dark:text-neutral-400">Operating System</span>
                <span className="capitalize text-neutral-800 dark:text-neutral-200">{targetDevice?.os}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-neutral-500 dark:text-neutral-400">Agent Endpoint</span>
                <span className="font-mono text-neutral-800 dark:text-neutral-200">
                  {targetDevice?.shutdown.agentScheme}://{targetDevice?.ip}:{targetDevice?.shutdown.agentPort}
                </span>
              </div>
            </div>

            <div className="flex items-center justify-end gap-2.5 pt-2">
              <button
                type="button"
                onClick={cancelShutdownFlow}
                className="px-4 py-2 text-xs font-medium text-neutral-700 dark:text-neutral-300 hover:bg-neutral-100 dark:hover:bg-neutral-800 rounded-lg transition-colors"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={confirmAndExecuteShutdown}
                disabled={shutdownFlow.deviceId !== null && isPowerLocked(shutdownFlow.deviceId)}
                className="px-4 py-2 text-xs font-medium text-white bg-red-600 hover:bg-red-700 rounded-lg shadow-sm transition-colors flex items-center gap-1.5 disabled:opacity-50"
              >
                <Power size={14} />
                <span>Confirm Power Off</span>
              </button>
            </div>
          </div>
        ) : (
          /* Phase 2: Live Progress & Multi-Step Verification */
          <div className="p-5 space-y-5">
            {/* Progress Bar */}
            <div className="space-y-1.5">
              <div className="flex justify-between text-xs">
                <span className={`font-medium ${shutdownFlow.step === 'failed' ? 'text-red-600 dark:text-red-400' : 'text-neutral-700 dark:text-neutral-300'}`}>
                  {shutdownFlow.message}
                </span>
                <span className="font-mono text-neutral-500 tabular-nums">
                  {shutdownFlow.progressPercent}%
                </span>
              </div>
              <div className="w-full h-1.5 bg-neutral-100 dark:bg-neutral-800 rounded-full overflow-hidden">
                <div
                  className={`h-full transition-all duration-300 ${
                    shutdownFlow.step === 'offline_verified' ? 'bg-emerald-500' : shutdownFlow.step === 'failed' ? 'bg-red-500' : 'bg-amber-500'
                  }`}
                  style={{ width: `${shutdownFlow.progressPercent}%` }}
                ></div>
              </div>
            </div>

            {/* Step Indicators */}
            <div className="grid grid-cols-4 gap-1.5 text-center text-[11px]">
              <div className={`p-2 rounded-lg border ${
                shutdownFlow.progressPercent >= 30
                  ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-700 dark:text-emerald-300'
                  : 'border-neutral-200 dark:border-neutral-800 text-neutral-400'
              }`}>
                <Server size={13} className="mx-auto mb-1" />
                <span>1. Connect</span>
              </div>

              <div className={`p-2 rounded-lg border ${
                shutdownFlow.progressPercent >= 50
                  ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-700 dark:text-emerald-300'
                  : 'border-neutral-200 dark:border-neutral-800 text-neutral-400'
              }`}>
                <ShieldAlert size={13} className="mx-auto mb-1" />
                <span>2. Auth</span>
              </div>

              <div className={`p-2 rounded-lg border ${
                shutdownFlow.progressPercent >= 75
                  ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-700 dark:text-emerald-300'
                  : 'border-neutral-200 dark:border-neutral-800 text-neutral-400'
              }`}>
                <CheckCircle2 size={13} className="mx-auto mb-1" />
                <span>3. Agent ACK</span>
              </div>

              <div className={`p-2 rounded-lg border ${
                shutdownFlow.step === 'offline_verified'
                  ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-700 dark:text-emerald-300'
                  : shutdownFlow.progressPercent >= 88
                  ? 'border-amber-500/30 bg-amber-500/5 text-amber-700 dark:text-amber-300'
                  : 'border-neutral-200 dark:border-neutral-800 text-neutral-400'
              }`}>
                {shutdownFlow.step === 'offline_verified' ? (
                  <CheckCircle2 size={13} className="mx-auto mb-1 text-emerald-500" />
                ) : shutdownFlow.step === 'failed' ? (
                  <AlertTriangle size={13} className="mx-auto mb-1 text-red-500" />
                ) : (
                  <Loader2 size={13} className="mx-auto mb-1 text-amber-500 animate-spin" />
                )}
                <span>4. Offline</span>
              </div>
            </div>

            {/* Socket / Agent Trace Log */}
            <div className="bg-neutral-950 rounded-lg p-3 border border-neutral-800 font-mono text-[11px] text-neutral-300 max-h-36 overflow-y-auto space-y-1">
              <div className="flex items-center gap-1.5 text-neutral-500 pb-1 border-b border-neutral-800/80">
                <Terminal size={12} />
                <span>Agent Protocol Session Log</span>
              </div>
              {shutdownFlow.logs.map((log, idx) => (
                <div key={idx} className="leading-relaxed text-neutral-400">
                  {log}
                </div>
              ))}
            </div>

            {/* Action Buttons */}
            <div className="flex justify-end pt-2">
              <button
                type="button"
                onClick={cancelShutdownFlow}
                className="px-4 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-200 dark:bg-neutral-800 hover:bg-neutral-300 dark:hover:bg-neutral-700 rounded-lg transition-colors"
              >
                {isFinished ? 'Close' : 'Dismiss to Background'}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
};
