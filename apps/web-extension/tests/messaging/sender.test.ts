import { describe, expect, it } from 'vitest';
import { isTrustedSender } from '../../src/messaging/sender.js';

const OWN_ID = 'abcdefghijklmnopabcdefghijklmnop';

describe('runtime message sender validation', () => {
  it('accepts a content script running on github.com', () => {
    expect(
      isTrustedSender(
        { id: OWN_ID, tab: { id: 7 }, url: 'https://github.com/alice/repo/pull/3' } as never,
        OWN_ID,
      ),
    ).toBe(true);
  });

  it('rejects a message claiming to be from another extension', () => {
    expect(
      isTrustedSender(
        { id: 'someone-elses-extension', tab: { id: 7 }, url: 'https://github.com/' } as never,
        OWN_ID,
      ),
    ).toBe(false);
  });

  it('rejects a content script on any other origin', () => {
    for (const url of [
      'https://github.com.evil.example/',
      'https://gist.github.com/',
      'http://github.com/',
      'https://evil.example/github.com',
    ]) {
      expect(isTrustedSender({ id: OWN_ID, tab: { id: 7 }, url } as never, OWN_ID)).toBe(false);
    }
  });

  it('rejects a tab sender with no url at all', () => {
    expect(isTrustedSender({ id: OWN_ID, tab: { id: 7 } } as never, OWN_ID)).toBe(false);
  });

  it('rejects an absent sender', () => {
    expect(isTrustedSender(undefined, OWN_ID)).toBe(false);
  });

  it('accepts our own extension pages opened in a TAB', () => {
    // options_ui.open_in_tab makes the settings page a real tab. A tab-first
    // trust rule rejected it and the settings page could not talk to the
    // background at all.
    expect(
      isTrustedSender(
        { id: OWN_ID, tab: { id: 12 }, url: `chrome-extension://${OWN_ID}/options.html` } as never,
        OWN_ID,
      ),
    ).toBe(true);
  });

  it('accepts our own extension pages', () => {
    expect(
      isTrustedSender(
        { id: OWN_ID, url: `chrome-extension://${OWN_ID}/popup.html` } as never,
        OWN_ID,
      ),
    ).toBe(true);
  });

  it('rejects an extension page belonging to a different extension', () => {
    expect(
      isTrustedSender({ id: OWN_ID, url: 'chrome-extension://someoneelse/popup.html' } as never, OWN_ID),
    ).toBe(false);
  });
});
