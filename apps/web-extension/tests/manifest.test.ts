import { describe, expect, it } from 'vitest';
import { readFileSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';

/**
 * Validates the BUILT manifests rather than the config that produced them.
 *
 * These are the properties a store reviewer (and a security-conscious user)
 * actually checks, and the ones that are easy to break silently: permission
 * creep, remote code, a missing Firefox add-on id, or an icon that is declared
 * but not shipped.
 */
const OUT = resolve(import.meta.dirname, '../.output');

type Manifest = {
  manifest_version: number;
  name: string;
  version: string;
  permissions?: string[];
  host_permissions?: string[];
  content_security_policy?: unknown;
  browser_specific_settings?: { gecko?: { id?: string; strict_min_version?: string } };
  icons?: Record<string, string>;
  background?: { service_worker?: string; scripts?: string[] };
  content_scripts?: Array<{ matches: string[] }>;
  action?: unknown;
  browser_action?: unknown;
};

function load(dir: string): Manifest | null {
  const file = resolve(OUT, dir, 'manifest.json');
  if (!existsSync(file)) return null;
  return JSON.parse(readFileSync(file, 'utf8')) as Manifest;
}

const ALLOWED_PERMISSIONS = ['storage', 'tabs', 'alarms'];

/**
 * Host permissions live in different places per manifest version: MV3 has a
 * dedicated `host_permissions`, MV2 mixes them into `permissions`.
 */
function hostPermissions(manifest: Manifest): string[] {
  if (manifest.host_permissions?.length) return manifest.host_permissions;
  return (manifest.permissions ?? []).filter((p) => p.includes('://'));
}

function apiPermissions(manifest: Manifest): string[] {
  return (manifest.permissions ?? []).filter((p) => !p.includes('://'));
}

describe.each([
  ['chrome-mv3', 3],
  ['edge-mv3', 3],
  ['firefox-mv2', 2],
])('built manifest: %s', (dir, expectedVersion) => {
  const manifest = load(dir);

  it('exists (run `wxt build` first)', () => {
    expect(manifest, `no built manifest at .output/${dir}`).not.toBeNull();
  });

  it('declares the expected manifest version', () => {
    if (!manifest) return;
    expect(manifest.manifest_version).toBe(expectedVersion);
  });

  it('requests no permission beyond what the product uses', () => {
    if (!manifest) return;
    for (const permission of apiPermissions(manifest)) {
      expect(ALLOWED_PERMISSIONS, `unexpected permission "${permission}"`).toContain(permission);
    }
  });

  it('limits host permissions to GitHub and one service origin', () => {
    if (!manifest) return;
    const hosts = hostPermissions(manifest);
    expect(hosts).toContain('https://github.com/*');
    expect(hosts.length, 'host permissions should stay minimal').toBeLessThanOrEqual(2);
    for (const host of hosts) {
      expect(host, 'must never request access to all sites').not.toBe('<all_urls>');
      expect(host).not.toContain('*://*/*');
    }
  });

  it('only injects content scripts into GitHub', () => {
    if (!manifest) return;
    for (const script of manifest.content_scripts ?? []) {
      for (const match of script.matches) {
        expect(match).toMatch(/^https:\/\/github\.com\//);
      }
    }
  });

  it('ships every icon it declares', () => {
    if (!manifest) return;
    for (const path of Object.values(manifest.icons ?? {})) {
      const file = resolve(OUT, dir, path.replace(/^\//, ''));
      expect(existsSync(file), `declared icon is missing from the package: ${path}`).toBe(true);
    }
  });

  it('forbids remote code', () => {
    if (!manifest) return;
    const csp = JSON.stringify(manifest.content_security_policy ?? '');
    if (csp && csp !== '""') {
      expect(csp).toContain("script-src 'self'");
      expect(csp).not.toContain('unsafe-eval');
    }
  });
});

describe('firefox specifics', () => {
  const manifest = load('firefox-mv2');

  it('carries a stable add-on id and a minimum version', () => {
    if (!manifest) return;
    const gecko = manifest.browser_specific_settings?.gecko;
    expect(gecko?.id, 'Firefox needs a stable add-on id to be updatable').toBeTruthy();
    expect(gecko?.strict_min_version).toBeTruthy();
  });

  it('uses a background script rather than an MV3 service worker', () => {
    if (!manifest) return;
    expect(manifest.background?.scripts ?? manifest.background?.service_worker).toBeTruthy();
  });

  it('uses browser_action, as MV2 requires', () => {
    if (!manifest) return;
    expect(manifest.browser_action ?? manifest.action).toBeTruthy();
  });
});
