/**
 * The command that starts the Agent on a managed machine (ADR-019), and the Agent id it needs.
 *
 * The Agent takes its id and the Controller public key as arguments, so it needs no config file and
 * writes nothing to disk. Run without them in a terminal, `homecore --mode agent` asks for this very
 * command and writes the systemd unit (Linux) or the shortcut (Windows) for it. The argument list
 * must stay identical to `Params.Args()` in `internal/agentsetup/parse.go`, which parses it back.
 */
import type { Device, OperatingSystem } from '../types';

/** Systems that have their own command (the Agent binary exists for these). */
export type AgentOs = 'windows' | 'linux' | 'macos';

export const AGENT_OSES: readonly { os: AgentOs; label: string }[] = [
  { os: 'windows', label: 'Windows' },
  { os: 'linux', label: 'Linux' },
  { os: 'macos', label: 'macOS' }
];

export const DEFAULT_AGENT_PORT = 9750;

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const NIL_UUID = '00000000-0000-0000-0000-000000000000';

/** True for a UUID the Controller accepts as an agent id (any version, not nil). */
export const isAgentId = (s: string): boolean => UUID.test(s.trim()) && s.trim() !== NIL_UUID;

/**
 * A new UUID version 7 (time-ordered, like the ids the Controller makes). `now` and `random` are
 * injectable for tests; by default the clock and `crypto.getRandomValues` are used.
 */
export const newAgentId = (
  now: number = Date.now(),
  random: (n: number) => Uint8Array = n => crypto.getRandomValues(new Uint8Array(n))
): string => {
  const b = random(16);
  // 48-bit big-endian millisecond timestamp, then version 7 and the RFC 9562 variant.
  let ms = now;
  for (let i = 5; i >= 0; i--) {
    b[i] = ms % 256;
    ms = Math.floor(ms / 256);
  }
  b[6] = (b[6] & 0x0f) | 0x70;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, x => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
};

export interface AgentCommandInput {
  agentId: string;
  /** The Controller public key (standard base64) from `GET /api/v1/system/controller-key`. */
  publicKey: string;
  port: number;
}

/** The arguments after the program name. */
export const agentArgs = ({ agentId, publicKey, port }: AgentCommandInput): string[] => [
  '--mode',
  'agent',
  '--agent-id',
  agentId.trim().toLowerCase(),
  '--trusted-controller',
  publicKey,
  '--agent-listen',
  `:${port}`
];

/** How the program is started on each system: in the folder where it was unpacked. */
const PROGRAM: Record<AgentOs, string> = {
  windows: '.\\homecore.exe',
  linux: './homecore',
  macos: './homecore'
};

/** The full command line for one system. */
export const agentCommand = (os: AgentOs, input: AgentCommandInput): string =>
  [PROGRAM[os], ...agentArgs(input)].join(' ');

/** The command tab to show first for a device: its own system when it has a command. */
export const defaultAgentOs = (deviceOs: OperatingSystem | undefined): AgentOs =>
  deviceOs === 'windows' || deviceOs === 'macos' ? deviceOs : 'linux';

/** True when the device has an agent binding the panel can describe. */
export const hasAgentBinding = (d: Pick<Device, 'shutdown'>): boolean =>
  d.shutdown.enabled && isAgentId(d.shutdown.agentId);
