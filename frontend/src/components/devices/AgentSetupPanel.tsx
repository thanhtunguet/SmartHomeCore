import React from 'react';
import { useApp } from '../../context/AppContext';
import { useCopy } from '../../hooks/useCopy';
import type { OperatingSystem } from '../../types';
import {
  AGENT_OSES,
  AgentOs,
  agentCommand,
  defaultAgentOs,
  isAgentId
} from '../../api/agentCommand';
import { AlertCircle, Check, Copy, RefreshCw, Terminal } from 'lucide-react';

interface AgentSetupPanelProps {
  agentId: string;
  port: number;
  scheme: 'http' | 'https';
  /** The device's own system: its tab is shown first. */
  deviceOs?: OperatingSystem;
  /** Makes a new id; when given, a Generate button is shown next to the id. */
  onGenerate?: () => void;
}

/** What happens on each system when the binary is run without arguments. */
const HINTS: Record<AgentOs, string> = {
  windows:
    'Run it in PowerShell on the device, in the folder with homecore.exe. To install instead, run .\\homecore.exe --mode agent with nothing else: it asks for this command, creates a "HomeCore Agent" shortcut that runs as administrator and prints how to start it at sign-in and open the firewall.',
  linux:
    'Run it on the device, in the folder with homecore. To install as a service instead, run ./homecore --mode agent with nothing else: it asks for this command, writes homecore-agent.service and its polkit rule and prints the install steps.',
  macos:
    'Run it in Terminal on the device, in the folder with homecore. It runs in the foreground; macOS has no generated service file yet.'
};

const CopyButton: React.FC<{ label: string; active: boolean; disabled?: boolean; onClick: () => void }> = ({
  label,
  active,
  disabled,
  onClick
}) => (
  <button
    type="button"
    onClick={onClick}
    disabled={disabled}
    aria-label={`Copy ${label}`}
    className="shrink-0 flex items-center gap-1 px-2 py-1 text-[11px] font-medium text-neutral-600 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
  >
    {active ? <Check size={12} className="text-emerald-500" /> : <Copy size={12} />}
    <span>{active ? 'Copied' : 'Copy'}</span>
  </button>
);

const CodeBlock: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <code className="block font-mono text-[11px] text-neutral-700 dark:text-neutral-300 bg-white dark:bg-neutral-900 border border-neutral-200 dark:border-neutral-700/60 rounded-md px-2 py-1.5 break-all select-all">
    {children}
  </code>
);

/**
 * Agent ID and the command to start the Agent on the device, for Windows, Linux and macOS, each with
 * its own copy button. The command carries the id and the Controller public key (ADR-019), so the
 * Agent needs no config file.
 */
export const AgentSetupPanel: React.FC<AgentSetupPanelProps> = ({ agentId, port, scheme, deviceOs, onGenerate }) => {
  const { controllerKey, controllerKeyError, loadControllerKey } = useApp();
  const { copied, failed, copy } = useCopy();
  const [os, setOs] = React.useState<AgentOs>(() => defaultAgentOs(deviceOs));

  React.useEffect(() => {
    if (!controllerKey) void loadControllerKey();
  }, []);

  const id = agentId.trim();
  const idOk = isAgentId(id);
  const portOk = Number.isInteger(port) && port >= 1 && port <= 65535;
  const publicKey = controllerKey?.public_key ?? '';
  const ready = idOk && portOk && publicKey !== '';
  const command = ready ? agentCommand(os, { agentId: id, publicKey, port }) : '';

  return (
    <div className="space-y-3 text-xs">
      <div className="space-y-1">
        <div className="flex items-center justify-between gap-2">
          <span className="font-medium text-neutral-800 dark:text-neutral-200">Agent ID</span>
          <div className="flex items-center gap-1.5">
            {onGenerate && (
              <button
                type="button"
                onClick={onGenerate}
                className="shrink-0 flex items-center gap-1 px-2 py-1 text-[11px] font-medium text-neutral-600 dark:text-neutral-300 bg-neutral-100 dark:bg-neutral-800 hover:bg-neutral-200 dark:hover:bg-neutral-700 rounded-md transition-colors"
              >
                <RefreshCw size={12} />
                <span>Generate</span>
              </button>
            )}
            <CopyButton label="Agent ID" active={copied === 'id'} disabled={!idOk} onClick={() => copy('id', id)} />
          </div>
        </div>
        <CodeBlock>{idOk ? id : onGenerate ? 'Press Generate, or enter an existing id above' : '—'}</CodeBlock>
      </div>

      <div className="space-y-1.5">
        <div className="flex items-center justify-between gap-2">
          <span className="font-medium text-neutral-800 dark:text-neutral-200 flex items-center gap-1.5">
            <Terminal size={13} />
            <span>Command to run on the device</span>
          </span>
          <CopyButton label="command" active={copied === 'cmd'} disabled={!ready} onClick={() => copy('cmd', command)} />
        </div>

        <div role="tablist" aria-label="Operating system" className="flex gap-1">
          {AGENT_OSES.map(o => (
            <button
              key={o.os}
              type="button"
              role="tab"
              aria-selected={os === o.os}
              onClick={() => setOs(o.os)}
              className={`px-2.5 py-1 rounded-md text-[11px] font-medium transition-colors ${
                os === o.os
                  ? 'bg-emerald-600 text-white'
                  : 'bg-neutral-100 dark:bg-neutral-800 text-neutral-600 dark:text-neutral-300 hover:bg-neutral-200 dark:hover:bg-neutral-700'
              }`}
            >
              {o.label}
            </button>
          ))}
        </div>

        {ready ? (
          <CodeBlock>{command}</CodeBlock>
        ) : (
          <p className="text-[11px] text-neutral-400">
            {!idOk
              ? 'The command appears once the Agent ID is a valid UUID.'
              : !portOk
              ? 'Enter a port from 1 to 65535.'
              : controllerKeyError ?? 'Loading the Controller public key…'}
          </p>
        )}
        <p className="text-[11px] text-neutral-500 dark:text-neutral-400 leading-relaxed">{HINTS[os]}</p>
      </div>

      <div className="pt-1.5 border-t border-neutral-200 dark:border-neutral-700/60 text-[11px] text-neutral-500 dark:text-neutral-400 flex items-start gap-1.5">
        <AlertCircle size={13} className="shrink-0 mt-px text-neutral-400" />
        <span>
          {failed
            ? 'Could not copy automatically: select the text and copy it. '
            : 'The Agent keeps nothing on disk: the id and the Controller key are in the command. '}
          {scheme === 'https' &&
            'This command serves plain HTTP; for HTTPS set agent.tls in a config file instead, or choose HTTP above. '}
          Keep the port open to your LAN or VPN only.
        </span>
      </div>
    </div>
  );
};
