import { defineConfig } from 'wxt';

// Production host permissions are limited to GitHub plus the configured
// service and media origins. There is deliberately no <all_urls>, and no
// broad browsing-history access: the extension only needs to see github.com
// pages and talk to its own service.
// The live service the published extension talks to. Self-hosters change this
// one line and rebuild; the runtime service URL is also overridable from the
// options page, so a rebuild is only needed to widen host permissions.
const SERVICE_ORIGIN = 'https://ghstories.fly.dev/*';

export default defineConfig({
  modules: ['@wxt-dev/module-react'],
  srcDir: '.',
  outDir: '.output',
  manifest: ({ browser }) => ({
    name: 'GitHub Stories',
    description:
      'Stories for GitHub. Photos and videos on the avatars you already know, gone in 24 hours.',
    version: '0.1.0',
    // Only what is actually used:
    //   storage  – the session token and per-account caches, in local storage
    //              only (never sync, which would copy it across devices)
    //   tabs     – opening the authorization tab and detecting its completion
    permissions: ['storage', 'tabs'],
    host_permissions: ['https://github.com/*', SERVICE_ORIGIN],
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
  }),
});
