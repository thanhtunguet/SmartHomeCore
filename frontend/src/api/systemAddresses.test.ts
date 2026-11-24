import { describe, expect, it } from 'vitest';
import { toSystemMetrics } from './mappers';

describe('system local addresses', () => {
  const base = { collected_at: '2026-11-14T00:00:00Z', platform: 'linux/amd64' };
  it('preserves the interface for each IP, including duplicate IPs', () => {
    const m = toSystemMetrics({ ...base, local_ips: ['10.0.0.2', '10.0.0.2'], local_addresses: [
      { interface_name: 'eth0', ip_address: '10.0.0.2' },
      { interface_name: 'wlan0', ip_address: '10.0.0.2' },
    ] });
    expect(m.localAddresses).toEqual([{ interfaceName: 'eth0', ipAddress: '10.0.0.2' }, { interfaceName: 'wlan0', ipAddress: '10.0.0.2' }]);
  });
  it('retains IPs without inventing interface names for older Controllers', () => {
    expect(toSystemMetrics({ ...base, local_ips: ['10.0.0.2'] }).localAddresses).toEqual([{ ipAddress: '10.0.0.2' }]);
    expect(toSystemMetrics(base).localAddresses).toEqual([]);
  });
});
