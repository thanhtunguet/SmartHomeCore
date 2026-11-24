import { describe, expect, it } from 'vitest';
import { agentArgs, agentCommand, defaultAgentOs, isAgentId, newAgentId } from './agentCommand';

const ID = '0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b';
const KEY = 'dj4QXtbSB0ZIlhUdRL7HTJBmDQD61ZEHAnLQrQJw/PI=';

describe('newAgentId', () => {
  it('makes a version 7 UUID carrying the timestamp', () => {
    const id = newAgentId(0x0198a1b2c3d4, n => new Uint8Array(n).fill(0xff));
    expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(id.slice(0, 13)).toBe('0198a1b2-c3d4');
    expect(isAgentId(id)).toBe(true);
  });

  it('does not repeat', () => {
    const ids = new Set(Array.from({ length: 200 }, () => newAgentId()));
    expect(ids.size).toBe(200);
  });
});

describe('isAgentId', () => {
  it('accepts a UUID in any case and rejects the rest', () => {
    expect(isAgentId(ID)).toBe(true);
    expect(isAgentId(` ${ID.toUpperCase()} `)).toBe(true);
    expect(isAgentId('')).toBe(false);
    expect(isAgentId('not-a-uuid')).toBe(false);
    expect(isAgentId('00000000-0000-0000-0000-000000000000')).toBe(false);
  });
});

describe('agentCommand', () => {
  const input = { agentId: ID.toUpperCase(), publicKey: KEY, port: 9750 };

  it('lists the arguments the Agent parses (same order as internal/agentsetup Params.Args)', () => {
    expect(agentArgs(input)).toEqual([
      '--mode', 'agent', '--agent-id', ID, '--trusted-controller', KEY, '--agent-listen', ':9750'
    ]);
  });

  it('starts the program the way each system does', () => {
    expect(agentCommand('windows', input)).toBe(
      `.\\homecore.exe --mode agent --agent-id ${ID} --trusted-controller ${KEY} --agent-listen :9750`
    );
    expect(agentCommand('linux', input)).toBe(
      `./homecore --mode agent --agent-id ${ID} --trusted-controller ${KEY} --agent-listen :9750`
    );
    expect(agentCommand('macos', { ...input, port: 9800 })).toContain('--agent-listen :9800');
  });

  it('shows the device system first when it has a command', () => {
    expect(defaultAgentOs('windows')).toBe('windows');
    expect(defaultAgentOs('macos')).toBe('macos');
    expect(defaultAgentOs('linux')).toBe('linux');
    expect(defaultAgentOs('android')).toBe('linux');
    expect(defaultAgentOs(undefined)).toBe('linux');
  });
});
