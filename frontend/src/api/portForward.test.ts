import { describe, expect, it } from 'vitest';
import { draftFromRule, emptyDraft, interfaceOptions, isIP, statusState, validateDraft } from './portForward';
import type { PortForward } from './schema';

const draft = (over: Partial<ReturnType<typeof emptyDraft>> = {}) => ({
  ...emptyDraft(), name: 'NAS web', listenPort: '8080', targetHost: '192.168.1.20', targetPort: '80', ...over,
});

describe('emptyDraft', () => {
  it('listens on all interfaces, TCP, enabled', () => {
    expect(emptyDraft()).toMatchObject({ listenAddress: '0.0.0.0', protocol: 'tcp', enabled: true });
  });
});

describe('isIP', () => {
  it('accepts IPv4 and IPv6 literals and refuses the rest', () => {
    for (const ip of ['0.0.0.0', '192.168.1.10', '::', '::1', 'fe80::1', '2001:db8::8a2e:370:7334', '::ffff:10.0.0.1']) expect(isIP(ip), ip).toBe(true);
    for (const ip of ['', '256.1.1.1', '1.2.3', '01.2.3.4', 'host', ':::', '1::2::3', '12345::1', '1:2:3:4:5:6:7:8:9']) expect(isIP(ip), ip).toBe(false);
  });
});

describe('validateDraft', () => {
  it('accepts a complete draft, trims and converts the ports', () => {
    expect(validateDraft(draft({ name: ' NAS web ', listenPort: ' 8080 ', targetHost: ' nas.local ' }))).toEqual({
      value: { name: 'NAS web', protocol: 'tcp', listenAddress: '0.0.0.0', listenPort: 8080, targetHost: 'nas.local', targetPort: 80, enabled: true },
    });
  });

  it('a blank interface means all interfaces', () => {
    expect(validateDraft(draft({ listenAddress: ' ' }))).toMatchObject({ value: { listenAddress: '0.0.0.0' } });
  });

  it('refuses an empty, control-character or too long name', () => {
    for (const name of ['   ', 'a\nb', 'x'.repeat(101)]) expect(validateDraft(draft({ name })), name).toHaveProperty('error');
  });

  it('refuses ports outside 1..65535 and non-integers', () => {
    for (const p of ['', '0', '65536', '-1', '80.5', 'http', '1e3']) {
      expect(validateDraft(draft({ listenPort: p })), `listen ${p}`).toHaveProperty('error');
      expect(validateDraft(draft({ targetPort: p })), `target ${p}`).toHaveProperty('error');
    }
    expect(validateDraft(draft({ listenPort: '65535', targetPort: '1' }))).toHaveProperty('value');
  });

  it('refuses a bad local interface', () => {
    for (const a of ['nas.local', '300.1.1.1', 'eth0']) expect(validateDraft(draft({ listenAddress: a })), a).toHaveProperty('error');
    expect(validateDraft(draft({ listenAddress: 'fe80::1' }))).toHaveProperty('value');
  });

  it('refuses a target host with spaces, slashes or nothing', () => {
    for (const h of ['', 'a b', 'http://nas', 'nas/x', '-bad.example', 'x'.repeat(254)]) expect(validateDraft(draft({ targetHost: h })), h).toHaveProperty('error');
    for (const h of ['nas', 'nas.local', 'my_host.example.com', '10.0.0.5', '2001:db8::1']) expect(validateDraft(draft({ targetHost: h })), h).toHaveProperty('value');
  });

  it('refuses a rule that forwards a port to itself', () => {
    expect(validateDraft(draft({ listenPort: '80', targetPort: '80', targetHost: 'localhost' }))).toHaveProperty('error');
    expect(validateDraft(draft({ listenPort: '80', targetPort: '80', targetHost: '127.0.0.1', listenAddress: '127.0.0.1' }))).toHaveProperty('error');
    expect(validateDraft(draft({ listenPort: '80', targetPort: '80', targetHost: '10.0.0.5', listenAddress: '10.0.0.5' }))).toHaveProperty('error');
    // other port, or another machine on the same port: fine
    expect(validateDraft(draft({ listenPort: '80', targetPort: '8080', targetHost: 'localhost' }))).toHaveProperty('value');
    expect(validateDraft(draft({ listenPort: '80', targetPort: '80', targetHost: '10.0.0.9' }))).toHaveProperty('value');
    expect(validateDraft(draft({ listenPort: '80', targetPort: '80', targetHost: '10.0.0.9', listenAddress: '10.0.0.5' }))).toHaveProperty('value');
  });
});

describe('draftFromRule', () => {
  it('fills the form from the rule', () => {
    const r = { name: 'A', protocol: 'udp', listen_address: '10.0.0.1', listen_port: 53, target_host: 'dns', target_port: 5353, enabled: false } as PortForward;
    expect(draftFromRule(r)).toEqual({ name: 'A', protocol: 'udp', listenAddress: '10.0.0.1', listenPort: '53', targetHost: 'dns', targetPort: '5353', enabled: false });
  });
});

describe('statusState', () => {
  it('maps the runtime status to a label and tone', () => {
    expect(statusState({ enabled: true, status: 'running' })).toEqual({ label: 'Running', tone: 'ok' });
    expect(statusState({ enabled: false, status: 'stopped' })).toEqual({ label: 'Off', tone: 'off' });
    expect(statusState({ enabled: true, status: 'stopped' }).label).toBe('Starting…');
    expect(statusState({ enabled: true, status: 'error', last_error: 'address already in use' })).toEqual({
      label: 'Error', tone: 'warn', detail: 'address already in use',
    });
  });
});

describe('interfaceOptions', () => {
  it('lists all interfaces first, then each address with its interface name', () => {
    expect(interfaceOptions([{ interfaceName: 'eth0', ipAddress: '10.0.0.2' }, { interfaceName: 'eth0', ipAddress: 'fd00::2' }])).toEqual([
      { value: '0.0.0.0', label: 'All interfaces (0.0.0.0)' },
      { value: '10.0.0.2', label: 'eth0 — 10.0.0.2' },
      { value: 'fd00::2', label: 'eth0 — fd00::2' },
    ]);
  });
  it('dedupes an address and tolerates a missing interface name', () => {
    const o = interfaceOptions([{ interfaceName: 'eth0', ipAddress: '10.0.0.2' }, { interfaceName: 'wlan0', ipAddress: '10.0.0.2' }, { ipAddress: '10.0.0.3' }]);
    expect(o.map(x => x.value)).toEqual(['0.0.0.0', '10.0.0.2', '10.0.0.3']);
    expect(o[2].label).toBe('10.0.0.3');
  });
  it('keeps the stored address of a rule that is no longer a local address', () => {
    expect(interfaceOptions([], '192.168.9.9').map(x => x.value)).toEqual(['0.0.0.0', '192.168.9.9']);
    expect(interfaceOptions([], '0.0.0.0')).toHaveLength(1);
  });
});
