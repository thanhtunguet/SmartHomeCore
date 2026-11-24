import { describe, expect, it } from 'vitest';
import { legacyDeviceRef, legacyEndpoints } from './legacy';

describe('legacy endpoint URLs', () => {
  it('uses the device id when it is an integer', () => {
    expect(legacyDeviceRef({ id: '12', name: 'Gaming PC' })).toBe('12');
  });

  it('uses the device name when the id is not an integer (UUIDs, as issued by the Controller)', () => {
    expect(legacyDeviceRef({ id: '0193a0b2-7c4e-7f00-8000-000000000001', name: 'Gaming PC' })).toBe('Gaming PC');
    expect(legacyDeviceRef({ id: '12a', name: 'x' })).toBe('x');
    expect(legacyDeviceRef({ id: '-1', name: 'x' })).toBe('x');
    expect(legacyDeviceRef({ id: '', name: 'x' })).toBe('x');
  });

  it('builds the three GET endpoint paths (no origin) with the device percent-encoded', () => {
    const urls = legacyEndpoints({ id: '0193a0b2', name: 'Living room & TV/1' });
    expect(urls.map(u => u.key)).toEqual(['turnOn', 'turnOff', 'isOnline']);
    expect(urls.map(u => u.path)).toEqual([
      '/api/turn-on?device=Living%20room%20%26%20TV%2F1',
      '/api/turn-off?device=Living%20room%20%26%20TV%2F1',
      '/api/is-online?device=Living%20room%20%26%20TV%2F1'
    ]);
  });

  it('puts the integer id in the URL unencoded', () => {
    expect(legacyEndpoints({ id: '7', name: 'pc' })[0].path).toBe('/api/turn-on?device=7');
  });
});
