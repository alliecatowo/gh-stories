/**
 * Seeds the local service with fictional accounts and one Story, and writes
 * the resulting session tokens for the extension test to pick up.
 *
 * Uses the demo-only sign-in route, which exists solely in a `ghs_testidp`
 * build and is refused outside local/test. It replaces ONLY the external
 * GitHub identity boundary — every authorization rule under test is the real
 * one.
 */
import { writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';

const SERVICE = process.env.GHS_SERVICE_URL ?? 'http://localhost:8787';
const ROOT = resolve(import.meta.dirname, '../..');

async function testLogin(login) {
  const res = await fetch(`${SERVICE}/v1/auth/test/login`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ login }),
  });
  if (!res.ok) throw new Error(`test login failed for ${login}: ${res.status}`);
  return res.json();
}

const alice = await testLogin('alice');
const maya = await testLogin('maya-devs');

// maya-devs posts a public Story through the real CLI, so the extension is
// looking at something the real posting path produced.
const home = '/tmp/ghs-e2e-maya';
execFileSync('rm', ['-rf', home]);
execFileSync('mkdir', ['-p', `${home}/gh-stories`]);
writeFileSync(
  `${home}/gh-stories/credentials.json`,
  JSON.stringify({
    [SERVICE]: {
      service_url: SERVICE,
      token: maya.token,
      login: 'maya-devs',
      user_id: maya.user.github_user_id,
      stored_at: new Date().toISOString(),
    },
  }),
  { mode: 0o600 },
);

execFileSync(resolve(ROOT, 'gh-stories'), [
  'post', resolve(ROOT, 'tests/fixtures/samples/sunset.jpg'),
  '--caption', 'golden hour from the office roof',
  '--description', 'A sunset over a distant ridge',
  '--audience', 'public',
], {
  env: { ...process.env, XDG_CONFIG_HOME: home, GHS_CREDENTIAL_STORE: 'file', GHS_SERVICE_URL: SERVICE },
  stdio: 'inherit',
});

writeFileSync(
  '/tmp/ghs-e2e-session.json',
  JSON.stringify({ service: SERVICE, alice, maya }, null, 2),
);
console.log('seeded: alice (extension user) and maya-devs (posted a public Story)');
