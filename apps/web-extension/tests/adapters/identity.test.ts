import { describe, expect, it } from 'vitest';
import { collectIdentities, extractIdentity } from '../../src/adapters/identity.js';
import { ALL_AVATAR_SURFACES_SELECTOR } from '../../src/adapters/index.js';
import { commentTimeline, withBotAndOrg } from '../fixtures/github.js';

function dom(html: string): Document {
  document.body.innerHTML = html;
  return document;
}

describe('identity extraction', () => {
  it('reads the login and the authoritative numeric id from an avatar', () => {
    const doc = dom(commentTimeline);
    const img = doc.querySelector('img.avatar') as HTMLImageElement;
    const identity = extractIdentity(img);
    expect(identity).not.toBeNull();
    expect(identity?.login).toBe('maya');
    // GitHub's numeric id is the authoritative key; logins are renameable.
    expect(identity?.githubUserId).toBe(4242);
  });

  it('finds every real account in a timeline exactly once', () => {
    const doc = dom(commentTimeline);
    const found = collectIdentities(doc, ALL_AVATAR_SURFACES_SELECTOR);
    expect(found.map((f) => f.login).sort()).toEqual(['maya', 'sam']);
  });

  it('skips bots, apps, organizations, ghost and non-account images', () => {
    const doc = dom(withBotAndOrg);
    const found = collectIdentities(doc, ALL_AVATAR_SURFACES_SELECTOR);
    // Not every <img> is a GitHub account, and a Story ring must never be
    // attached to a bot, an app, an org, or a marketing illustration.
    expect(found).toHaveLength(0);
  });
});
