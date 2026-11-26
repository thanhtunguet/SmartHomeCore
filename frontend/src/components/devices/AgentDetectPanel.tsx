import React, { useCallback, useEffect, useRef, useState } from 'react';
import { inspectAgent } from '../../api/endpoints';
import { describeInspection, InspectionView } from '../../api/agentInspection';
import { ApiError } from '../../api/client';
import { AlertCircle, Check, Loader2, Radar } from 'lucide-react';

interface AgentDetectPanelProps {
  /** Device IP from the form; the Controller refuses names and public addresses. */
  host: string;
  port: number;
  scheme: 'http' | 'https';
  /** What the Agent ID field holds now. */
  agentId: string;
  /** Called with the id the agent reported, when the user accepts it (or on the first look, see `fillOnFirstLook`). */
  onUseId: (id: string) => void;
  /** New binding: take the id of an agent that is already installed instead of keeping a generated one. */
  fillOnFirstLook: boolean;
}

const TONES: Record<InspectionView['tone'], string> = {
  ok: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 border-emerald-500/20',
  warn: 'bg-amber-500/10 text-amber-700 dark:text-amber-300 border-amber-500/20',
  error: 'bg-red-500/10 text-red-700 dark:text-red-300 border-red-500/20',
  info: 'bg-neutral-500/10 text-neutral-700 dark:text-neutral-300 border-neutral-500/20'
};

/**
 * Asks the Controller to look at the agent running at host:port (POST /agents/inspect) and shows
 * its id, version and whether it trusts this Controller. The browser cannot ask the agent itself
 * (CORS, mixed content), so the Controller makes the call.
 */
export const AgentDetectPanel: React.FC<AgentDetectPanelProps> = ({
  host,
  port,
  scheme,
  agentId,
  onUseId,
  fillOnFirstLook
}) => {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Awaited<ReturnType<typeof inspectAgent>> | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const latest = useRef(0); // ignores an answer that arrives after a newer request
  const agentIdRef = useRef(agentId);
  agentIdRef.current = agentId;

  const detect = useCallback(
    async (fill: boolean) => {
      const ip = host.trim();
      if (!ip) {
        setResult(null);
        setFailure('Enter the device IP address on the Basic tab first.');
        return;
      }
      const mine = ++latest.current;
      setBusy(true);
      setFailure(null);
      try {
        const res = await inspectAgent({ host: ip, port, scheme });
        if (mine !== latest.current) return;
        setResult(res);
        if (fill && res.reachable && res.agent_id) onUseId(res.agent_id.toLowerCase());
      } catch (e) {
        if (mine !== latest.current) return;
        setResult(null);
        setFailure(e instanceof ApiError ? e.message : 'Could not ask the Controller to detect the agent.');
      } finally {
        if (mine === latest.current) setBusy(false);
      }
    },
    [host, port, scheme, onUseId]
  );

  // One look when the panel opens, so an installed agent is found without clicking.
  useEffect(() => {
    void detect(fillOnFirstLook);
    return () => {
      latest.current++;
    };
    // Deliberately only on open; the Detect button repeats it with the current values.
  }, []);

  const view = result ? describeInspection(result, agentId) : null;

  return (
    <div className="space-y-2 text-xs" data-testid="agent-detect">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium text-neutral-700 dark:text-neutral-300">
          Agent at {host.trim() || 'device IP'}:{port}
        </span>
        <button
          type="button"
          onClick={() => void detect(true)}
          disabled={busy}
          className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md border border-neutral-200 dark:border-neutral-700 text-neutral-700 dark:text-neutral-200 hover:bg-neutral-100 dark:hover:bg-neutral-800 disabled:opacity-50"
        >
          {busy ? <Loader2 size={12} className="animate-spin" /> : <Radar size={12} />}
          <span>{busy ? 'Detecting…' : 'Detect'}</span>
        </button>
      </div>

      {failure && (
        <div role="alert" className={`flex items-start gap-2 p-2.5 rounded-lg border ${TONES.error}`}>
          <AlertCircle size={13} className="shrink-0 mt-0.5" />
          <span>{failure}</span>
        </div>
      )}

      {view && (
        <div className={`p-2.5 rounded-lg border space-y-1.5 ${TONES[view.tone]}`}>
          <div className="flex items-start gap-2 font-medium">
            {view.tone === 'ok' ? (
              <Check size={13} className="shrink-0 mt-0.5" />
            ) : (
              <AlertCircle size={13} className="shrink-0 mt-0.5" />
            )}
            <span>{view.headline}</span>
          </div>
          {view.foundId && <div className="font-mono text-[11px] break-all select-all">id {view.foundId}</div>}
          {view.notes.map(n => (
            <p key={n} className="text-[11px] leading-relaxed">
              {n}
            </p>
          ))}
          {view.idDiffers && view.foundId && (
            <button
              type="button"
              onClick={() => onUseId(view.foundId as string)}
              className="text-[11px] font-medium underline underline-offset-2"
            >
              Use the agent&apos;s id
            </button>
          )}
        </div>
      )}
    </div>
  );
};
