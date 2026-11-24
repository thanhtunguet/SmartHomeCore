import React from 'react';
import { AlertTriangle, RefreshCw } from 'lucide-react';
import { useApp } from '../../context/AppContext';

/** Explains why data may be missing: Controller unreachable, endpoint not implemented, ... */
export const StatusBanner: React.FC = () => {
  const { connectionIssue, refreshDevices } = useApp();
  if (!connectionIssue) return null;

  const text = connectionIssue.network
    ? 'Cannot reach the HomeCore Controller. Showing the last data received; retrying automatically.'
    : connectionIssue.notImplemented
      ? 'This Controller does not implement some API endpoints yet (HTTP 501), so parts of the UI stay empty.'
      : `The Controller reported an error: ${connectionIssue.message}`;

  return (
    <div role="status" className="mb-4 flex items-center gap-2.5 px-3.5 py-2.5 rounded-lg text-xs bg-amber-500/10 text-amber-800 dark:text-amber-300 border border-amber-500/20">
      <AlertTriangle size={14} className="shrink-0" />
      <span className="flex-1">{text}</span>
      <button onClick={() => void refreshDevices().catch(() => undefined)} className="flex items-center gap-1 font-medium hover:underline shrink-0">
        <RefreshCw size={12} />
        <span>Retry</span>
      </button>
    </div>
  );
};
