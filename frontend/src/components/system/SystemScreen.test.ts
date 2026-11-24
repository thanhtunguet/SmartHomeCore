import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { SystemScreen } from './SystemScreen';

const { state } = vi.hoisted(() => ({
  state: {
    systemMetrics: {
      collectedAt: '2026-11-14T00:00:00Z',
      diskPath: '/a/very/long/storage/path',
      diskUsedBytes: 1024 * 1024,
      diskTotalBytes: 2 * 1024 * 1024,
      process: {}, localIps: [], localAddresses: [] as { interfaceName?: string; ipAddress: string }[], restricted: [],
    },
    refreshSystemMetrics: vi.fn(), serverHealth: null, connectionIssue: null,
  },
}));
vi.mock('../../context/AppContext', () => ({ useApp: () => state }));

describe('System disk display', () => {
  it('keeps the path in a tooltip while showing disk usage', () => {
    const html = renderToStaticMarkup(React.createElement(SystemScreen));
    expect(html).toContain('title="/a/very/long/storage/path" tabindex="0">Disk</span>');
    expect(html).not.toContain('Disk (/a/very/long/storage/path)');
    expect(html).toContain('50%');
    expect(html).toContain('Used: 1.0 MB');
    expect(html).toContain('Total: 2.0 MB');
  });
});


describe('System local addresses', () => {
  it('renders each interface alongside its IP and falls back when the name is unknown', () => {
    state.systemMetrics.localAddresses = [
      { interfaceName: 'en0', ipAddress: '192.168.1.2' },
      { interfaceName: 'eth0', ipAddress: '2001:db8::1' },
      { ipAddress: '10.0.0.2' },
    ];
    const html = renderToStaticMarkup(React.createElement(SystemScreen));
    expect(html).toMatch(/>en0<\/span><span[^>]*>192\.168\.1\.2<\/span>/);
    expect(html).toMatch(/>eth0<\/span><span[^>]*>2001:db8::1<\/span>/);
    expect(html).toMatch(/>—<\/span><span[^>]*>10\.0\.0\.2<\/span>/);
  });
});
