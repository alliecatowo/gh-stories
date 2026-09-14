import { chromium } from '@playwright/test';
import { readFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';

const EXT = '/Users/allie/Develop/github-stories/apps/web-extension/.output/chrome-mv3';
const prHTML = readFileSync('/Users/allie/Develop/github-stories/tests/e2e/fixtures/github-pr.html', 'utf8');
const s = JSON.parse(readFileSync('/tmp/ghs-e2e-session.json', 'utf8'));

const ctx = await chromium.launchPersistentContext(mkdtempSync(`${tmpdir()}/ghs-dbg-`), {
  channel: 'chromium', headless: true,
  args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`],
});
let [w] = ctx.serviceWorkers();
if (!w) w = await ctx.waitForEvent('serviceworker');
const id = new URL(w.url()).host;

await ctx.route('https://github.com/**', r =>
  r.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: prHTML }));
await ctx.route('https://avatars.githubusercontent.com/**', r =>
  r.fulfill({ status: 200, contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#888"/></svg>' }));

const opt = await ctx.newPage();
await opt.goto(`chrome-extension://${id}/options.html`);
await opt.evaluate(async ([svc, tok, login, av, uid]) => {
  const accountId = String(uid);
  await chrome.storage.local.set({
    'ghs:serviceOrigin': svc, 'ghs:activeAccountId': accountId,
    'ghs:accounts': { [accountId]: { accountId, login, avatarUrl: av, isModerator: false, token: tok } },
  });
}, [s.service, s.alice.token, s.alice.user.login, s.alice.user.avatar_url, s.alice.user.github_user_id]);
await opt.close();

const page = await ctx.newPage();
const logs = [];
page.on('console', m => logs.push(`[page:${m.type()}] ${m.text()}`));
page.on('pageerror', e => logs.push(`[pageerror] ${e.message}`));
w.on('console', m => logs.push(`[sw:${m.type()}] ${m.text()}`));

await page.goto('https://github.com/octo-org/upload-worker/pull/482');
await page.waitForTimeout(6000);

console.log('ring states:', JSON.stringify(await page.evaluate(() => {
  const out = [];
  document.querySelectorAll('ghs-ring-overlay').forEach((host) => {
    const sr = host.shadowRoot;
    const frame = sr?.querySelector('[class*="frame"]');
    const label = sr?.textContent?.trim().slice(0, 60);
    const slot = host.closest('.ghs-avatar-slot');
    const alt = slot?.querySelector('img')?.getAttribute('alt');
    out.push({
      alt,
      state: frame?.getAttribute('data-state'),
      hasBadge: !!sr?.querySelector('button'),
      label,
      frameBox: frame ? JSON.parse(JSON.stringify(frame.getBoundingClientRect())) : null,
    });
  });
  return out;
}), null, 1));
console.log('injected elements:', JSON.stringify(await page.evaluate(() => {
  const out = [];
  document.querySelectorAll('*').forEach((el) => {
    const tag = el.tagName.toLowerCase();
    const cls = typeof el.className === 'string' ? el.className : '';
    if (tag.includes('ghs') || cls.includes('ghs')) {
      out.push({ tag, cls, shadow: !!el.shadowRoot, parent: el.parentElement?.tagName.toLowerCase() });
    }
  });
  return out;
}), null, 1));
console.log('avatars found:', await page.evaluate(() => document.querySelectorAll('img.avatar').length));
console.log('--- logs ---');
console.log(logs.slice(0, 25).join('\n') || '(none)');

// Ask the background directly what it thinks.
await ctx.close();

// eslint-disable-next-line
async function unusedOpt2(ctx, id) {
  const p = await ctx.newPage();
  await p.goto(`chrome-extension://${id}/options.html`);
  const r = await p.evaluate(() => chrome.runtime.sendMessage({ type: 'ghs:session/get' }));
  await p.close(); return r;
}
async function unusedOpt3(ctx, id) {
  const p = await ctx.newPage();
  await p.goto(`chrome-extension://${id}/options.html`);
  const r = await p.evaluate(() => chrome.runtime.sendMessage({
    type: 'ghs:ring/status', tabId: 0, logins: ['maya-devs', 'alice', 'sam'] }));
  await p.close(); return r;
}
