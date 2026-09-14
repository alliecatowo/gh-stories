import { expect, test, chromium, type BrowserContext, type Page } from '@playwright/test';
import { readFileSync, mkdirSync, mkdtempSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { tmpdir } from 'node:os';

/**
 * Real browser-extension scenarios.
 *
 * An MV3 extension cannot be loaded by a plain headless browser: it needs a
 * PERSISTENT Chromium context with --load-extension. Rendering the shared React
 * UI in an ordinary page would not exercise extension permissions, background
 * messaging, or GitHub injection, so it is not a substitute and is not used.
 *
 * github.com requests are fulfilled from a FICTIONAL, hand-written fixture. The
 * page URL stays https://github.com/... so the content script matches exactly as
 * it would in the wild, but no real page, account or private content is involved.
 */

const EXT = process.env.GHS_EXTENSION_PATH ??
  resolve(import.meta.dirname, '../../../apps/web-extension/.output/chrome-mv3');
const FIXTURES = resolve(import.meta.dirname, '../fixtures');
const SHOTS = resolve(import.meta.dirname, '../../../evidence/browser');

const prHTML = readFileSync(`${FIXTURES}/github-pr.html`, 'utf8');
const dashHTML = readFileSync(`${FIXTURES}/github-dashboard.html`, 'utf8');

type Account = { token: string; user: { login: string; avatar_url: string; github_user_id: number } };
type Session = { service: string; alice: Account; maya: Account };

/**
 * The fixture's avatar URLs carry placeholder numeric ids. The extension keys
 * off GitHub's authoritative NUMERIC id (logins are renameable), so the fixture
 * has to carry the ids the seeded accounts actually have — otherwise the batch
 * status lookup correctly answers "no Story" for accounts that do not exist,
 * and the test would be measuring the wrong thing.
 */
function withRealIds(html: string, s: Session): string {
  return html
    .replaceAll('u/1001', `u/${s.alice.user.github_user_id}`)
    .replaceAll('u/4242', `u/${s.maya.user.github_user_id}`);
}

function session(): Session {
  return JSON.parse(readFileSync('/tmp/ghs-e2e-session.json', 'utf8')) as Session;
}

async function shot(page: Page, name: string): Promise<void> {
  const file = `${SHOTS}/${name}.png`;
  mkdirSync(dirname(file), { recursive: true });
  await page.screenshot({ path: file, fullPage: false });
}

async function launch(): Promise<{ context: BrowserContext; extensionId: string }> {
  const userDataDir = mkdtempSync(`${tmpdir()}/ghs-ext-`);
  const context = await chromium.launchPersistentContext(userDataDir, {
    // The headless SHELL cannot load extensions at all. The full Chromium
    // build in new headless mode can, which is why the channel is explicit.
    channel: 'chromium',
    headless: true,
    args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`],
    colorScheme: 'dark',
    viewport: { width: 1280, height: 900 },
  });

  // The MV3 service worker's URL carries the extension id.
  let [worker] = context.serviceWorkers();
  if (!worker) worker = await context.waitForEvent('serviceworker', { timeout: 20_000 });
  const extensionId = new URL(worker.url()).host;

  // Fictional GitHub. The URL is real; the content is not.
  await context.route('https://github.com/**', async (route) => {
    const url = route.request().url();
    const body = withRealIds(url.includes('/pull/') ? prHTML : dashHTML, session());
    await route.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body });
  });
  // Avatars come from GitHub's CDN in the fixture; serve a deterministic pixel.
  await context.route('https://avatars.githubusercontent.com/**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" rx="32" fill="#6e7681"/></svg>',
    });
  });

  return { context, extensionId };
}

/** Signs the extension in by seeding exactly what a completed login stores. */
async function signIn(context: BrowserContext, extensionId: string): Promise<void> {
  const s = session();
  const page = await context.newPage();
  await page.goto(`chrome-extension://${extensionId}/options.html`);
  await page.evaluate(
    async ([service, token, login, avatar, id]) => {
      const accountId = String(id);
      await chrome.storage.local.set({
        'ghs:serviceOrigin': service,
        'ghs:activeAccountId': accountId,
        'ghs:accounts': {
          [accountId]: { accountId, login, avatarUrl: avatar, isModerator: false, token },
        },
      });
    },
    [s.service, s.alice.token, s.alice.user.login, s.alice.user.avatar_url, s.alice.user.github_user_id] as const,
  );
  await page.close();
}

test.describe('browser extension', () => {
  test('@visual loads into a real browser and decorates GitHub avatars', async () => {
    const { context, extensionId } = await launch();
    try {
      await signIn(context, extensionId);
      const page = await context.newPage();
      await page.goto('https://github.com/octo-org/upload-worker/pull/482');

      // The extension injects its own shadow hosts; wait for one to appear.
      // Each ring is its own Shadow DOM host, slotted beside the avatar.
      const host = page.locator('ghs-ring-overlay').first();
      await expect(host).toBeAttached({ timeout: 20_000 });

      // At least one eligible account must actually be decorated.
      await expect(page.locator('ghs-ring-overlay')).not.toHaveCount(0);

      // maya-devs posted a public Story, so their avatar must carry an
      // UNSEEN ring with an activation badge — not merely a decorated slot.
      await expect
        .poll(async () =>
          page.evaluate(() => {
            const states: string[] = [];
            document.querySelectorAll('ghs-ring-overlay').forEach((host) => {
              const frame = host.shadowRoot?.querySelector('[data-state]');
              const st = frame?.getAttribute('data-state');
              if (st) states.push(st);
            });
            return states;
          }), { timeout: 15_000 })
        .toContain('unseen');

      const badges = await page.evaluate(() => {
        let n = 0;
        document.querySelectorAll('ghs-ring-overlay').forEach((h) => {
          if (h.shadowRoot?.querySelector('button')) n++;
        });
        return n;
      });
      expect(badges, 'an account with a Story needs an activation control').toBeGreaterThan(0);

      // And decoration must not HIDE the person. The ring is an overlay; the
      // real GitHub avatar has to remain visible through its hollow centre.
      const hidden = await page.evaluate(() => {
        const bad: string[] = [];
        document.querySelectorAll('.ghs-avatar-slot img').forEach((img) => {
          const el = img as HTMLElement;
          const box = el.getBoundingClientRect();
          const cs = getComputedStyle(el);
          if (box.width < 8 || box.height < 8 || cs.visibility === 'hidden' || cs.opacity === '0') {
            bad.push(`${el.getAttribute('alt')} ${box.width}x${box.height} ${cs.visibility}`);
          }
        });
        return bad;
      });
      expect(hidden, `decorated avatars must stay visible: ${hidden.join(', ')}`).toHaveLength(0);

      // Clicking the avatar must still reach GitHub's own profile link: the
      // ring is decoration, and the overlay must not intercept the hit test.
      const hit = await page.evaluate(() => {
        const slot = document.querySelector('.ghs-avatar-slot');
        if (!slot) return 'no slot';
        const b = slot.getBoundingClientRect();
        const top = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
        if (!top) return 'none';
        return top.closest('a')?.getAttribute('href') ?? top.tagName.toLowerCase();
      });
      expect(hit, `the ring overlay is swallowing clicks (hit ${hit})`).toMatch(/^\//);

      await shot(page, 'extension-pr-rings');

      // It must never decorate a bot or an app account.
      // Scoped to the bot's OWN avatar: its container also holds the human
      // reviewers, so looking at the parent would find their rings.
      const botDecorated = await page.evaluate(() => {
        const botImg = document.querySelector('a[href="/apps/dependabot"] img');
        if (!botImg) return false;
        return botImg.closest('.ghs-avatar-slot') != null;
      });
      expect(botDecorated, 'a bot/app avatar must never get a Story ring').toBe(false);
    } finally {
      await context.close();
    }
  });

  test('leaves ordinary GitHub links and layout alone', async () => {
    const { context, extensionId } = await launch();
    try {
      const page = await context.newPage();

      // Measure the page BEFORE the extension has decorated anything.
      await page.goto('https://github.com/octo-org/upload-worker/pull/482');
      const before = await page.evaluate(() => {
        const el = document.querySelector('#late-comment-anchor');
        return el ? Math.round(el.getBoundingClientRect().top) : -1;
      });

      await signIn(context, extensionId);
      await page.reload();
      await page.waitForTimeout(3000);

      const after = await page.evaluate(() => {
        const el = document.querySelector('#late-comment-anchor');
        return el ? Math.round(el.getBoundingClientRect().top) : -1;
      });
      // Rings reserve their width whether or not one is shown, so comment
      // layout must not move.
      expect(Math.abs(after - before), 'decorating avatars must not shift layout').toBeLessThanOrEqual(2);

      // The username link still navigates normally.
      const href = await page.locator('a.author').first().getAttribute('href');
      expect(href).toBe('/maya-devs');

      // No invalid nested interactive elements were created.
      const nested = await page.evaluate(() =>
        document.querySelectorAll('a button, button a, a a').length);
      expect(nested, 'must not create nested interactive elements').toBe(0);
    } finally {
      await context.close();
    }
  });

  test('handles lazily inserted comments without duplicating rings', async () => {
    const { context, extensionId } = await launch();
    try {
      await signIn(context, extensionId);
      const page = await context.newPage();
      await page.goto('https://github.com/octo-org/upload-worker/pull/482');
      await page.waitForTimeout(2500);
      const first = await page.locator('ghs-ring-overlay').count();

      // A comment arrives after load, and a Turbo navigation fires.
      const s = session();
      await page.evaluate((id) => window.__insertLateComment('alice', id),
        s.alice.user.github_user_id);
      await page.evaluate(() => window.__fireTurboNavigation());
      await page.waitForTimeout(2500);

      const second = await page.locator('ghs-ring-overlay').count();
      expect(second, 'a late comment should be decorated').toBeGreaterThanOrEqual(first);

      // Re-running the observer must not double-decorate anything.
      // Each decorated avatar sits inside exactly one slot with exactly one
      // ring overlay. More than one means the observer decorated it twice.
      const duplicates = await page.evaluate(() => {
        let dupes = 0;
        document.querySelectorAll('.ghs-avatar-slot').forEach((slot) => {
          if (slot.querySelectorAll('ghs-ring-overlay').length > 1) dupes++;
          if (slot.querySelectorAll('img.avatar').length > 1) dupes++;
        });
        return dupes;
      });
      expect(duplicates, 'avatars must never be decorated twice').toBe(0);
    } finally {
      await context.close();
    }
  });

  test('@visual the toolbar popup works independently of the page integration', async () => {
    const { context, extensionId } = await launch();
    try {
      await signIn(context, extensionId);
      const page = await context.newPage();
      // No github.com tab at all: the popup must still work.
      await page.goto(`chrome-extension://${extensionId}/popup.html`);
      await expect(page.locator('body')).toContainText(/Stories|Post|Inbox|Settings/i, {
        timeout: 15_000,
      });
      await shot(page, 'extension-popup');
    } finally {
      await context.close();
    }
  });

  test('GitHub stays usable when the service is unreachable', async () => {
    const { context, extensionId } = await launch();
    try {
      await signIn(context, extensionId);
      const s = session();
      // Every call to the service fails.
      await context.route(`${s.service}/**`, (route) => route.abort('failed'));

      const page = await context.newPage();
      const errors: string[] = [];
      page.on('pageerror', (e) => errors.push(e.message));
      await page.goto('https://github.com/octo-org/upload-worker/pull/482');
      await page.waitForTimeout(3000);

      // The page itself must be intact and interactive.
      await expect(page.locator('h1')).toBeVisible();
      expect(await page.locator('a.author').getAttribute('href')).toBe('/maya-devs');
      expect(errors, `an API outage must not throw into the page:\n${errors.join('\n')}`).toHaveLength(0);
    } finally {
      await context.close();
    }
  });
});
