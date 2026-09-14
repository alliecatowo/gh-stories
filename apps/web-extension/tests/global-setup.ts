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
 * Builds any missing browser target before the manifest tests run.
 *
 * The suite validates BUILT manifests, so it must not depend on a build step
 * having happened earlier in some pipeline — running the tests before the
 * build is a perfectly reasonable thing to do, and it should just work.
 */
export default function setup(): void {
  for (const [browser, dir] of TARGETS) {
    if (existsSync(resolve(ROOT, '.output', dir, 'manifest.json'))) continue;
    execFileSync('npx', ['wxt', 'build', '-b', browser], {
      cwd: ROOT,
      stdio: 'inherit',
      env: process.env,
    });
  }
}
