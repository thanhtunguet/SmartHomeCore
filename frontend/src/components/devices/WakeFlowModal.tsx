import React from 'react';
import { useApp } from '../../context/AppContext';
import {
  Zap,
  CheckCircle2,
  AlertTriangle,
  Loader2,
  X,
  Radio,
  Terminal
} from 'lucide-react';

export const WakeFlowModal: React.FC = () => {
  const { wakeFlow, cancelWakeFlow } = useApp();

  if (!wakeFlow.active) return null;

  const failed = wakeFlow.step === 'failed' || wakeFlow.step === 'packet_sent_probing_timeout';
  const succeeded = wakeFlow.step === 'online_confirmed';
  const finished = failed || succeeded;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs animate-in fade-in duration-150">
      <div className="bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-800 rounded-xl max-w-lg w-full shadow-2xl overflow-hidden">
        {/* Header */}
        <div className="px-5 py-4 border-b border-neutral-200 dark:border-neutral-800 flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <div className="p-2 rounded-lg bg-amber-500/10 text-amber-500">
              <Zap size={18} />
            </div>
            <div>
              <h3 className="font-semibold text-sm text-neutral-900 dark:text-neutral-100">
                Wake-on-LAN Dispatch
              </h3>
              <p className="text-xs text-neutral-500 dark:text-neutral-400">
                Target: {wakeFlow.deviceName}
              </p>
            </div>
          </div>
          <button
            onClick={cancelWakeFlow}
            className="p-1 text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-200 rounded-md"
            aria-label="Close dialog"
          >
            <X size={16} />
          </button>
        </div>

        {/* Content Body */}
        <div className="p-5 space-y-5">
          {/* Progress Bar */}
          <div className="space-y-1.5">
            <div className="flex justify-between text-xs">
              <span className={`font-medium ${failed ? 'text-red-600 dark:text-red-400' : 'text-neutral-700 dark:text-neutral-300'}`}>
                {wakeFlow.message}
              </span>
              <span className="font-mono text-neutral-500 tabular-nums">
                {wakeFlow.progressPercent}%
              </span>
            </div>
            <div className="w-full h-1.5 bg-neutral-100 dark:bg-neutral-800 rounded-full overflow-hidden">
              <div
                className={`h-full transition-all duration-300 ${failed ? 'bg-red-500' : 'bg-emerald-500'}`}
                style={{ width: `${wakeFlow.progressPercent}%` }}
              ></div>
            </div>
          </div>

          {/* Stepper Status */}
          <div className="grid grid-cols-3 gap-2 text-center text-xs">
            <div className={`p-2.5 rounded-lg border ${
              wakeFlow.progressPercent >= 20
                ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-700 dark:text-emerald-300'
                : 'border-neutral-200 dark:border-neutral-800 text-neutral-400'
            }`}>
              <Radio size={14} className="mx-auto mb-1 text-emerald-500" />
              <div className="font-medium">1. Packet Formed</div>
            </div>

            <div className={`p-2.5 rounded-lg border ${
              wakeFlow.progressPercent >= 45
                ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-700 dark:text-emerald-300'
                : 'border-neutral-200 dark:border-neutral-800 text-neutral-400'
            }`}>
              <Radio size={14} className="mx-auto mb-1 text-emerald-500" />
              <div className="font-medium">2. Broadcast Sent</div>
            </div>

            <div className={`p-2.5 rounded-lg border ${
              succeeded
                ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-700 dark:text-emerald-300'
                : failed
                ? 'border-red-500/30 bg-red-500/5 text-red-700 dark:text-red-300'
                : wakeFlow.progressPercent >= 70
                ? 'border-amber-500/30 bg-amber-500/5 text-amber-700 dark:text-amber-300'
                : 'border-neutral-200 dark:border-neutral-800 text-neutral-400'
            }`}>
              {succeeded ? (
                <CheckCircle2 size={14} className="mx-auto mb-1 text-emerald-500" />
              ) : failed ? (
                <AlertTriangle size={14} className="mx-auto mb-1 text-red-500" />
              ) : (
                <Loader2 size={14} className="mx-auto mb-1 text-amber-500 animate-spin" />
              )}
              <div className="font-medium">3. TCP Probe</div>
            </div>
          </div>

          {/* Technical Diagnostics Output */}
          <div className="bg-neutral-950 rounded-lg p-3 border border-neutral-800 font-mono text-[11px] text-neutral-300 max-h-36 overflow-y-auto space-y-1">
            <div className="flex items-center gap-1.5 text-neutral-500 pb-1 border-b border-neutral-800/80">
              <Terminal size={12} />
              <span>Controller Socket Output</span>
            </div>
            {wakeFlow.logs.map((log, idx) => (
              <div key={idx} className="leading-relaxed text-neutral-400">
                {log}
              </div>
            ))}
          </div>

          <div className="p-3 bg-neutral-50 dark:bg-neutral-800/40 rounded-lg text-xs text-neutral-500 dark:text-neutral-400 leading-relaxed">
            <span className="font-semibold text-neutral-700 dark:text-neutral-300">Notice:</span> A Wake-on-LAN magic packet triggers hardware NIC boot. Operating system startup and TCP service readiness typically take between 15 to 45 seconds depending on hardware. The Controller keeps watching for up to its wake deadline even if you close this dialog.
          </div>
        </div>

        {/* Footer */}
        <div className="px-5 py-3 border-t border-neutral-200 dark:border-neutral-800 bg-neutral-50/50 dark:bg-neutral-900/50 flex justify-end">
          <button
            onClick={cancelWakeFlow}
            className="px-4 py-1.5 text-xs font-medium text-neutral-700 dark:text-neutral-300 bg-neutral-200 dark:bg-neutral-800 hover:bg-neutral-300 dark:hover:bg-neutral-700 rounded-lg transition-colors"
          >
            {finished ? 'Close' : 'Run in Background'}
          </button>
        </div>
      </div>
    </div>
  );
};
