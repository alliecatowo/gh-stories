import { describe, expect, it } from 'vitest';
import { isMessageAllowed } from '../../src/messaging/policy.js';
import { senderKind } from '../../src/messaging/sender.js';

const ID = 'abcdefghijklmnopabcdefghijklmnop';

describe('per-message sender policy', () => {
  const page = senderKind({ id: ID, tab: { id: 1 }, url: 'https://github.com/a' } as never, ID);
  const own = senderKind({ id: ID, url: `chrome-extension://${ID}/popup.html` } as never, ID);

  it('classifies senders', () => {
    expect(page).toBe('content-script');
    expect(own).toBe('extension-page');
  });

  it('blocks account-level messages from content scripts', () => {
    for (const type of [
      'ghs:session/set-service-url',
      'ghs:settings/delete-account',
      'ghs:settings/sign-out-all',
      'ghs:settings/revoke-session',
      'ghs:settings/update',
      'ghs:session/logout',
      'ghs:session/login-start',
    ]) {
      expect(isMessageAllowed({ type } as never, 'content-script'), type).toBe(false);
      expect(isMessageAllowed({ type } as never, 'extension-page'), type).toBe(true);
    }
  });

  it('allows what the overlay needs, and only follow/unfollow graph actions', () => {
    expect(isMessageAllowed({ type: 'ghs:story/react' } as never, 'content-script')).toBe(true);
    expect(isMessageAllowed({ type: 'ghs:graph/action', action: 'follow' } as never, 'content-script')).toBe(true);
    expect(isMessageAllowed({ type: 'ghs:graph/action', action: 'block' } as never, 'content-script')).toBe(false);
  });
});

import { normalizeServiceOrigin } from '../../src/state/session.js';

describe('service origin validation', () => {
  it('accepts https origins and localhost http', () => {
    expect(normalizeServiceOrigin('https://example.run.app/')).toBe('https://example.run.app');
    expect(normalizeServiceOrigin('http://localhost:8787')).toBe('http://localhost:8787');
  });
  it('rejects other schemes, paths and credentials', () => {
    for (const bad of ['http://example.com', 'javascript:alert(1)', 'https://a.com/x?y=1', 'https://u:p@a.com', 'nope']) {
      expect(() => normalizeServiceOrigin(bad), bad).toThrow();
    }
  });
});
