import type { AgentInspection } from './schema';

export type InspectionTone = 'ok' | 'warn' | 'error' | 'info';

/** Whether the agent trusts this Controller's key, as far as the agent says. */
export type InspectionTrust = 'trusted' | 'untrusted' | 'unknown';

export interface InspectionView {
  tone: InspectionTone;
  headline: string;
  /** Extra sentences, one per line. */
  notes: string[];
  /** The id the agent reported (lower case); absent when no agent answered. */
  foundId?: string;
  /** The form already holds a different, non-empty id: commands signed for it would be rejected. */
  idDiffers: boolean;
  trust: InspectionTrust;
}

/**
 * Turns the Controller's answer to POST /agents/inspect into what the device form shows.
 * `currentAgentId` is whatever is in the form's Agent ID field now.
 */
export function describeInspection(res: AgentInspection, currentAgentId: string): InspectionView {
  if (!res.reachable || !res.agent_id) {
    return {
      tone: 'error',
      headline: res.error || 'No HomeCore agent answered at this address.',
      notes: [],
      idDiffers: false,
      trust: 'unknown'
    };
  }
  const foundId = res.agent_id.toLowerCase();
  const current = currentAgentId.trim().toLowerCase();
  const idDiffers = current !== '' && current !== foundId;
  const trust: InspectionTrust =
    res.controller_trusted === true ? 'trusted' : res.controller_trusted === false ? 'untrusted' : 'unknown';

  const notes: string[] = [];
  if (idDiffers) {
    notes.push('The agent at this address has a different id from the one entered. Commands would be rejected until they match.');
  }
  if (trust === 'untrusted') {
    notes.push(
      "This agent does not trust this Controller's key yet. Add --trusted-controller <key> (the key is shown below) to the agent's command and restart it."
    );
  } else if (trust === 'trusted') {
    notes.push('This agent trusts this Controller.');
  } else {
    notes.push('This agent version does not report which Controllers it trusts, so that cannot be checked.');
  }
  const tone: InspectionTone = idDiffers || trust === 'untrusted' ? 'warn' : trust === 'trusted' ? 'ok' : 'info';
  return {
    tone,
    headline: res.version ? `HomeCore agent ${res.version} found` : 'HomeCore agent found',
    notes,
    foundId,
    idDiffers,
    trust
  };
}
