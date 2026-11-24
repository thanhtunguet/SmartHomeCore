/**
 * Helpers for the Tunnel (port forwarding) screen (ADR-022): connection types, client-side checks of a rule
 * (the Controller validates again), the wording of a rule's runtime status and the choices for the
 * local interface. Pure, so it is unit tested.
 */
import type { PortForward, PortForwardProtocol } from './schema';

export const MAX_RULE_NAME_LENGTH = 100;
export const ALL_INTERFACES = '0.0.0.0';

export const PROTOCOLS: { value: PortForwardProtocol; label: string; description: string }[] = [
  { value: 'tcp', label: 'TCP', description: 'Connection-oriented traffic: SSH, HTTP, databases.' },
  { value: 'udp', label: 'UDP', description: 'Datagrams: DNS, games, VPN, streaming.' },
  { value: 'universal', label: 'Universal', description: 'TCP and UDP on the same port.' },
];

export const protocolLabel = (p: PortForwardProtocol): string => PROTOCOLS.find(x => x.value === p)?.label ?? p;

export interface RuleDraft {
  name: string;
  protocol: PortForwardProtocol;
  listenAddress: string;
  listenPort: string;
  targetHost: string;
  targetPort: string;
  enabled: boolean;
}

export const emptyDraft = (): RuleDraft => ({
  name: '',
  protocol: 'tcp',
  listenAddress: ALL_INTERFACES,
  listenPort: '',
  targetHost: '',
  targetPort: '',
  enabled: true,
});

export const draftFromRule = (r: PortForward): RuleDraft => ({
  name: r.name,
  protocol: r.protocol,
  listenAddress: r.listen_address,
  listenPort: String(r.listen_port),
  targetHost: r.target_host,
  targetPort: String(r.target_port),
  enabled: r.enabled,
});

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
/** Loose but safe: hex groups and colons (with an optional `::`), optionally ending in an IPv4. */
export function isIPv6(s: string): boolean {
  if (!s.includes(':') || !/^[0-9A-Fa-f:.]+$/.test(s)) return false;
  const dbl = s.split('::');
  if (dbl.length > 2) return false;
  let groups = 0;
  for (const part of dbl) {
    if (part === '') continue;
    const items = part.split(':');
    for (let i = 0; i < items.length; i++) {
      const it = items[i];
      if (it.includes('.')) {
        // an embedded IPv4 is only valid as the very last group
        if (!(dbl.length === 1 || part === dbl[1]) || i !== items.length - 1 || !IPV4.test(it)) return false;
        groups += 2;
      } else if (/^[0-9A-Fa-f]{1,4}$/.test(it)) groups += 1;
      else return false;
    }
  }
  return dbl.length === 2 ? groups < 8 : groups === 8;
}
export const isIPv4 = (s: string): boolean => IPV4.test(s);
export const isIP = (s: string): boolean => isIPv4(s) || isIPv6(s);

const HOSTNAME = /^(?=.{1,253}$)([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?)(\.[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?)*\.?$/;

const isUnspecified = (ip: string) => ip === '0.0.0.0' || /^(0{1,4}:){7}0{1,4}$|^::$/.test(ip);
const isLoopbackTarget = (h: string) => h.toLowerCase() === 'localhost' || /^127\./.test(h) || h === '::1';

const isLoopbackAddr = (ip: string) => /^127\./.test(ip) || ip === '::1';
/** The target is this very listener: the same literal address, or a loopback target reached through an all-interfaces/loopback listener. */
const isSelfTarget = (listen: string, target: string): boolean =>
  target === listen || (isLoopbackTarget(target) && (isUnspecified(listen) || isLoopbackAddr(listen)));

const parsePort = (s: string): number | null => {
  const t = s.trim();
  if (!/^\d{1,5}$/.test(t)) return null;
  const n = Number(t);
  return n >= 1 && n <= 65535 ? n : null;
};

export interface RuleValue {
  name: string;
  protocol: PortForwardProtocol;
  listenAddress: string;
  listenPort: number;
  targetHost: string;
  targetPort: number;
  enabled: boolean;
}

/** Trimmed, typed fields, or the first problem as a message. */
export function validateDraft(d: RuleDraft): { value: RuleValue } | { error: string } {
  const name = d.name.trim();
  if (name === '') return { error: 'Give the rule a name, for example "NAS web".' };
  if ([...name].length > MAX_RULE_NAME_LENGTH) return { error: `The name can have at most ${MAX_RULE_NAME_LENGTH} characters.` };
  if (/[\u0000-\u001f\u007f]/.test(name)) return { error: 'The name cannot contain control characters.' };

  const listenAddress = d.listenAddress.trim() || ALL_INTERFACES;
  if (!isIP(listenAddress)) return { error: 'The local interface must be an IP address such as 0.0.0.0 or 192.168.1.10.' };

  const listenPort = parsePort(d.listenPort);
  if (listenPort === null) return { error: 'The listen port must be a number from 1 to 65535.' };

  const targetHost = d.targetHost.trim();
  if (targetHost === '') return { error: 'Enter the target host (an IP address or a host name).' };
  if (targetHost.length > 253) return { error: 'The target host is too long (at most 253 characters).' };
  if (!isIP(targetHost) && !HOSTNAME.test(targetHost)) return { error: 'The target host must be an IP address or a host name without spaces or slashes.' };

  const targetPort = parsePort(d.targetPort);
  if (targetPort === null) return { error: 'The target port must be a number from 1 to 65535.' };

  // A rule that forwards a port to itself would feed its own connections back to it.
  if (listenPort === targetPort && isSelfTarget(listenAddress, targetHost)) {
    return { error: 'This rule would forward the port to itself. Choose a different target or port.' };
  }
  return { value: { name, protocol: d.protocol, listenAddress, listenPort, targetHost, targetPort, enabled: d.enabled } };
}

export type StatusTone = 'ok' | 'warn' | 'off';

/** What to show for a rule: the stored switch decides "Off", otherwise the runtime status. */
export function statusState(r: Pick<PortForward, 'enabled' | 'status' | 'last_error'>): { label: string; tone: StatusTone; detail?: string } {
  if (r.status === 'error') return { label: 'Error', tone: 'warn', detail: r.last_error || 'The rule could not start.' };
  if (r.status === 'running') return { label: 'Running', tone: 'ok' };
  return r.enabled ? { label: 'Starting…', tone: 'off' } : { label: 'Off', tone: 'off' };
}

export interface InterfaceOption {
  value: string;
  label: string;
}

/**
 * Choices for the local interface: all interfaces first, then every address of the host (IPv4 and
 * IPv6, one per interface), then `current` when it is not among them, so editing a rule never drops
 * its stored address.
 */
export function interfaceOptions(
  addresses: { interfaceName?: string; ipAddress: string }[],
  current?: string,
): InterfaceOption[] {
  const out: InterfaceOption[] = [{ value: ALL_INTERFACES, label: `All interfaces (${ALL_INTERFACES})` }];
  const seen = new Set<string>([ALL_INTERFACES]);
  for (const a of addresses) {
    const key = `${a.ipAddress}|${a.interfaceName ?? ''}`;
    if (seen.has(key)) continue;
    seen.add(key);
    // The same IP on two interfaces is one choice (the value is the IP); show the first name.
    if (out.some(o => o.value === a.ipAddress)) continue;
    out.push({ value: a.ipAddress, label: a.interfaceName ? `${a.interfaceName} — ${a.ipAddress}` : a.ipAddress });
  }
  if (current && !out.some(o => o.value === current)) out.push({ value: current, label: `${current} (saved)` });
  return out;
}
