import {defineConfig} from 'vitest/config';

/**
 * Unit tests of the API layer run in plain Node (no jsdom, no Controller): `fetch` and
 * `localStorage` are stubbed per test (see src/test/helpers.ts).
 */
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
    restoreMocks: true,
    unstubGlobals: true,
  },
});
