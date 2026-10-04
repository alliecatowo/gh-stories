import { defineConfig } from 'wxt';

// Production host permissions are limited to GitHub plus the configured
// service and media origins. There is deliberately no <all_urls>, and no
// broad browsing-history access: the extension only needs to see github.com
// pages and talk to its own service.
// Node's process is available in this build-time config file.
declare const process: { env: Record<string, string | undefined> };

// The service origin the built extension is allowed to talk to.
//
// Host permissions are baked into the manifest, so pointing the extension at a
// different service needs a rebuild with GHS_SERVICE_ORIGIN set. The runtime
// service URL is separately overridable from the options page; the rebuild is
// only ever needed to WIDEN permissions, which is the conservative direction.
// End-to-end tests build with a localhost origin for exactly this reason.
//
// One value drives everything: the manifest host permission AND the runtime
// default (injected below as __GHS_SERVICE_ORIGIN__). It is the same Cloud Run
// origin the release workflow passes to the CLI as GHS_DEFAULT_SERVICE_URL.
const PRODUCTION_ORIGIN = 'https://gh-stories-api-706402477894.us-central1.run.app';
const SERVICE_ORIGIN = new URL(
  process.env.GHS_SERVICE_ORIGIN || process.env.GHS_DEFAULT_SERVICE_URL || PRODUCTION_ORIGIN,
).origin;

// Release builds export GHS_VERSION=<tag>; the manifest version must track it
// (stores require a monotonic version). Local builds keep the package version.
const MANIFEST_VERSION =
  /^v?(\d+\.\d+\.\d+)/.exec(process.env.GHS_VERSION ?? '')?.[1] ?? '0.1.0';

export default defineConfig({
  modules: ['@wxt-dev/module-react'],
  srcDir: '.',
  outDir: '.output',
  manifest: ({ browser }) => ({
    name: 'GitHub Stories',
    description:
      'Stories for GitHub. Photos and videos on the avatars you already know, gone in 24 hours.',
    version: MANIFEST_VERSION,
    // Only what is actually used:
    //   storage  – the session token and per-account caches, in local storage
    //              only (never sync, which would copy it across devices)
    //   alarms   – an MV3 service worker is killed aggressively, so the
    //              bounded login poll needs an alarm to survive being evicted
    //              mid-login. Without this permission the worker throws during
    //              init and never registers its message listener at all.
    permissions: ['storage', 'alarms'],
    host_permissions: ['https://github.com/*', `${SERVICE_ORIGIN}/*`],
    action: {
      default_title: 'GitHub Stories',
      default_popup: 'popup.html',
    },
    options_ui: { page: 'options.html', open_in_tab: true },
    icons: {
      16: '/icon/16.png',
      32: '/icon/32.png',
      48: '/icon/48.png',
      128: '/icon/128.png',
    },
    // No remote code: everything executable ships in the package.
    content_security_policy: {
      extension_pages: "script-src 'self'; object-src 'none'",
    },
    ...(browser === 'firefox'
      ? {
          browser_specific_settings: {
            gecko: {
              id: 'gh-stories@alliecatowo.github.io',
              strict_min_version: '128.0',
            },
          },
        }
      : {}),
  }),
  vite: () => ({
    build: { sourcemap: false },
    define: { __GHS_SERVICE_ORIGIN__: JSON.stringify(SERVICE_ORIGIN) },
  }),
});
