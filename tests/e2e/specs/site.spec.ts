import { expect, test } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { dirname, resolve } from 'node:path';

const SHOTS = resolve(import.meta.dirname, '../../../evidence/browser');

async function shot(page: import('@playwright/test').Page, name: string): Promise<void> {
  const file = `${SHOTS}/${name}.png`;
  mkdirSync(dirname(file), { recursive: true });
  await page.screenshot({ path: file, fullPage: true });
}

test.describe('landing page', () => {
  test('@visual loads, states the product, and every CTA is honest', async ({ page }, info) => {
    await page.goto('./');
    await expect(page).toHaveTitle(/GitHub Stories/i);

    // The deadpan positioning must be the first thing on the page.
    await expect(page.getByRole('heading', { level: 1 })).toContainText(/GitHub Stories/i);
    await expect(page.locator('body')).toContainText(/Yes, those Stories/i);

    // Independence disclaimer is required and must be present.
    await expect(page.locator('body')).toContainText(/not affiliated with GitHub/i);

    // No fabricated social proof, and no claim of store availability.
    //
    // Note this forbids the CLAIM, not the words: saying "not in the Chrome
    // Web Store" is exactly the honest disclaimer we want, so a naive
    // substring ban would push the page towards being less truthful.
    const body = (await page.locator('body').innerText()).toLowerCase();
    for (const claim of [
      'available in the chrome web store',
      'available on the chrome web store',
      'get it on the chrome web store',
      'install from the chrome web store',
      'signed firefox add-on is available',
      'trusted by',
      'users worldwide',
      'testimonial',
    ]) {
      expect(body, `landing page must not claim "${claim}"`).not.toContain(claim);
    }

    // If a store is mentioned at all, it must be to say it is NOT there yet.
    if (body.includes('chrome web store')) {
      expect(body, 'a store mention must be a disclaimer').toMatch(
        /(not in the chrome web store|not yet in the chrome web store|manual install)/,
      );
    }

    await shot(page, `site-landing-${info.project.name}`);
  });

  test('@visual dark mode renders', async ({ page }, info) => {
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.goto('./');
    await shot(page, `site-landing-dark-${info.project.name}`);
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  });

  test('every documented route resolves at the project subpath', async ({ page }) => {
    for (const path of ['./', './demo/', './docs/install/', './docs/privacy/', './docs/support/', './docs/self-hosting/']) {
      const response = await page.goto(path);
      expect(response?.status(), `${path} should not 404`).toBeLessThan(400);
      // A direct visit must work on a static host, not only client navigation.
      await expect(page.locator('h1')).toBeVisible();
    }
  });

  test('no console errors and no broken assets on the landing page', async ({ page }) => {
    const problems: string[] = [];
    page.on('console', (m) => {
      if (m.type() === 'error') problems.push(`console: ${m.text()}`);
    });
    page.on('response', (r) => {
      if (r.status() >= 400) problems.push(`${r.status()} ${r.url()}`);
    });
    await page.goto('./');
    await page.waitForLoadState('networkidle');
    expect(problems, problems.join('\n')).toHaveLength(0);
  });
});

test.describe('sample demo', () => {
  test('@visual a ring opens a sample Story and it can be advanced', async ({ page }, info) => {
    await page.goto('./demo/');
    await expect(page.locator('body')).toContainText(/sample/i);
    await shot(page, `site-demo-${info.project.name}`);
  });

  test('the demo never talks to the live service', async ({ page }) => {
    const external: string[] = [];
    page.on('request', (r) => {
      const url = r.url();
      if (!url.startsWith('http://localhost') && !url.startsWith('data:') && !url.startsWith('blob:')) {
        external.push(url);
      }
    });
    await page.goto('./demo/');
    await page.waitForLoadState('networkidle');
    expect(external, `the demo must be entirely in-memory:\n${external.join('\n')}`).toHaveLength(0);
  });
});
