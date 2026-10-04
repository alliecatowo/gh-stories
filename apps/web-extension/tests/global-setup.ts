import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';

const ROOT = resolve(import.meta.dirname, '..');
const TARGETS = [
  ['chrome', 'chrome-mv3'],
  ['firefox', 'firefox-mv2'],
  ['edge', 'edge-mv3'],
] as const;

/**
 * Rebuilds every browser target before the manifest tests run.
 *
 * The suite validates BUILT manifests, so a stale `.output` from an earlier
 * build would let a manifest or bundle regression pass. Always rebuilding makes
 * the result depend only on the current source. Set GHS_REUSE_BUILD=1 to reuse
 * an existing build when iterating locally.
 */
export default function setup(): void {
  for (const [browser, dir] of TARGETS) {
    if (process.env.GHS_REUSE_BUILD === '1' && existsSync(resolve(ROOT, '.output', dir, 'manifest.json'))) continue;
    execFileSync('npx', ['wxt', 'build', '-b', browser], {
      cwd: ROOT,
      stdio: 'inherit',
      env: process.env,
    });
  }
}
