import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig, loadEnv} from 'vite';

/**
 * Dev: `npm run dev` serves the UI on :3000 and proxies the API to a running Controller
 * (`make run` in the SmartHomeCore repo, default http://127.0.0.1:8080), so the browser sees one
 * origin and the Controller's same-origin CSP / CORS-off defaults keep working.
 * Prod: `make ui-build` in SmartHomeCore builds into web/static, which is embedded in the binary.
 */
export default defineConfig(({mode}) => {
  const env = loadEnv(mode, process.cwd(), '');
  const controller = env.HOMECORE_API_URL || 'http://127.0.0.1:8080';
  const proxy = Object.fromEntries(['/api', '/healthz', '/readyz'].map(p => [p, {target: controller}]));
  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, '.'),
      },
    },
    build: {
      // The Controller's CSP has no `font-src data:`: never inline fonts as data: URIs.
      assetsInlineLimit: 0,
    },
    server: {proxy},
    preview: {proxy},
  };
});
