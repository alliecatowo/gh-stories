import { defineConfig, devices } from '@playwright/test';

/**
 * Extension scenarios need a PERSISTENT Chromium context with
 * --load-extension; a plain headless browser cannot load an MV3 extension at
 * all. Those tests therefore create their own context (see fixtures.ts) rather
 * than using a project-level browser.
 */
export default defineConfig({
  testDir: './specs',
  fullyParallel: false,
  workers: 1,
  reporter: [['list'], ['html', { outputFolder: '../../evidence/browser/report', open: 'never' }]],
  outputDir: '../../evidence/browser/results',
  timeout: 60_000,
  expect: { timeout: 10_000, toHaveScreenshot: { maxDiffPixelRatio: 0.02 } },
  use: {
    // The trailing slash matters: Playwright resolves a goto() path against
    // this URL, and a leading-slash path would discard the /gh-stories project
    // subpath the site is actually served from.
    baseURL: process.env.GHS_SITE_URL ?? 'http://localhost:4321/gh-stories/',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  // Self-contained: the suite serves the built site itself, at the project
  // subpath, so it can run anywhere without a separate setup step.
  webServer: {
    command: 'node serve-site.mjs',
    url: 'http://localhost:4321/gh-stories/',
    reuseExistingServer: true,
    timeout: 30_000,
  },

  projects: [
    { name: 'site', use: { ...devices['Desktop Chrome'] } },
    // Chromium-based so a WebKit download is not required to check narrow
    // layout. Mobile Safari is therefore NOT covered here, and the support
    // matrix says so rather than implying it was tested.
    { name: 'site-mobile', use: { ...devices['Pixel 7'] } },
    // The narrowest width the brief asks for, explicitly.
    { name: 'site-360', use: { ...devices['Desktop Chrome'], viewport: { width: 360, height: 780 } } },
    { name: 'site-wide', use: { ...devices['Desktop Chrome'], viewport: { width: 1920, height: 1080 } } },
  ],
});
