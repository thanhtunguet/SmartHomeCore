import { describe, expect, it } from 'vitest';
import { describeInspection } from './agentInspection';

const ID = '0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b';
const OTHER = '0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f0000';

describe('describeInspection', () => {
  it('reports an unreachable address with the Controller\'s reason', () => {
    const v = describeInspection({ reachable: false, error: 'cannot reach the agent at 10.0.0.2:9750: connection refused' }, '');
    expect(v.tone).toBe('error');
    expect(v.headline).toContain('connection refused');
    expect(v.foundId).toBeUndefined();
  });

  it('falls back to a generic message when the Controller gives no reason', () => {
    expect(describeInspection({ reachable: false }, '').headline).toMatch(/No HomeCore agent/);
  });

  it('is ok when the agent trusts this Controller and the id matches or is empty', () => {
    const res = { reachable: true, agent_id: ID, version: 'v1.2.0', trusted_key_ids: ['3f9a0c1d2b4e5f60'], controller_trusted: true };
    for (const current of ['', ID, ID.toUpperCase(), `  ${ID} `]) {
      const v = describeInspection(res, current);
      expect(v).toMatchObject({ tone: 'ok', trust: 'trusted', idDiffers: false, foundId: ID });
    }
    expect(describeInspection(res, '').headline).toBe('HomeCore agent v1.2.0 found');
  });

  it('warns when the agent does not trust this Controller', () => {
    const v = describeInspection({ reachable: true, agent_id: ID, trusted_key_ids: [], controller_trusted: false }, ID);
    expect(v.tone).toBe('warn');
    expect(v.trust).toBe('untrusted');
    expect(v.notes.join(' ')).toContain('--trusted-controller');
  });

  it('warns when the form holds a different id than the agent reports', () => {
    const v = describeInspection({ reachable: true, agent_id: ID, controller_trusted: true }, OTHER);
    expect(v).toMatchObject({ tone: 'warn', idDiffers: true, foundId: ID });
  });

  it('does not claim anything about trust for an agent that does not report its keys', () => {
    const v = describeInspection({ reachable: true, agent_id: ID, version: 'v1.1.1' }, '');
    expect(v).toMatchObject({ tone: 'info', trust: 'unknown' });
    expect(v.notes.join(' ')).toMatch(/does not report/);
  });
});
