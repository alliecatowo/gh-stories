/**
 * Builds the extension with the test service in its host permissions, then
 * seeds the service.
 *
 * Host permissions are baked into the manifest, so an extension built for the
 * production origin simply cannot reach a local service — the rings stay
 * blank and every scenario times out. This makes the suite self-contained
 * rather than depending on someone having exported the right variable.
 */
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';

const ROOT = resolve(import.meta.dirname, '../..');
const SERVICE = process.env.GHS_SERVICE_URL ?? 'http://localhost:8787';

export default async function globalSetup() {
  execFileSync('npx', ['wxt', 'build', '-b', 'chrome'], {
    cwd: resolve(ROOT, 'apps/web-extension'),
    stdio: 'inherit',
    env: { ...process.env, GHS_SERVICE_ORIGIN: `${SERVICE}/*` },
  });
  execFileSync('node', [resolve(ROOT, 'tests/e2e/extension.setup.mjs')], {
    cwd: ROOT,
    stdio: 'inherit',
    env: { ...process.env, GHS_SERVICE_URL: SERVICE },
  });
}
