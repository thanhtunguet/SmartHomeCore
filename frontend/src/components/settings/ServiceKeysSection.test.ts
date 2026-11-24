import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ServiceKeysSection } from './ServiceKeysSection';

// The tests run in Node without a DOM, so this renders the first paint only (before the list
// loads): enough to catch a crash and to pin what the create form offers. The one-time key panel
// is state-driven and is covered by the manual check listed in the changelog.
describe('ServiceKeysSection first render', () => {
  const html = renderToStaticMarkup(createElement(ServiceKeysSection));

  it('shows the heading, the loading state and no key panel', () => {
    expect(html).toContain('Service Account Keys');
    expect(html).toContain('Loading…');
    expect(html).not.toContain('new-key');
    expect(html).not.toContain('Copy this key now');
  });

  it('offers a name, a lifetime defaulting to one year with presets, both scopes (mcp first) and a submit button', () => {
    expect(html).toContain('placeholder="e.g. claude mcp"');
    expect(html).toContain('value="365"');
    for (const preset of ['30 days', '90 days', '1 year', '5 years']) expect(html).toContain(preset);
    expect(html.indexOf('AI agent (mcp)')).toBeGreaterThan(-1);
    expect(html.indexOf('AI agent (mcp)')).toBeLessThan(html.indexOf('Legacy app (legacy)'));
    expect(html).toMatch(/name="key-scope" checked="" value="mcp"/); // mcp is the preselected scope
    expect(html).not.toMatch(/name="key-scope" checked="" value="legacy"/);
    expect(html).not.toContain('w-full px-3 py-2 bg-neutral-50 dark:bg-neutral-800 border border-neutral-200 dark:border-neutral-700 rounded-lg text-neutral-900 dark:text-neutral-100 text-xs w-28'); // no conflicting widths
    expect(html).toContain('Create key');
  });

  it('says that rotation is done by the agent, not from this screen', () => {
    expect(html).toContain('/api/v1/auth/service-key/rotate');
    expect(html).not.toContain('>Rotate<');
  });
});
